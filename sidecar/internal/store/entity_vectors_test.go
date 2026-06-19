package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestEntityVectors_UpsertAndSimilar(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	const model = "test-emb"

	// meeting ≈ reunion (near-parallel); banana orthogonal.
	if err := db.UpsertEntityVector(ctx, "meeting", []float32{1, 0, 0}, model); err != nil {
		t.Fatalf("upsert meeting: %v", err)
	}
	if err := db.UpsertEntityVector(ctx, "reunion", []float32{0.9, 0.1, 0}, model); err != nil {
		t.Fatalf("upsert reunion: %v", err)
	}
	if err := db.UpsertEntityVector(ctx, "banana", []float32{0, 0, 1}, model); err != nil {
		t.Fatalf("upsert banana: %v", err)
	}
	// Upsert is idempotent (replace, not duplicate).
	if err := db.UpsertEntityVector(ctx, "meeting", []float32{1, 0, 0}, model); err != nil {
		t.Fatalf("re-upsert meeting: %v", err)
	}

	norms, err := db.SimilarEntityNorms(ctx, []float32{1, 0, 0}, model, 0.8, 10)
	if err != nil {
		t.Fatalf("similar: %v", err)
	}
	if len(norms) != 2 {
		t.Fatalf("similar → %v, want [meeting reunion] (banana below threshold)", norms)
	}
	if norms[0] != "meeting" || norms[1] != "reunion" {
		t.Errorf("similar order = %v, want [meeting reunion] (cosine desc)", norms)
	}

	// A different model shares no vector space → no matches.
	if other, _ := db.SimilarEntityNorms(ctx, []float32{1, 0, 0}, "other-model", 0.8, 10); len(other) != 0 {
		t.Errorf("cross-model similar → %v, want none", other)
	}

	// Empty query vector → nil.
	if none, _ := db.SimilarEntityNorms(ctx, nil, model, 0.8, 10); none != nil {
		t.Errorf("nil query vec → %v, want nil", none)
	}
}

func TestEntityVectors_NeedingVector(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "ev.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	mkItem(t, db, "item-a")
	if err := db.ReplaceEntityMentions(ctx, "item-a", []EntityMention{
		{EntityNorm: "acme", Attribute: "x"},
		{EntityNorm: "globex", Attribute: "y"},
	}); err != nil {
		t.Fatalf("mentions: %v", err)
	}
	const model = "m1"

	if need, _ := db.EntityNormsNeedingVector(ctx, model, 100); len(need) != 2 {
		t.Fatalf("needing (none embedded) → %v, want 2", need)
	}

	if err := db.UpsertEntityVector(ctx, "acme", []float32{1, 0}, model); err != nil {
		t.Fatalf("embed acme: %v", err)
	}
	need, _ := db.EntityNormsNeedingVector(ctx, model, 100)
	if len(need) != 1 || need[0] != "globex" {
		t.Errorf("needing after embedding acme → %v, want [globex]", need)
	}

	// A model change invalidates the existing vector for comparison purposes.
	if need, _ := db.EntityNormsNeedingVector(ctx, "m2", 100); len(need) != 2 {
		t.Errorf("needing under new model → %v, want 2", need)
	}
}
