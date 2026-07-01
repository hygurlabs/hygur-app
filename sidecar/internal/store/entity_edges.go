package store

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"
)

// Hebbian entity edges (DREAM Phase D, docs/DREAM_PLAN_ADDENDUM.md §3). Entities that
// co-occur in an item strengthen their undirected edge ("fire together, wire
// together"); the weight decays with time. Norms passed in are already normalized by
// the callers (ingest derives them from entity_mentions; retrieval uses
// contradict.NormKey) so this layer stays decoupled from the contradiction package.

const (
	hebbianHalfLifeDays = 120.0 // edge-weight half-life (addendum §3.3)
	hebbianKMax         = 12    // max distinct entities per item folded into pairs (§3.2)
)

// UpsertCoOccurrences increments the edge between every unordered pair of the given
// entity norms, stamping last_co_at = `at` (when the co-occurrence was observed).
// Deduped + sorted (canonical a<b) + capped at K_MAX entities (≤66 pairs) so a noisy
// item can't explode the table. No-op for <2 distinct norms. One transaction.
func (d *DB) UpsertCoOccurrences(ctx context.Context, norms []string, at string) error {
	seen := make(map[string]struct{}, len(norms))
	uniq := make([]string, 0, len(norms))
	for _, n := range norms {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		uniq = append(uniq, n)
	}
	if len(uniq) < 2 {
		return nil
	}
	sort.Strings(uniq)
	if len(uniq) > hebbianKMax {
		uniq = uniq[:hebbianKMax]
	}
	if strings.TrimSpace(at) == "" {
		at = time.Now().UTC().Format(time.RFC3339)
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO entity_edges (entity_a, entity_b, co_count, last_co_at)
VALUES (?, ?, 1, ?)
ON CONFLICT(entity_a, entity_b) DO UPDATE SET co_count = co_count + 1, last_co_at = excluded.last_co_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i := 0; i < len(uniq); i++ {
		for j := i + 1; j < len(uniq); j++ {
			if _, err := stmt.ExecContext(ctx, uniq[i], uniq[j], at); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Neighbor is a Hebbian co-occurrence neighbor with its association weight — NPMI
// (normalized pointwise mutual information) between the two entities, recency-decayed.
type Neighbor struct {
	Norm   string  `json:"norm"`
	Weight float64 `json:"weight"`
}

// HebbianNeighborsWeighted returns the entities most strongly ASSOCIATED with `norm`,
// each with a weight = NPMI(norm, other) · recencyDecay. NPMI (normalized pointwise
// mutual information) measures how much two entities co-occur beyond chance: it divides
// the raw co-occurrence by what each entity's own frequency would predict, so a
// super-hub — an entity co-occurring with almost everything, like the corpus owner —
// sinks even with a huge raw co_count, while a specific pair surfaces. NPMI ∈ [-1,1]
// (1 = always together, 0 = independent, <0 = anti-correlated); the recency factor then
// scales it in (0,1]. Only neighbors with weight ≥ minWeight are returned (minWeight 0
// keeps positively-associated pairs), top `max` by weight.
func (d *DB) HebbianNeighborsWeighted(ctx context.Context, norm string, now time.Time, minWeight float64, max int) ([]Neighbor, error) {
	if strings.TrimSpace(norm) == "" {
		return nil, nil
	}
	if max <= 0 {
		max = 10
	}
	rows, err := d.db.QueryContext(ctx, `
SELECT CASE WHEN entity_a = ? THEN entity_b ELSE entity_a END AS other, co_count, last_co_at
FROM entity_edges WHERE entity_a = ? OR entity_b = ?`, norm, norm, norm)
	if err != nil {
		return nil, err
	}
	type edge struct {
		other   string
		coCount int
		recency float64
	}
	var edges []edge
	neighborNorms := make([]string, 0)
	for rows.Next() {
		var other, lastCo string
		var coCount int
		if err := rows.Scan(&other, &coCount, &lastCo); err != nil {
			rows.Close()
			return nil, err
		}
		rec := 1.0
		if t, perr := time.Parse(time.RFC3339, lastCo); perr == nil {
			if age := now.Sub(t).Hours() / 24.0; age > 0 {
				rec = math.Exp(-math.Ln2 * age / hebbianHalfLifeDays)
			}
		}
		edges = append(edges, edge{other, coCount, rec})
		neighborNorms = append(neighborNorms, other)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(edges) == 0 {
		return nil, nil
	}
	// Marginals (distinct items per entity) + corpus size N turn raw co-counts into
	// NPMI. N = number of distinct items carrying any mention — the co-occurrence
	// "contexts". Without marginals (an isolated graph) no NPMI is defined → no neighbors.
	marg, total, err := d.entityMentionMarginals(ctx, append(neighborNorms, norm))
	if err != nil {
		return nil, err
	}
	countA := marg[norm]
	if total <= 0 || countA <= 0 {
		return nil, nil
	}
	nF, ln2 := float64(total), math.Log(2)
	var cands []Neighbor
	for _, e := range edges {
		cb := marg[e.other]
		if cb <= 0 || e.coCount <= 0 {
			continue
		}
		pab := float64(e.coCount) / nF
		var npmi float64
		if pab >= 1 {
			npmi = 1
		} else {
			// pmi = log2( co·N / (countA·countB) ); npmi = pmi / -log2(pab).
			pmi := math.Log(float64(e.coCount)*nF/(float64(countA)*float64(cb))) / ln2
			npmi = pmi / (-math.Log(pab) / ln2)
		}
		if w := npmi * e.recency; w >= minWeight {
			cands = append(cands, Neighbor{Norm: e.other, Weight: w})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Weight > cands[j].Weight })
	if len(cands) > max {
		cands = cands[:max]
	}
	return cands, nil
}

// entityMentionMarginals returns, for each given norm, the number of DISTINCT items
// mentioning it, plus the total number of distinct items carrying any mention (the
// corpus size N for the co-occurrence probability space). Feeds NPMI edge weighting.
func (d *DB) entityMentionMarginals(ctx context.Context, norms []string) (map[string]int, int, error) {
	out := make(map[string]int, len(norms))
	var total int
	if err := d.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT content_id) FROM entity_mentions`).Scan(&total); err != nil {
		return nil, 0, err
	}
	seen := make(map[string]bool, len(norms))
	uniq := make([]string, 0, len(norms))
	for _, n := range norms {
		if strings.TrimSpace(n) != "" && !seen[n] {
			seen[n] = true
			uniq = append(uniq, n)
		}
	}
	if len(uniq) == 0 {
		return out, total, nil
	}
	ph := make([]string, len(uniq))
	args := make([]any, len(uniq))
	for i, n := range uniq {
		ph[i] = "?"
		args[i] = n
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT entity_norm, COUNT(DISTINCT content_id) FROM entity_mentions WHERE entity_norm IN (`+strings.Join(ph, ",")+`) GROUP BY entity_norm`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		var c int
		if err := rows.Scan(&n, &c); err != nil {
			return nil, 0, err
		}
		out[n] = c
	}
	return out, total, rows.Err()
}

// HebbianNeighbors returns just the neighbor norms (weights dropped), top `max` by
// weight — the common case for spreading-activation expansion.
func (d *DB) HebbianNeighbors(ctx context.Context, norm string, now time.Time, minWeight float64, max int) ([]string, error) {
	ns, err := d.HebbianNeighborsWeighted(ctx, norm, now, minWeight, max)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Norm
	}
	return out, nil
}

// EntityEdgeCount returns the number of Hebbian edges (metering / calibration).
func (d *DB) EntityEdgeCount(ctx context.Context) (int64, error) {
	var n int64
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entity_edges`).Scan(&n)
	return n, err
}

// ClearEntityEdges truncates the edge table — used by the backfill so it can rebuild
// a consistent graph from the cached claims (one count per item) idempotently.
func (d *DB) ClearEntityEdges(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM entity_edges`)
	return err
}
