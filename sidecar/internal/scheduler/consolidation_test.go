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
	res, err := c.RunOnce(ctx, now)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if res == nil || res.Scored != 2 {
		t.Fatalf("expected 2 items scored, got %+v", res)
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
	if _, err := c.RunOnce(ctx, now); err != nil {
		t.Fatalf("RunOnce (2nd): %v", err)
	}
}

func TestConsolidationSurpriseRaisesSalience(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()

	if err := db.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
		ContentID: "note:s", SourceType: "note", Title: "S",
		NormalizedText: "body", VersionID: "v1", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	c := NewConsolidator(db, zerolog.Nop())

	// Baseline (no surprise stamped).
	if _, err := c.RunOnce(ctx, now); err != nil {
		t.Fatalf("RunOnce baseline: %v", err)
	}
	base, _ := db.ItemSignalsByIDs(ctx, []string{"note:s"})

	// Stamp maximum surprise, then re-score.
	if err := db.UpsertItemSurprise(ctx, "note:s", 1.0); err != nil {
		t.Fatalf("UpsertItemSurprise: %v", err)
	}
	if _, err := c.RunOnce(ctx, now); err != nil {
		t.Fatalf("RunOnce surprised: %v", err)
	}
	after, _ := db.ItemSignalsByIDs(ctx, []string{"note:s"})

	// surprise carries weight 0.15 in ComputeSalience (addendum §1.2).
	if d := after["note:s"].Salience - base["note:s"].Salience; d < 0.10 || d > 0.20 {
		t.Errorf("surprise should raise salience by ~0.15, got Δ=%.4f (base=%.4f after=%.4f)",
			d, base["note:s"].Salience, after["note:s"].Salience)
	}
}
