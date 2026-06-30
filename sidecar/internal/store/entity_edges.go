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

// Neighbor is a Hebbian co-occurrence neighbor with its recency-decayed edge weight
// (weight = co_count · exp(-ln2 · ageDays / HL)). Exposed where the connection
// strength matters — e.g. down-weighting 2nd-order items in an Engram dossier.
type Neighbor struct {
	Norm   string  `json:"norm"`
	Weight float64 `json:"weight"`
}

// HebbianNeighborsWeighted returns the entities most strongly co-occurring with `norm`,
// each with its recency-decayed weight (weight = co_count · exp(-ln2 · ageDays / HL)).
// Only neighbors with weight ≥ minWeight are returned, top `max` by weight (§3.3).
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
	defer rows.Close()
	var cands []Neighbor
	for rows.Next() {
		var other, lastCo string
		var coCount int
		if err := rows.Scan(&other, &coCount, &lastCo); err != nil {
			return nil, err
		}
		w := float64(coCount)
		if t, perr := time.Parse(time.RFC3339, lastCo); perr == nil {
			if age := now.Sub(t).Hours() / 24.0; age > 0 {
				w *= math.Exp(-math.Ln2 * age / hebbianHalfLifeDays)
			}
		}
		if w >= minWeight {
			cands = append(cands, Neighbor{Norm: other, Weight: w})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Weight > cands[j].Weight })
	if len(cands) > max {
		cands = cands[:max]
	}
	return cands, nil
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
