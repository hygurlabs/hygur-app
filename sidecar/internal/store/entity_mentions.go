package store

import (
	"context"
	"fmt"
	"strings"
)

// EntityMention is one (entity, attribute) an item asserts a claim about. The
// normalized form (EntityNorm) is the lookup key shared with the contradiction
// layer; EntityRaw is kept for display/debug. Populated from the cached claims
// (extracted_claims) — see ingest.BackfillEntityIndex / applyItemClaims.
type EntityMention struct {
	EntityNorm string
	EntityRaw  string
	Attribute  string
	AssertedAt string
}

// ReplaceEntityMentions replaces all entity_mentions rows for contentID in one
// transaction (delete-then-insert), so re-indexing an item is idempotent. An
// empty mentions slice just clears the item's rows. Rows with an empty
// EntityNorm are skipped; duplicate (norm, attribute) pairs collapse to one.
func (d *DB) ReplaceEntityMentions(ctx context.Context, contentID string, mentions []EntityMention) error {
	if d == nil || d.db == nil || strings.TrimSpace(contentID) == "" {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("entity mentions: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM entity_mentions WHERE content_id = ?`, contentID); err != nil {
		return fmt.Errorf("entity mentions: clear: %w", err)
	}
	seen := make(map[string]bool, len(mentions))
	for _, m := range mentions {
		if strings.TrimSpace(m.EntityNorm) == "" {
			continue
		}
		key := m.EntityNorm + "\x1f" + m.Attribute
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO entity_mentions (entity_norm, entity_raw, content_id, attribute, asserted_at)
			 VALUES (?, ?, ?, ?, ?)`,
			m.EntityNorm, m.EntityRaw, contentID, m.Attribute, m.AssertedAt); err != nil {
			return fmt.Errorf("entity mentions: insert: %w", err)
		}
	}
	return tx.Commit()
}

// EntityMentionContentIDs returns the distinct content_ids whose claims mention
// any of the normalized entities, most-recently-asserted first, capped at limit
// (default 200). The norms slice is the brick-1 lookup key set — an embedding
// synonymy pass widens it before the call. Empty norms yields nil (no lookup).
func (d *DB) EntityMentionContentIDs(ctx context.Context, norms []string, limit int) ([]string, error) {
	if d == nil || d.db == nil {
		return nil, nil
	}
	clean := make([]string, 0, len(norms))
	for _, n := range norms {
		if strings.TrimSpace(n) != "" {
			clean = append(clean, n)
		}
	}
	if len(clean) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(clean)), ",")
	query := `SELECT content_id FROM entity_mentions
	          WHERE entity_norm IN (` + placeholders + `)
	          GROUP BY content_id
	          ORDER BY MAX(asserted_at) DESC
	          LIMIT ?`
	args := make([]any, 0, len(clean)+1)
	for _, n := range clean {
		args = append(args, n)
	}
	args = append(args, limit)

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("entity mentions: query: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err != nil {
			return nil, fmt.Errorf("entity mentions: scan: %w", err)
		}
		out = append(out, cid)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entity mentions: iterate: %w", err)
	}
	return out, nil
}

// EntityNormsMatching returns, among the given candidate norms, those that exist in
// the entity index, mapped to their mention count. Used by deterministic query→entity
// detection: more mentions = more central to the corpus, so it anchors better.
func (d *DB) EntityNormsMatching(ctx context.Context, norms []string) (map[string]int, error) {
	out := make(map[string]int, len(norms))
	if len(norms) == 0 {
		return out, nil
	}
	ph := make([]string, len(norms))
	args := make([]any, len(norms))
	for i, n := range norms {
		ph[i] = "?"
		args[i] = n
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT entity_norm, COUNT(*) FROM entity_mentions WHERE entity_norm IN (`+strings.Join(ph, ",")+`) GROUP BY entity_norm`, args...)
	if err != nil {
		return nil, fmt.Errorf("entity norms matching: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		var c int
		if err := rows.Scan(&n, &c); err != nil {
			return nil, err
		}
		out[n] = c
	}
	return out, rows.Err()
}
