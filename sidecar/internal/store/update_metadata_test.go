package store

import (
	"context"
	"testing"
	"time"
)

// UpdateKnowledgeItemMetadata must rewrite metadata WITHOUT bumping updated_at — a
// full-corpus Tier-2 backfill relies on this so it can't make every item read as
// "recently modified" (which would flood updated_at-based recency queries).
func TestUpdateKnowledgeItemMetadataPreservesUpdatedAt(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	old := time.Date(2025, 1, 1, 9, 0, 0, 0, time.UTC)

	if err := db.InsertKnowledgeItem(ctx, &KnowledgeItem{
		ContentID: "k1", SourceType: SourceTypeNote, Title: "t", NormalizedText: "x",
		VersionID: "v1", CreatedAt: old, UpdatedAt: old,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	before, _ := db.GetKnowledgeItem(ctx, "k1")

	if err := db.UpdateKnowledgeItemMetadata(ctx, "k1", map[string]any{"extracted_orgs": []any{"Acme"}}); err != nil {
		t.Fatalf("update metadata: %v", err)
	}
	after, err := db.GetKnowledgeItem(ctx, "k1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("updated_at changed from %v to %v — must be preserved", before.UpdatedAt, after.UpdatedAt)
	}
	if orgs, _ := after.Metadata["extracted_orgs"].([]any); len(orgs) != 1 {
		t.Errorf("metadata not written: %+v", after.Metadata)
	}

	// Contrast: the full update DOES bump updated_at.
	after.Title = "t2"
	if err := db.UpdateKnowledgeItem(ctx, after); err != nil {
		t.Fatalf("full update: %v", err)
	}
	bumped, _ := db.GetKnowledgeItem(ctx, "k1")
	if !bumped.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("full UpdateKnowledgeItem should bump updated_at, got %v", bumped.UpdatedAt)
	}
}
