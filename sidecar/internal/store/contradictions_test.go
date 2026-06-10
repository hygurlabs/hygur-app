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
