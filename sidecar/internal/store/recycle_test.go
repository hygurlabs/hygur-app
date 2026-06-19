package store

import (
	"context"
	"testing"
	"time"
)

// insertMail is a tiny helper: a mail knowledge_item carrying a source_ref in
// metadata (the shape the edge ingest path produces).
func insertMail(t *testing.T, db *DB, ctx context.Context, contentID, sourceRef string) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.InsertKnowledgeItem(ctx, &KnowledgeItem{
		ContentID:      contentID,
		SourceType:     "mail",
		Title:          "subject " + contentID,
		NormalizedText: "body of " + contentID,
		Metadata:       map[string]any{"source_ref": sourceRef, "provider": "proton"},
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("insert %s: %v", contentID, err)
	}
}

func activeExists(t *testing.T, db *DB, ctx context.Context, contentID string) bool {
	t.Helper()
	it, err := db.GetKnowledgeItem(ctx, contentID)
	if err != nil {
		t.Fatalf("get %s: %v", contentID, err)
	}
	return it != nil
}

func sourceRefOf(t *testing.T, db *DB, ctx context.Context, contentID string) string {
	t.Helper()
	it, err := db.GetKnowledgeItem(ctx, contentID)
	if err != nil || it == nil {
		t.Fatalf("get %s: %v", contentID, err)
	}
	if it.Metadata == nil {
		return ""
	}
	s, _ := it.Metadata["source_ref"].(string)
	return s
}

func TestBackfillMailSourceRefs(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()

	mk := func(cid, acct, ref string) {
		md := map[string]any{}
		if acct != "" {
			md["account_id"] = acct
		}
		if ref != "" {
			md["source_ref"] = ref
		}
		if err := db.InsertKnowledgeItem(ctx, &KnowledgeItem{
			ContentID: cid, SourceType: "mail", Title: cid, NormalizedText: "body " + cid,
			Metadata: md, VersionID: "v1", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert %s: %v", cid, err)
		}
	}
	mk("email:t1", "acctA", "")                  // → stamped gmail:t1
	mk("email:t2", "acctA", "gmail:preexisting") // already has a ref → untouched
	mk("email:t3", "acctB", "")                  // other account → untouched
	mk("note:n1", "acctA", "")                   // not mail → untouched

	n, err := db.BackfillMailSourceRefs(ctx, "gmail", "acctA")
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 1 {
		t.Fatalf("stamped %d items, want 1", n)
	}
	if got := sourceRefOf(t, db, ctx, "email:t1"); got != "gmail:t1" {
		t.Fatalf("t1 source_ref = %q, want gmail:t1", got)
	}
	if got := sourceRefOf(t, db, ctx, "email:t2"); got != "gmail:preexisting" {
		t.Fatalf("t2 source_ref changed to %q", got)
	}
	if got := sourceRefOf(t, db, ctx, "email:t3"); got != "" {
		t.Fatalf("t3 (other account) wrongly stamped %q", got)
	}
	if got := sourceRefOf(t, db, ctx, "note:n1"); got != "" {
		t.Fatalf("note (non-mail) wrongly stamped %q", got)
	}
	// Idempotent.
	if n2, _ := db.BackfillMailSourceRefs(ctx, "gmail", "acctA"); n2 != 0 {
		t.Fatalf("second backfill stamped %d, want 0", n2)
	}
}

func TestMoveToRecycleAndRestore(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	insertMail(t, db, ctx, "uuid-1", "proton:msg-1@example.com")

	// Move to recycle → gone from active, present in recycle.
	if err := db.MoveToRecycle(ctx, "uuid-1"); err != nil {
		t.Fatalf("MoveToRecycle: %v", err)
	}
	if activeExists(t, db, ctx, "uuid-1") {
		t.Fatal("item must be gone from knowledge_items after recycle")
	}
	entries, err := db.ListRecycleByPrefix(ctx, "proton:")
	if err != nil {
		t.Fatalf("ListRecycleByPrefix: %v", err)
	}
	if len(entries) != 1 || entries[0].ContentID != "uuid-1" || entries[0].SourceRef != "proton:msg-1@example.com" {
		t.Fatalf("recycle entry wrong: %+v", entries)
	}
	if entries[0].NormalizedText != "body of uuid-1" {
		t.Errorf("recycle must preserve text for restore, got %q", entries[0].NormalizedText)
	}

	// Move of an unknown id is a harmless no-op.
	if err := db.MoveToRecycle(ctx, "does-not-exist"); err != nil {
		t.Fatalf("MoveToRecycle(no-op): %v", err)
	}
}

// TestMoveToRecycleCascades proves the structural "invisible everywhere" guarantee:
// recycling an item removes its chunks too (ON DELETE CASCADE), so it vanishes from
// the FTS / vector retrieval surfaces — no per-query "absent" filter to forget.
func TestMoveToRecycleCascades(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	insertMail(t, db, ctx, "uuid-c", "proton:c@example.com")
	if err := db.InsertChunk(ctx, &Chunk{
		ChunkID:   "chunk-c",
		ContentID: "uuid-c",
		ChunkHash: "h",
		Text:      "searchable body",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("InsertChunk: %v", err)
	}

	var before int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks WHERE content_id='uuid-c'`).Scan(&before); err != nil || before != 1 {
		t.Fatalf("chunk should exist before recycle (count=%d, err=%v)", before, err)
	}
	if err := db.MoveToRecycle(ctx, "uuid-c"); err != nil {
		t.Fatalf("MoveToRecycle: %v", err)
	}
	var after int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks WHERE content_id='uuid-c'`).Scan(&after); err != nil {
		t.Fatalf("count chunks after: %v", err)
	}
	if after != 0 {
		t.Errorf("chunks must be cascade-deleted on recycle (retrieval/FTS hidden), got %d", after)
	}
}

func TestReconcileMailRefs(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// Two active mails + one attachment of the first; another source untouched.
	insertMail(t, db, ctx, "uuid-keep", "proton:keep@x.com")
	insertMail(t, db, ctx, "uuid-gone", "proton:gone@x.com")
	insertMail(t, db, ctx, "uuid-gone-att", "proton:gone@x.com:att:invoice.pdf")
	insertMail(t, db, ctx, "uuid-other", "files:/some/path")

	seen := map[string]struct{}{"proton:keep@x.com": {}}

	plan, err := db.ReconcileMailRefs(ctx, "proton:", seen, 3)
	if err != nil {
		t.Fatalf("ReconcileMailRefs: %v", err)
	}
	// gone mail + its attachment recycled; keep + other untouched.
	if plan.Recycled != 2 {
		t.Errorf("Recycled = %d, want 2 (gone mail + its attachment)", plan.Recycled)
	}
	if !activeExists(t, db, ctx, "uuid-keep") {
		t.Error("present mail must be kept")
	}
	if !activeExists(t, db, ctx, "uuid-other") {
		t.Error("a different source (files:) must never be touched by a proton: reconcile")
	}
	if activeExists(t, db, ctx, "uuid-gone") || activeExists(t, db, ctx, "uuid-gone-att") {
		t.Error("absent mail + attachment must be recycled")
	}

	// Grace: still absent on the next pass → miss_count climbs but no purge yet (grace=3).
	if _, err := db.ReconcileMailRefs(ctx, "proton:", seen, 3); err != nil {
		t.Fatalf("ReconcileMailRefs pass 2: %v", err)
	}
	if entries, _ := db.ListRecycleByPrefix(ctx, "proton:"); len(entries) != 2 {
		t.Fatalf("after pass 2 grace, recycle should still hold 2, got %d", len(entries))
	}

	// Third consecutive miss reaches grace → purge.
	plan3, err := db.ReconcileMailRefs(ctx, "proton:", seen, 3)
	if err != nil {
		t.Fatalf("ReconcileMailRefs pass 3: %v", err)
	}
	if plan3.Purged != 2 {
		t.Errorf("Purged = %d, want 2 after reaching grace", plan3.Purged)
	}
	if entries, _ := db.ListRecycleByPrefix(ctx, "proton:"); len(entries) != 0 {
		t.Errorf("recycle should be empty after purge, got %d", len(entries))
	}
}

func TestReconcileRestoreOnReappearance(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	insertMail(t, db, ctx, "uuid-flap", "proton:flap@x.com")

	// Vanishes → recycled.
	if _, err := db.ReconcileMailRefs(ctx, "proton:", map[string]struct{}{}, 3); err != nil {
		t.Fatalf("reconcile (absent): %v", err)
	}
	if activeExists(t, db, ctx, "uuid-flap") {
		t.Fatal("should be recycled")
	}

	// Reappears → returned for restore (caller re-ingests), still in recycle until DeleteRecycle.
	seen := map[string]struct{}{"proton:flap@x.com": {}}
	plan, err := db.ReconcileMailRefs(ctx, "proton:", seen, 3)
	if err != nil {
		t.Fatalf("reconcile (reappeared): %v", err)
	}
	if len(plan.Restore) != 1 || plan.Restore[0].ContentID != "uuid-flap" {
		t.Fatalf("expected 1 restore candidate, got %+v", plan.Restore)
	}
	if plan.Restore[0].SourceRef != "proton:flap@x.com" || plan.Restore[0].NormalizedText != "body of uuid-flap" {
		t.Errorf("restore entry must carry enough to re-ingest: %+v", plan.Restore[0])
	}
}
