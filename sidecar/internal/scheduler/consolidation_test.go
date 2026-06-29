package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/hygur/sidecar/internal/store"
)

func TestConsolidationShadowPass(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()

	mk := func(id, title string) *store.KnowledgeItem {
		return &store.KnowledgeItem{
			ContentID: id, SourceType: "note", Title: title,
			NormalizedText: "body of " + title, VersionID: "v1",
			CreatedAt: now, UpdatedAt: now,
		}
	}
	if err := db.InsertKnowledgeItem(ctx, mk("note:a", "A")); err != nil {
		t.Fatalf("insert A: %v", err)
	}
	if err := db.InsertKnowledgeItem(ctx, mk("note:b", "B")); err != nil {
		t.Fatalf("insert B: %v", err)
	}

	// A is cited across two turns (earns an access signal); B never.
	if err := db.BumpItemAccess(ctx, []string{"note:a"}); err != nil {
		t.Fatalf("bump 1: %v", err)
	}
	if err := db.BumpItemAccess(ctx, []string{"note:a"}); err != nil {
		t.Fatalf("bump 2: %v", err)
	}

	c := NewConsolidator(db, zerolog.Nop())
	if err := c.RunOnce(ctx, now); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	sigs, err := db.ItemSignalsByIDs(ctx, []string{"note:a", "note:b"})
	if err != nil {
		t.Fatalf("read signals: %v", err)
	}
	a, okA := sigs["note:a"]
	b, okB := sigs["note:b"]
	if !okA || !okB {
		t.Fatalf("both items must be scored: a=%v b=%v", okA, okB)
	}
	if a.Salience <= b.Salience {
		t.Errorf("accessed item should outscore unaccessed: a=%.4f b=%.4f", a.Salience, b.Salience)
	}
	if a.Strength <= 0 || a.Strength > 1 {
		t.Errorf("strength out of (0,1]: %.6f", a.Strength)
	}
	// Shadow under budget with no vectors → everyone stays hot, nothing evicted.
	if a.Tier != "hot" || b.Tier != "hot" {
		t.Errorf("shadow pass must not cold-tier under budget: a=%q b=%q", a.Tier, b.Tier)
	}

	// Idempotent: a second pass overwrites cleanly.
	if err := c.RunOnce(ctx, now); err != nil {
		t.Fatalf("RunOnce (2nd): %v", err)
	}
}
