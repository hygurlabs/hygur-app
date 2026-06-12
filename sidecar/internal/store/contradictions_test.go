package store

import (
	"context"
	"testing"
)

func TestDismissedContradictions(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("new db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if got, _ := db.DismissedContradictions(ctx); len(got) != 0 {
		t.Fatalf("want empty set, got %v", got)
	}

	// Dismiss is idempotent.
	for _, k := range []string{"k1", "k1", "k2"} {
		if err := db.DismissContradiction(ctx, k); err != nil {
			t.Fatalf("dismiss %s: %v", k, err)
		}
	}
	got, err := db.DismissedContradictions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got["k1"] || !got["k2"] {
		t.Fatalf("want {k1,k2}, got %v", got)
	}

	// Undo restores just that one.
	if err := db.UndismissContradiction(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	got, _ = db.DismissedContradictions(ctx)
	if len(got) != 1 || got["k1"] || !got["k2"] {
		t.Fatalf("want {k2}, got %v", got)
	}
}

func TestReconcileVerdictCache(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if m, err := db.GetReconcileVerdicts(ctx); err != nil || len(m) != 0 {
		t.Fatalf("empty cache expected, got %d (err %v)", len(m), err)
	}
	// Store a real conflict and a 'none' (so the none cluster is never re-judged).
	if err := db.PutReconcileVerdict(ctx, "k-conflict", "conflict", "two due dates"); err != nil {
		t.Fatalf("put conflict: %v", err)
	}
	if err := db.PutReconcileVerdict(ctx, "k-none", "none", ""); err != nil {
		t.Fatalf("put none: %v", err)
	}
	m, err := db.GetReconcileVerdicts(ctx)
	if err != nil || len(m) != 2 {
		t.Fatalf("want 2 cached, got %d (err %v)", len(m), err)
	}
	if m["k-conflict"].Kind != "conflict" || m["k-conflict"].Reason != "two due dates" {
		t.Errorf("conflict verdict wrong: %+v", m["k-conflict"])
	}
	if m["k-none"].Kind != "none" {
		t.Errorf("none verdict must be cached (else re-judged forever): %+v", m["k-none"])
	}
	// Re-judge of the same Key overwrites.
	_ = db.PutReconcileVerdict(ctx, "k-conflict", "supersedes", "evolved")
	m, _ = db.GetReconcileVerdicts(ctx)
	if m["k-conflict"].Kind != "supersedes" {
		t.Errorf("overwrite failed: %+v", m["k-conflict"])
	}
}
