package store

import (
	"context"
	"testing"
)

func TestBumpItemAccessAndStats(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// First citation of a turn: a + b, with a cited twice (deduped → counts once).
	if err := db.BumpItemAccess(ctx, []string{"mail:a", "mail:a", "note:b", ""}); err != nil {
		t.Fatalf("bump 1: %v", err)
	}
	// Second turn cites a again.
	if err := db.BumpItemAccess(ctx, []string{"mail:a"}); err != nil {
		t.Fatalf("bump 2: %v", err)
	}

	var hitsA, hitsB int
	if err := db.db.QueryRowContext(ctx, `SELECT hit_count FROM item_access WHERE content_id='mail:a'`).Scan(&hitsA); err != nil {
		t.Fatalf("read a: %v", err)
	}
	if err := db.db.QueryRowContext(ctx, `SELECT hit_count FROM item_access WHERE content_id='note:b'`).Scan(&hitsB); err != nil {
		t.Fatalf("read b: %v", err)
	}
	if hitsA != 2 {
		t.Errorf("mail:a hit_count = %d, want 2 (deduped per turn, two turns)", hitsA)
	}
	if hitsB != 1 {
		t.Errorf("note:b hit_count = %d, want 1", hitsB)
	}
	// last_accessed_at must be stamped.
	var last string
	if err := db.db.QueryRowContext(ctx, `SELECT last_accessed_at FROM item_access WHERE content_id='mail:a'`).Scan(&last); err != nil || last == "" {
		t.Errorf("last_accessed_at not stamped: %q (err %v)", last, err)
	}
	// Empty input is a no-op (no error).
	if err := db.BumpItemAccess(ctx, nil); err != nil {
		t.Errorf("empty bump should be a no-op: %v", err)
	}

	// DB size is positive.
	if sz, err := db.DBSizeBytes(ctx); err != nil || sz <= 0 {
		t.Errorf("DBSizeBytes = %d (err %v)", sz, err)
	}
}
