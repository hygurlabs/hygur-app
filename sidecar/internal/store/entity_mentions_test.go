package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func mkItem(t *testing.T, db *DB, id string) {
	t.Helper()
	now := time.Now()
	if err := db.InsertKnowledgeItem(context.Background(), &KnowledgeItem{
		ContentID: id, SourceType: SourceTypeNote, Title: id, VersionID: id + "-v1",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func TestEntityMentions_ReplaceAndLookup(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "em.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	mkItem(t, db, "item-a")
	mkItem(t, db, "item-b")

	if err := db.ReplaceEntityMentions(ctx, "item-a", []EntityMention{
		{EntityNorm: "acme", EntityRaw: "Acme SARL", Attribute: "vendor", AssertedAt: "2026-06-01T00:00:00Z"},
		{EntityNorm: "acme", EntityRaw: "ACME", Attribute: "vendor", AssertedAt: "2026-06-01T00:00:00Z"}, // dup (norm,attr) collapses
		{EntityNorm: "dupont", EntityRaw: "Dupont", Attribute: "person", AssertedAt: "2026-06-02T00:00:00Z"},
		{EntityNorm: "", EntityRaw: "skip"}, // empty norm skipped
	}); err != nil {
		t.Fatalf("replace a: %v", err)
	}
	if err := db.ReplaceEntityMentions(ctx, "item-b", []EntityMention{
		{EntityNorm: "acme", EntityRaw: "Acme", Attribute: "vendor", AssertedAt: "2026-06-03T00:00:00Z"},
	}); err != nil {
		t.Fatalf("replace b: %v", err)
	}

	// "acme" → both items, most-recently-asserted first (item-b 06-03 > item-a 06-01).
	cids, err := db.EntityMentionContentIDs(ctx, []string{"acme"}, 10)
	if err != nil {
		t.Fatalf("lookup acme: %v", err)
	}
	if len(cids) != 2 {
		t.Fatalf("acme → %v, want 2 items", cids)
	}
	if cids[0] != "item-b" {
		t.Errorf("acme top = %q, want item-b (most recent)", cids[0])
	}

	// "dupont" → only item-a.
	if cids, _ := db.EntityMentionContentIDs(ctx, []string{"dupont"}, 10); len(cids) != 1 || cids[0] != "item-a" {
		t.Errorf("dupont → %v, want [item-a]", cids)
	}

	// Replace item-a with empty clears its rows: dupont gone, acme keeps item-b.
	if err := db.ReplaceEntityMentions(ctx, "item-a", nil); err != nil {
		t.Fatalf("clear a: %v", err)
	}
	if cids, _ := db.EntityMentionContentIDs(ctx, []string{"dupont"}, 10); len(cids) != 0 {
		t.Errorf("dupont after clear → %v, want empty", cids)
	}
	if cids, _ := db.EntityMentionContentIDs(ctx, []string{"acme"}, 10); len(cids) != 1 || cids[0] != "item-b" {
		t.Errorf("acme after clearing a → %v, want [item-b]", cids)
	}

	// Empty norms → nil (no lookup).
	if cids, _ := db.EntityMentionContentIDs(ctx, nil, 10); cids != nil {
		t.Errorf("nil norms → %v, want nil", cids)
	}
}

// TestEntityMentions_CascadeOnItemDelete — deleting a knowledge item must purge
// its entity_mentions (the recycle-bin "structural invisibility" invariant).
func TestEntityMentions_CascadeOnItemDelete(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "em.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	mkItem(t, db, "doomed")
	if err := db.ReplaceEntityMentions(ctx, "doomed", []EntityMention{
		{EntityNorm: "acme", Attribute: "vendor"},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if cids, _ := db.EntityMentionContentIDs(ctx, []string{"acme"}, 10); len(cids) != 1 {
		t.Fatalf("precondition: acme → %v, want [doomed]", cids)
	}
	if err := db.DeleteKnowledgeItem(ctx, "doomed"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if cids, _ := db.EntityMentionContentIDs(ctx, []string{"acme"}, 10); len(cids) != 0 {
		t.Errorf("after delete acme → %v, want empty (cascade)", cids)
	}
}
