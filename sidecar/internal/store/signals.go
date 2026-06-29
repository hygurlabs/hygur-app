package store

import (
	"context"
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
