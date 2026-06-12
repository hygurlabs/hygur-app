package store

import (
	"context"
	"time"
)

// Per-item access signal — "Quand Hygur rêve" Phase 0 (docs/DREAM_PLAN.md). The
// foundation for memory consolidation: importance is scored (later) from how often
// and how recently an item was actually USED to answer the user, not from raw
// vector matches. OBSERVE-ONLY for now — these are written but not yet read by any
// tiering/eviction logic.

// BumpItemAccess records that these items were cited in an answer: increments
// hit_count and stamps last_accessed_at, in one transaction. Deduped (a content_id
// cited twice in a turn counts once). Best-effort and meant to be called detached
// from the request path — it must never block a response.
func (d *DB) BumpItemAccess(ctx context.Context, contentIDs []string) error {
	if len(contentIDs) == 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO item_access (content_id, hit_count, last_accessed_at)
VALUES (?, 1, ?)
ON CONFLICT(content_id) DO UPDATE SET hit_count = hit_count + 1, last_accessed_at = excluded.last_accessed_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	seen := make(map[string]bool, len(contentIDs))
	for _, id := range contentIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if _, err := stmt.ExecContext(ctx, id, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DBSizeBytes returns the logical database size (page_count × page_size). Works on
// SQLCipher too — it's the in-DB page accounting, independent of the file path and
// of any -wal/-shm overhang. The per-tenant storage-metering signal.
func (d *DB) DBSizeBytes(ctx context.Context) (int64, error) {
	var pageCount, pageSize int64
	if err := d.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0, err
	}
	if err := d.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, err
	}
	return pageCount * pageSize, nil
}

// ItemAccessStats returns the total knowledge_items count and how many have ever
// been accessed (hit_count > 0) — a coarse "how much of the corpus is actually
// used" gauge for the admin surface while we gather the real distribution.
func (d *DB) ItemAccessStats(ctx context.Context) (items int, accessed int, err error) {
	if err = d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_items`).Scan(&items); err != nil {
		return 0, 0, err
	}
	if err = d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM item_access WHERE hit_count > 0`).Scan(&accessed); err != nil {
		return 0, 0, err
	}
	return items, accessed, nil
}
