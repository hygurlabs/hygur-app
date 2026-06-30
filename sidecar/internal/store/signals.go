package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Memory-consolidation signals — the store layer for "Quand Hygur rêve"
// (DREAM_PLAN Phase 1, docs/DREAM_PLAN_ADDENDUM.md). The nightly pass reads the
// vector footprint + per-item link/exempt signals, scores salience/strength
// (retrieval/salience.go), and persists the result to item_signals. SHADOW today:
// computed and stored, but no eviction yet (Phase E).

// VectorFootprint returns the working-memory (vector) footprint: number of stored
// embeddings, their total byte size, and the embedding dimension (bytes/4, 0 when
// empty). This is the per-tenant budget signal that replaces any calendar cutoff
// (DREAM_PLAN §6). length(embedding) is the raw BLOB size in SQLite.
func (d *DB) VectorFootprint(ctx context.Context) (rows int64, bytes int64, dim int, err error) {
	if err = d.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(length(embedding)), 0) FROM chunk_vectors`).Scan(&rows, &bytes); err != nil {
		return 0, 0, 0, err
	}
	if rows > 0 {
		var oneLen int
		if e := d.db.QueryRowContext(ctx, `SELECT length(embedding) FROM chunk_vectors LIMIT 1`).Scan(&oneLen); e == nil {
			dim = oneLen / 4 // float32 little-endian
		}
	}
	return rows, bytes, dim, nil
}

// ItemVectorBytes returns, per content_id, the total bytes its chunk embeddings
// occupy (absent / 0 when nothing is embedded). The per-item cost used to draw the
// budget line during consolidation tiering.
func (d *DB) ItemVectorBytes(ctx context.Context, contentIDs []string) (map[string]int64, error) {
	out := make(map[string]int64, len(contentIDs))
	if len(contentIDs) == 0 {
		return out, nil
	}
	ph := make([]string, len(contentIDs))
	args := make([]any, len(contentIDs))
	for i, id := range contentIDs {
		ph[i] = "?"
		args[i] = id
	}
	rows, err := d.db.QueryContext(ctx, `
SELECT c.content_id, COALESCE(SUM(length(cv.embedding)), 0)
FROM chunks c JOIN chunk_vectors cv ON cv.chunk_id = c.chunk_id
WHERE c.content_id IN (`+strings.Join(ph, ",")+`)
GROUP BY c.content_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid string
		var b int64
		if err := rows.Scan(&cid, &b); err != nil {
			return nil, err
		}
		out[cid] = b
	}
	return out, rows.Err()
}

// LinkSignal aggregates the deterministic "importance link" signals for an item:
// how many durable structures reference it, and whether it is hard-exempt from
// eviction (DREAM_PLAN §3 / addendum §1.3-1.4). Open-contradiction membership is a
// deliberate TODO for v1 — contradiction_cache is keyed by scope with a JSON
// conflict set, so folding it in needs a parse; it is additive and the weights are
// shadow-tunable anyway.
type LinkSignal struct {
	LinkCount        int
	StandingDecision bool
	ActiveProject    bool
	Pinned           bool
}

// Exempt reports whether the item is hard-exempt from eviction (never goes cold).
func (l LinkSignal) Exempt() bool { return l.StandingDecision || l.ActiveProject || l.Pinned }

// ItemLinkSignals batch-computes per-item link and exempt signals from standing
// decisions and active-project membership (pins ride on project_links.pin_state).
// Items with no links are simply absent from the map (zero value).
func (d *DB) ItemLinkSignals(ctx context.Context, contentIDs []string) (map[string]LinkSignal, error) {
	out := make(map[string]LinkSignal, len(contentIDs))
	if len(contentIDs) == 0 {
		return out, nil
	}
	ph := make([]string, len(contentIDs))
	args := make([]any, len(contentIDs))
	for i, id := range contentIDs {
		ph[i] = "?"
		args[i] = id
	}
	in := "(" + strings.Join(ph, ",") + ")"

	// Standing decisions: the item is itself a confirmed decision.
	decRows, err := d.db.QueryContext(ctx,
		`SELECT content_id FROM decision_attrs WHERE status='standing' AND content_id IN `+in, args...)
	if err != nil {
		return nil, err
	}
	for decRows.Next() {
		var cid string
		if err := decRows.Scan(&cid); err != nil {
			decRows.Close()
			return nil, err
		}
		s := out[cid]
		s.StandingDecision = true
		s.LinkCount++
		out[cid] = s
	}
	decRows.Close()
	if err := decRows.Err(); err != nil {
		return nil, err
	}

	// Active-project membership (+ pin_state). Archived projects don't count.
	projRows, err := d.db.QueryContext(ctx, `
SELECT pl.content_id, COUNT(*), MAX(pl.pin_state)
FROM project_links pl JOIN projects p ON p.project_id = pl.project_id
WHERE p.archived = 0 AND pl.content_id IN `+in+`
GROUP BY pl.content_id`, args...)
	if err != nil {
		return nil, err
	}
	for projRows.Next() {
		var cid string
		var n, pinned int
		if err := projRows.Scan(&cid, &n, &pinned); err != nil {
			projRows.Close()
			return nil, err
		}
		s := out[cid]
		s.ActiveProject = true
		s.LinkCount += n
		if pinned != 0 {
			s.Pinned = true
		}
		out[cid] = s
	}
	projRows.Close()
	return out, projRows.Err()
}

// ItemSignal is one row of item_signals: the nightly consolidation scoring for an
// item. Tier is "hot" (vectors kept) or "cold" (vectors evictable — Phase E).
type ItemSignal struct {
	ContentID string
	Salience  float64
	Strength  float64
	Surprise  float64
	Exempt    bool
	Tier      string
	ScoredAt  time.Time
}

// UpsertItemSignals writes a batch of computed signals in one transaction.
// Idempotent per content_id (re-running a pass overwrites the previous score).
// rehydrated_at is intentionally left untouched — it is owned by the re-hydration
// path (Phase E), not the scoring pass.
func (d *DB) UpsertItemSignals(ctx context.Context, sigs []ItemSignal) error {
	if len(sigs) == 0 {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO item_signals (content_id, salience, strength, surprise, exempt, tier, scored_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(content_id) DO UPDATE SET
    salience=excluded.salience, strength=excluded.strength, surprise=excluded.surprise,
    exempt=excluded.exempt, tier=excluded.tier, scored_at=excluded.scored_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, s := range sigs {
		ex := 0
		if s.Exempt {
			ex = 1
		}
		if _, err := stmt.ExecContext(ctx, s.ContentID, s.Salience, s.Strength, s.Surprise, ex, s.Tier,
			s.ScoredAt.UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ItemSignalsByIDs reads back the stored signals for the given content ids (absent
// when never scored). Used by inspection/metrics and tests.
func (d *DB) ItemSignalsByIDs(ctx context.Context, contentIDs []string) (map[string]ItemSignal, error) {
	out := make(map[string]ItemSignal, len(contentIDs))
	if len(contentIDs) == 0 {
		return out, nil
	}
	ph := make([]string, len(contentIDs))
	args := make([]any, len(contentIDs))
	for i, id := range contentIDs {
		ph[i] = "?"
		args[i] = id
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT content_id, salience, strength, surprise, exempt, tier, COALESCE(scored_at,'')
FROM item_signals WHERE content_id IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s ItemSignal
		var ex int
		var scoredAt string
		if err := rows.Scan(&s.ContentID, &s.Salience, &s.Strength, &s.Surprise, &ex, &s.Tier, &scoredAt); err != nil {
			return nil, err
		}
		s.Exempt = ex != 0
		if t, perr := time.Parse(time.RFC3339, scoredAt); perr == nil {
			s.ScoredAt = t
		}
		out[s.ContentID] = s
	}
	return out, rows.Err()
}

// reconciledConflictLite is the minimal shape of a cached reconciled conflict — just
// enough to tell which items are in a live (kind="conflict") contradiction. Mirrors
// contradict.ReconciledConflict's JSON without importing the package (keeps store
// decoupled). Dismissed is applied per-request, never cached, so it is absent here.
type reconciledConflictLite struct {
	Members []struct {
		SourceID string `json:"source_id"`
	} `json:"members"`
	Verdict struct {
		Kind string `json:"kind"`
	} `json:"verdict"`
}

// OpenContradictionContentIDs returns the set of content_ids that are members of a
// live ("conflict") reconciled contradiction — the same cache the authority layer
// reads (scope ""). These items are surfaced, important, and hard-exempt from
// eviction (DREAM_PLAN §3). Empty (not an error) when nothing has been computed yet.
func (d *DB) OpenContradictionContentIDs(ctx context.Context) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	js, _, _, found, err := d.GetContradictionCache(ctx, "")
	if err != nil {
		return nil, err
	}
	if !found || js == "" {
		return out, nil
	}
	var conflicts []reconciledConflictLite
	if err := json.Unmarshal([]byte(js), &conflicts); err != nil {
		return nil, err
	}
	for _, c := range conflicts {
		if c.Verdict.Kind != "conflict" || len(c.Members) < 2 {
			continue
		}
		for _, m := range c.Members {
			if m.SourceID != "" {
				out[m.SourceID] = struct{}{}
			}
		}
	}
	return out, nil
}

// ---- Calibration read API (DREAM Phase A) ----

// StatSummary is a 0..1 distribution: average/min/max + 10 buckets ([i/10,(i+1)/10)).
type StatSummary struct {
	Avg       float64 `json:"avg"`
	Min       float64 `json:"min"`
	Max       float64 `json:"max"`
	Histogram [10]int `json:"histogram"`
}

// ItemSignalRow is one row of a top-N inspection list.
type ItemSignalRow struct {
	ContentID  string  `json:"content_id"`
	SourceType string  `json:"source_type"`
	Title      string  `json:"title"`
	Salience   float64 `json:"salience"`
	Strength   float64 `json:"strength"`
	Surprise   float64 `json:"surprise"`
	Exempt     bool    `json:"exempt"`
	Tier       string  `json:"tier"`
}

// SignalsSummary aggregates item_signals for calibration. Vector.BudgetBytes is left
// 0 for the caller to fill (the budget constant lives in the scheduler package).
type SignalsSummary struct {
	Scored   int         `json:"scored"`
	Hot      int         `json:"hot"`
	Cold     int         `json:"cold"`
	Exempt   int         `json:"exempt"`
	Salience StatSummary `json:"salience"`
	Strength StatSummary `json:"strength"`
	Surprise struct {
		CountNonzero int     `json:"count_nonzero"`
		Avg          float64 `json:"avg"`
		Histogram    [10]int `json:"histogram"`
	} `json:"surprise"`
	Vector struct {
		Rows        int64 `json:"rows"`
		Bytes       int64 `json:"bytes"`
		Dim         int   `json:"dim"`
		BudgetBytes int64 `json:"budget_bytes"`
	} `json:"vector"`
	EntityEdges  int64           `json:"entity_edges"` // Hebbian co-occurrence edges (Phase D)
	LastScoredAt string          `json:"last_scored_at,omitempty"`
	TopSalience  []ItemSignalRow `json:"top_salience"`
	TopSurprise  []ItemSignalRow `json:"top_surprise"`
}

// histogramInto fills a [10]int from a 0..1 column of item_signals (bucket = floor
// of col*10, clamped to 9). col is a fixed internal name — never user input.
func (d *DB) histogramInto(ctx context.Context, col string, h *[10]int) error {
	rows, err := d.db.QueryContext(ctx,
		`SELECT MIN(CAST(`+col+`*10 AS INTEGER), 9) AS b, COUNT(*) FROM item_signals GROUP BY b`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var b, n int
		if err := rows.Scan(&b, &n); err != nil {
			return err
		}
		if b >= 0 && b < 10 {
			h[b] = n
		}
	}
	return rows.Err()
}

// ItemSignalsSummary returns the aggregate distribution of item_signals (DREAM
// Phase A — calibration). Vector.BudgetBytes is left 0 for the caller to fill.
func (d *DB) ItemSignalsSummary(ctx context.Context) (*SignalsSummary, error) {
	s := &SignalsSummary{}
	var lastScored string
	row := d.db.QueryRowContext(ctx, `
SELECT COUNT(*),
       COALESCE(SUM(CASE WHEN tier='cold' THEN 1 ELSE 0 END),0),
       COALESCE(SUM(exempt),0),
       COALESCE(AVG(salience),0), COALESCE(MIN(salience),0), COALESCE(MAX(salience),0),
       COALESCE(AVG(strength),0), COALESCE(MIN(strength),0), COALESCE(MAX(strength),0),
       COALESCE(SUM(CASE WHEN surprise>0 THEN 1 ELSE 0 END),0), COALESCE(AVG(surprise),0),
       COALESCE(MAX(scored_at),'')
FROM item_signals`)
	if err := row.Scan(&s.Scored, &s.Cold, &s.Exempt,
		&s.Salience.Avg, &s.Salience.Min, &s.Salience.Max,
		&s.Strength.Avg, &s.Strength.Min, &s.Strength.Max,
		&s.Surprise.CountNonzero, &s.Surprise.Avg, &lastScored); err != nil {
		return nil, err
	}
	s.Hot = s.Scored - s.Cold
	s.LastScoredAt = lastScored
	if s.Scored > 0 {
		if err := d.histogramInto(ctx, "salience", &s.Salience.Histogram); err != nil {
			return nil, err
		}
		if err := d.histogramInto(ctx, "strength", &s.Strength.Histogram); err != nil {
			return nil, err
		}
		if err := d.histogramInto(ctx, "surprise", &s.Surprise.Histogram); err != nil {
			return nil, err
		}
	}
	r, b, dim, err := d.VectorFootprint(ctx)
	if err != nil {
		return nil, err
	}
	s.Vector.Rows, s.Vector.Bytes, s.Vector.Dim = r, b, dim
	if s.EntityEdges, err = d.EntityEdgeCount(ctx); err != nil {
		return nil, err
	}
	if s.TopSalience, err = d.TopItemSignals(ctx, "salience", 15); err != nil {
		return nil, err
	}
	if s.TopSurprise, err = d.TopItemSignals(ctx, "surprise", 15); err != nil {
		return nil, err
	}
	return s, nil
}

// TopItemSignals returns the top-N scored items by "salience" (default) or
// "surprise" — for spot-checking the scoring during calibration.
func (d *DB) TopItemSignals(ctx context.Context, by string, n int) ([]ItemSignalRow, error) {
	order := "s.salience"
	if by == "surprise" {
		order = "s.surprise"
	}
	if n <= 0 || n > 100 {
		n = 15
	}
	rows, err := d.db.QueryContext(ctx, `
SELECT s.content_id, COALESCE(k.source_type,''), COALESCE(k.title,''),
       s.salience, s.strength, s.surprise, s.exempt, s.tier
FROM item_signals s
LEFT JOIN knowledge_items k ON k.content_id = s.content_id
ORDER BY `+order+` DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ItemSignalRow, 0, n)
	for rows.Next() {
		var r ItemSignalRow
		var ex int
		if err := rows.Scan(&r.ContentID, &r.SourceType, &r.Title,
			&r.Salience, &r.Strength, &r.Surprise, &ex, &r.Tier); err != nil {
			return nil, err
		}
		r.Exempt = ex != 0
		out = append(out, r)
	}
	return out, rows.Err()
}
