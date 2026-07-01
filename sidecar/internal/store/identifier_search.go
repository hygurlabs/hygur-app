package store

import (
	"context"
	"strings"

	"github.com/hygur/sidecar/internal/identifier"
)

// UpsertItemNorm writes the alnum-only normalized form of an item's text into the
// materialized item_norm index (identifier.Normalize), so an exact-identifier query matches
// a formatted value via substring regardless of its separators.
func (d *DB) UpsertItemNorm(ctx context.Context, contentID, text string) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO item_norm (content_id, norm) VALUES (?, ?)
		 ON CONFLICT(content_id) DO UPDATE SET norm = excluded.norm`,
		contentID, identifier.Normalize(text))
	return err
}

// RebuildIdentifierIndex repopulates item_norm from every knowledge item — the backfill for
// existing docs (the ingest hook keeps new ones current). Returns the number indexed.
func (d *DB) RebuildIdentifierIndex(ctx context.Context) (int, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT content_id, title, normalized_text FROM knowledge_items`)
	if err != nil {
		return 0, err
	}
	type rec struct{ id, text string }
	var recs []rec
	for rows.Next() {
		var id, title, text string
		if err := rows.Scan(&id, &title, &text); err != nil {
			rows.Close()
			return 0, err
		}
		recs = append(recs, rec{id, title + " " + text})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for i, r := range recs {
		if err := d.UpsertItemNorm(ctx, r.id, r.text); err != nil {
			return i, err
		}
	}
	return len(recs), nil
}

// SearchByIdentifier returns content IDs whose normalized text contains the identifier key
// (pre-normalized via identifier.Normalize/ExtractQuery) as a substring — a deterministic
// exact lookup over the materialized index, most-recent-first. No embeddings.
func (d *DB) SearchByIdentifier(ctx context.Context, key string, limit int) ([]string, error) {
	if strings.TrimSpace(key) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT n.content_id
		FROM item_norm n
		JOIN knowledge_items k ON k.content_id = n.content_id
		WHERE n.norm LIKE '%' || ? || '%'
		ORDER BY k.created_at DESC
		LIMIT ?`, key, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
