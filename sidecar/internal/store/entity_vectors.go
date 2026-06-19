package store

import (
	"context"
	"fmt"
	"strings"
)

// EntityVector is a normalized entity and its embedding (used internally by the
// synonymy expansion; embeddings stay in the store layer with the cosine math).
type EntityVector struct {
	Norm string
	Vec  []float32
}

// UpsertEntityVector stores (or replaces) the embedding for a normalized entity
// under the given model. Empty norm or vec is a no-op.
func (d *DB) UpsertEntityVector(ctx context.Context, norm string, vec []float32, model string) error {
	if d == nil || d.db == nil || strings.TrimSpace(norm) == "" || len(vec) == 0 {
		return nil
	}
	if _, err := d.db.ExecContext(ctx,
		`INSERT INTO entity_vectors (entity_norm, embedding, model) VALUES (?, ?, ?)
		 ON CONFLICT(entity_norm) DO UPDATE SET embedding = excluded.embedding, model = excluded.model`,
		norm, SerializeVector(vec), model); err != nil {
		return fmt.Errorf("entity vectors: upsert: %w", err)
	}
	return nil
}

// EntityNormsNeedingVector returns distinct entity_norms present in
// entity_mentions that have no entity_vectors row for the current model (new
// entities, or all of them after a model change). Capped at limit (default 500).
func (d *DB) EntityNormsNeedingVector(ctx context.Context, model string, limit int) ([]string, error) {
	if d == nil || d.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 500
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT DISTINCT m.entity_norm FROM entity_mentions m
		 WHERE NOT EXISTS (
		     SELECT 1 FROM entity_vectors v
		     WHERE v.entity_norm = m.entity_norm AND v.model = ?
		 )
		 LIMIT ?`, model, limit)
	if err != nil {
		return nil, fmt.Errorf("entity vectors: needing: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("entity vectors: needing scan: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// SimilarEntityNorms returns the entity_norms whose stored embedding (for model)
// has cosine similarity >= threshold to queryVec, most-similar first, capped at
// max (default 10). This is the brick-2 synonymy expansion of a queried entity.
// Empty queryVec yields nil. Vectors of a different dimension are skipped (a
// stale model row never corrupts the result).
func (d *DB) SimilarEntityNorms(ctx context.Context, queryVec []float32, model string, threshold float64, max int) ([]string, error) {
	if d == nil || d.db == nil || len(queryVec) == 0 {
		return nil, nil
	}
	if max <= 0 {
		max = 10
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT entity_norm, embedding FROM entity_vectors WHERE model = ?`, model)
	if err != nil {
		return nil, fmt.Errorf("entity vectors: load: %w", err)
	}
	defer rows.Close()

	type scored struct {
		norm string
		sim  float64
	}
	var hits []scored
	for rows.Next() {
		var norm string
		var blob []byte
		if err := rows.Scan(&norm, &blob); err != nil {
			return nil, fmt.Errorf("entity vectors: load scan: %w", err)
		}
		vec, derr := DeserializeVector(blob)
		if derr != nil || len(vec) != len(queryVec) {
			continue
		}
		if sim := cosineSimilarity(queryVec, vec); sim >= threshold {
			hits = append(hits, scored{norm: norm, sim: sim})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entity vectors: load iterate: %w", err)
	}

	// Insertion sort by similarity desc (bounded N), then cap.
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j-1].sim < hits[j].sim; j-- {
			hits[j-1], hits[j] = hits[j], hits[j-1]
		}
	}
	if len(hits) > max {
		hits = hits[:max]
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.norm)
	}
	return out, nil
}
