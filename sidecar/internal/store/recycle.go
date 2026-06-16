package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// RecycleEntry is a soft-deleted knowledge item retained for the grace period.
// It carries everything needed to re-ingest the item (restore) without touching
// the source server.
type RecycleEntry struct {
	ContentID      string
	SourceType     string
	SourcePath     string
	Title          string
	NormalizedText string
	Metadata       string // raw JSON, as stored on the knowledge_items row
	SourceRef      string
	ItemCreatedAt  time.Time
	MissCount      int
}

// messageRefOf strips a trailing ":att:<filename>" so an attachment item follows
// its parent mail: presence is decided on the parent message ref. A plain mail
// ref ("proton:<id>") is returned unchanged.
func messageRefOf(sourceRef string) string {
	if i := strings.Index(sourceRef, ":att:"); i >= 0 {
		return sourceRef[:i]
	}
	return sourceRef
}

// MoveToRecycle copies the knowledge_items row for contentID into kb_recycle then
// deletes it. The delete cascades (chunks/sections/vectors/FTS/claims/tags), so the
// item disappears from every read surface at once while remaining restorable. A
// re-move of an already-recycled id refreshes the snapshot and resets miss_count.
// No-op (no error) when the content_id has no active row.
func (d *DB) MoveToRecycle(ctx context.Context, contentID string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("move to recycle: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO kb_recycle
			(content_id, source_type, source_path, title, normalized_text, metadata,
			 source_ref, item_created_at, miss_count)
		SELECT content_id, source_type, source_path, title, normalized_text, metadata,
			   COALESCE(json_extract(metadata, '$.source_ref'), ''), created_at, 1
		FROM knowledge_items WHERE content_id = ?`, contentID)
	if err != nil {
		return fmt.Errorf("move to recycle: copy: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// No active row — nothing to recycle. Commit the no-op (the INSERT...SELECT
		// matched nothing, so kb_recycle is untouched).
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_items WHERE content_id = ?`, contentID); err != nil {
		return fmt.Errorf("move to recycle: delete: %w", err)
	}
	return tx.Commit()
}

// ActiveSourceRefsByPrefix returns content_id → source_ref for active mail items
// whose source_ref starts with prefix (e.g. "proton:"). Items with no source_ref
// are skipped. Used by reconcile to find pruning candidates.
func (d *DB) ActiveSourceRefsByPrefix(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT content_id, json_extract(metadata, '$.source_ref')
		FROM knowledge_items
		WHERE json_extract(metadata, '$.source_ref') LIKE ? || '%'`, prefix)
	if err != nil {
		return nil, fmt.Errorf("active source_refs by prefix: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id string
		var ref sql.NullString
		if err := rows.Scan(&id, &ref); err != nil {
			return nil, fmt.Errorf("active source_refs by prefix: scan: %w", err)
		}
		if ref.Valid && ref.String != "" {
			out[id] = ref.String
		}
	}
	return out, rows.Err()
}

// ListRecycleByPrefix returns recycle entries whose source_ref starts with prefix.
func (d *DB) ListRecycleByPrefix(ctx context.Context, prefix string) ([]RecycleEntry, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT content_id, source_type, COALESCE(source_path,''), title, normalized_text,
		       COALESCE(metadata,''), source_ref, item_created_at, miss_count
		FROM kb_recycle
		WHERE source_ref LIKE ? || '%'`, prefix)
	if err != nil {
		return nil, fmt.Errorf("list recycle by prefix: %w", err)
	}
	defer rows.Close()
	var out []RecycleEntry
	for rows.Next() {
		var e RecycleEntry
		var created sql.NullTime
		if err := rows.Scan(&e.ContentID, &e.SourceType, &e.SourcePath, &e.Title,
			&e.NormalizedText, &e.Metadata, &e.SourceRef, &created, &e.MissCount); err != nil {
			return nil, fmt.Errorf("list recycle by prefix: scan: %w", err)
		}
		if created.Valid {
			e.ItemCreatedAt = created.Time
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// BumpRecycleMiss increments a recycle entry's consecutive-miss counter.
func (d *DB) BumpRecycleMiss(ctx context.Context, contentID string) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE kb_recycle SET miss_count = miss_count + 1 WHERE content_id = ?`, contentID)
	if err != nil {
		return fmt.Errorf("bump recycle miss: %w", err)
	}
	return nil
}

// DeleteRecycle hard-removes a recycle entry (after a successful restore, or once
// it is past the grace period).
func (d *DB) DeleteRecycle(ctx context.Context, contentID string) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM kb_recycle WHERE content_id = ?`, contentID)
	if err != nil {
		return fmt.Errorf("delete recycle: %w", err)
	}
	return nil
}

// CountActiveBySourceRefPrefix counts active items for a prefix — a safety valve
// the reconcile endpoint uses to refuse an empty "present" set when the KB still
// holds items for that source (guards against a buggy edge wiping everything).
func (d *DB) CountActiveBySourceRefPrefix(ctx context.Context, prefix string) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM knowledge_items
		WHERE json_extract(metadata, '$.source_ref') LIKE ? || '%'`, prefix).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count active by prefix: %w", err)
	}
	return n, nil
}

// ReconcilePlan is the outcome of the pure-store half of deletion reconciliation.
// Restore lists recycle entries whose message ref reappeared on the server; the
// caller re-ingests them (re-embedding) then calls DeleteRecycle.
type ReconcilePlan struct {
	Recycled int
	Purged   int
	Restore  []RecycleEntry
}

// ReconcileMailRefs is the deterministic, transactional half of deletion
// reconciliation for one source prefix (e.g. "proton:") given the authoritative
// set of message refs currently present on the server. It NEVER decides anything
// from absence alone unless the caller has already verified the enumeration is
// complete — that gate lives at the edge/handler.
//
//   - active item whose message ref ∉ seen  → moved to recycle (Recycled++)
//   - recycle entry whose message ref ∈ seen → returned in Restore (caller re-ingests)
//   - recycle entry whose message ref ∉ seen → miss_count++, then hard-deleted once
//     it has been missing for ≥ grace consecutive passes (Purged++)
//
// Attachment items ("…:att:<file>") follow their parent message ref.
func (d *DB) ReconcileMailRefs(ctx context.Context, prefix string, seen map[string]struct{}, grace int) (ReconcilePlan, error) {
	var plan ReconcilePlan
	if grace < 1 {
		grace = 1
	}

	// 1) Active items absent from the server → recycle.
	active, err := d.ActiveSourceRefsByPrefix(ctx, prefix)
	if err != nil {
		return plan, err
	}
	justRecycled := map[string]struct{}{}
	for id, ref := range active {
		if _, present := seen[messageRefOf(ref)]; present {
			continue
		}
		if err := d.MoveToRecycle(ctx, id); err != nil {
			return plan, err
		}
		justRecycled[id] = struct{}{}
		plan.Recycled++
	}

	// 2) Recycle entries: restore the reappeared, age out the rest. Items recycled
	// in step 1 of THIS pass are skipped — they start at miss_count 1 and must not
	// be aged again in the same pass (else grace burns a count per pass too fast).
	recycled, err := d.ListRecycleByPrefix(ctx, prefix)
	if err != nil {
		return plan, err
	}
	for _, e := range recycled {
		if _, fresh := justRecycled[e.ContentID]; fresh {
			continue
		}
		if _, present := seen[messageRefOf(e.SourceRef)]; present {
			plan.Restore = append(plan.Restore, e)
			continue
		}
		if e.MissCount+1 >= grace {
			if err := d.DeleteRecycle(ctx, e.ContentID); err != nil {
				return plan, err
			}
			plan.Purged++
		} else if err := d.BumpRecycleMiss(ctx, e.ContentID); err != nil {
			return plan, err
		}
	}
	return plan, nil
}
