package store

import (
	"context"
	"strings"
	"time"
)

// Surprise / novelty store layer (DREAM Phase C, docs/DREAM_PLAN_ADDENDUM.md §2).
// item_surprise is owned by INGESTION (UpsertItemSurprise) and read by the nightly
// consolidation pass (ItemSurpriseByIDs) to nudge salience — the pass never writes
// it, so a re-score can't erase an ingestion-stamped value.

// EntityPriorMentions returns, for the given entity norms, what the entity index knew
// BEFORE this item: the set of known norms and the set of known "norm\x00attribute"
// pairs, drawn from every OTHER item's mentions. The item itself is excluded so a
// re-index of the same content does not read as "already known". The novelty/drift
// signal compares an item's fresh mentions against these.
func (d *DB) EntityPriorMentions(ctx context.Context, contentID string, norms []string) (knownNorms, knownPairs map[string]struct{}, err error) {
	knownNorms = map[string]struct{}{}
	knownPairs = map[string]struct{}{}
	if len(norms) == 0 {
		return knownNorms, knownPairs, nil
	}
	ph := make([]string, len(norms))
	args := make([]any, 0, len(norms)+1)
	for i, n := range norms {
		ph[i] = "?"
		args = append(args, n)
	}
	args = append(args, contentID)
	rows, err := d.db.QueryContext(ctx, `
SELECT DISTINCT entity_norm, attribute FROM entity_mentions
WHERE entity_norm IN (`+strings.Join(ph, ",")+`) AND content_id != ?`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var norm, attr string
		if err := rows.Scan(&norm, &attr); err != nil {
			return nil, nil, err
		}
		knownNorms[norm] = struct{}{}
		knownPairs[norm+"\x00"+attr] = struct{}{}
	}
	return knownNorms, knownPairs, rows.Err()
}

// UpsertItemSurprise stores an item's surprise score (0..1). Idempotent.
func (d *DB) UpsertItemSurprise(ctx context.Context, contentID string, surprise float64) error {
	_, err := d.db.ExecContext(ctx, `
INSERT INTO item_surprise (content_id, surprise, computed_at)
VALUES (?, ?, ?)
ON CONFLICT(content_id) DO UPDATE SET surprise=excluded.surprise, computed_at=excluded.computed_at`,
		contentID, surprise, time.Now().UTC().Format(time.RFC3339))
	return err
}

// ItemSurpriseByIDs batch-reads the surprise score for the given items (absent → 0).
func (d *DB) ItemSurpriseByIDs(ctx context.Context, contentIDs []string) (map[string]float64, error) {
	out := make(map[string]float64, len(contentIDs))
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
		`SELECT content_id, surprise FROM item_surprise WHERE content_id IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid string
		var s float64
		if err := rows.Scan(&cid, &s); err != nil {
			return nil, err
		}
		out[cid] = s
	}
	return out, rows.Err()
}
