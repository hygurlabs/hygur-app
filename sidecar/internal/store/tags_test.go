package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTagsCRUD(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Test CreateTag
	t.Run("CreateTag", func(t *testing.T) {
		tag := &Tag{
			Name:     "TestTag",
			Color:    "#FF5733",
			AutoRule: "folder:Documents",
			IsAuto:   false,
		}

		err := db.CreateTag(ctx, tag)
		if err != nil {
			t.Fatalf("failed to create tag: %v", err)
		}

		if tag.ID == "" {
			t.Error("tag ID should be generated")
		}
		if tag.CreatedAt.IsZero() {
			t.Error("CreatedAt should be set")
		}
		if tag.UpdatedAt.IsZero() {
			t.Error("UpdatedAt should be set")
		}
	})

	// Test GetTag
	t.Run("GetTag", func(t *testing.T) {
		// First create a tag
		tag := &Tag{
			ID:    uuid.New().String(),
			Name:  "GetTagTest",
			Color: "#10B981",
		}
		err := db.CreateTag(ctx, tag)
		if err != nil {
			t.Fatalf("failed to create tag: %v", err)
		}

		// Get the tag
		retrieved, err := db.GetTag(ctx, tag.ID)
		if err != nil {
			t.Fatalf("failed to get tag: %v", err)
		}
		if retrieved == nil {
			t.Fatal("tag should exist")
		}
		if retrieved.Name != "GetTagTest" {
			t.Errorf("expected name 'GetTagTest', got '%s'", retrieved.Name)
		}
		if retrieved.Color != "#10B981" {
			t.Errorf("expected color '#10B981', got '%s'", retrieved.Color)
		}
	})

	// Test GetTag not found
	t.Run("GetTag_NotFound", func(t *testing.T) {
		tag, err := db.GetTag(ctx, "nonexistent-id")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tag != nil {
			t.Error("expected nil for nonexistent tag")
		}
	})

	// Test GetTagByName (case-insensitive)
	t.Run("GetTagByName", func(t *testing.T) {
		tag := &Tag{
			ID:    uuid.New().String(),
			Name:  "CaseSensitiveTest",
			Color: "#3B82F6",
		}
		err := db.CreateTag(ctx, tag)
		if err != nil {
			t.Fatalf("failed to create tag: %v", err)
		}

		// Should find with same case
		retrieved, err := db.GetTagByName(ctx, "CaseSensitiveTest")
		if err != nil {
			t.Fatalf("failed to get tag: %v", err)
		}
		if retrieved == nil {
			t.Fatal("tag should exist")
		}

		// Should find with different case
		retrieved, err = db.GetTagByName(ctx, "casesensitivetest")
		if err != nil {
			t.Fatalf("failed to get tag: %v", err)
		}
		if retrieved == nil {
			t.Fatal("tag should be found case-insensitively")
		}
	})

	// Test UpdateTag
	t.Run("UpdateTag", func(t *testing.T) {
		tag := &Tag{
			ID:    uuid.New().String(),
			Name:  "UpdateTest",
			Color: "#F59E0B",
		}
		err := db.CreateTag(ctx, tag)
		if err != nil {
			t.Fatalf("failed to create tag: %v", err)
		}

		// Update the tag
		tag.Name = "UpdatedName"
		tag.Color = "#EF4444"
		tag.AutoRule = "mail:example.com"
		err = db.UpdateTag(ctx, tag)
		if err != nil {
			t.Fatalf("failed to update tag: %v", err)
		}

		// Verify update
		retrieved, err := db.GetTag(ctx, tag.ID)
		if err != nil {
			t.Fatalf("failed to get tag: %v", err)
		}
		if retrieved.Name != "UpdatedName" {
			t.Errorf("expected name 'UpdatedName', got '%s'", retrieved.Name)
		}
		if retrieved.Color != "#EF4444" {
			t.Errorf("expected color '#EF4444', got '%s'", retrieved.Color)
		}
		if retrieved.AutoRule != "mail:example.com" {
			t.Errorf("expected auto_rule 'mail:example.com', got '%s'", retrieved.AutoRule)
		}
	})

	// Test DeleteTag
	t.Run("DeleteTag", func(t *testing.T) {
		tag := &Tag{
			ID:    uuid.New().String(),
			Name:  "DeleteTest",
			Color: "#8B5CF6",
		}
		err := db.CreateTag(ctx, tag)
		if err != nil {
			t.Fatalf("failed to create tag: %v", err)
		}

		// Delete the tag
		err = db.DeleteTag(ctx, tag.ID)
		if err != nil {
			t.Fatalf("failed to delete tag: %v", err)
		}

		// Verify deletion
		retrieved, err := db.GetTag(ctx, tag.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if retrieved != nil {
			t.Error("tag should be deleted")
		}
	})

	// Test DeleteTag not found
	t.Run("DeleteTag_NotFound", func(t *testing.T) {
		err := db.DeleteTag(ctx, "nonexistent-id")
		if err == nil {
			t.Error("expected error for nonexistent tag")
		}
	})

	// Test ListTags
	t.Run("ListTags", func(t *testing.T) {
		// Create a fresh DB for this test
		db2, err := NewDB(":memory:")
		if err != nil {
			t.Fatalf("failed to create database: %v", err)
		}
		defer db2.Close()

		// Create some tags
		for _, name := range []string{"Alpha", "Beta", "Gamma"} {
			tag := &Tag{
				ID:    uuid.New().String(),
				Name:  name,
				Color: DefaultTagColor(name),
			}
			err := db2.CreateTag(ctx, tag)
			if err != nil {
				t.Fatalf("failed to create tag: %v", err)
			}
		}

		// List all tags
		tags, err := db2.ListTags(ctx)
		if err != nil {
			t.Fatalf("failed to list tags: %v", err)
		}
		if len(tags) != 3 {
			t.Errorf("expected 3 tags, got %d", len(tags))
		}

		// Check sorted by name
		if tags[0].Name != "Alpha" || tags[1].Name != "Beta" || tags[2].Name != "Gamma" {
			t.Error("tags should be sorted by name")
		}
	})
}

func TestItemTags(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Create a knowledge item
	item := &KnowledgeItem{
		ContentID:      uuid.New().String(),
		SourceType:     "test",
		Title:          "Test Item",
		NormalizedText: "Test content",
		VersionID:      "v1",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	err = db.InsertKnowledgeItem(ctx, item)
	if err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	// Create tags
	tag1 := &Tag{ID: uuid.New().String(), Name: "Tag1", Color: "#3B82F6"}
	tag2 := &Tag{ID: uuid.New().String(), Name: "Tag2", Color: "#10B981"}
	err = db.CreateTag(ctx, tag1)
	if err != nil {
		t.Fatalf("failed to create tag1: %v", err)
	}
	err = db.CreateTag(ctx, tag2)
	if err != nil {
		t.Fatalf("failed to create tag2: %v", err)
	}

	// Test AddTagToItem
	t.Run("AddTagToItem", func(t *testing.T) {
		err := db.AddTagToItem(ctx, item.ContentID, tag1.ID)
		if err != nil {
			t.Fatalf("failed to add tag to item: %v", err)
		}

		// Adding same tag again should not error (IGNORE)
		err = db.AddTagToItem(ctx, item.ContentID, tag1.ID)
		if err != nil {
			t.Fatalf("adding duplicate tag should not error: %v", err)
		}
	})

	// Test GetTagsForItem
	t.Run("GetTagsForItem", func(t *testing.T) {
		// Add another tag
		err := db.AddTagToItem(ctx, item.ContentID, tag2.ID)
		if err != nil {
			t.Fatalf("failed to add tag2 to item: %v", err)
		}

		tags, err := db.GetTagsForItem(ctx, item.ContentID)
		if err != nil {
			t.Fatalf("failed to get tags for item: %v", err)
		}
		if len(tags) != 2 {
			t.Errorf("expected 2 tags, got %d", len(tags))
		}
	})

	// Test tag item_count
	t.Run("TagItemCount", func(t *testing.T) {
		tag, err := db.GetTag(ctx, tag1.ID)
		if err != nil {
			t.Fatalf("failed to get tag: %v", err)
		}
		if tag.ItemCount != 1 {
			t.Errorf("expected item_count 1, got %d", tag.ItemCount)
		}
	})

	// Test RemoveTagFromItem
	t.Run("RemoveTagFromItem", func(t *testing.T) {
		err := db.RemoveTagFromItem(ctx, item.ContentID, tag1.ID)
		if err != nil {
			t.Fatalf("failed to remove tag from item: %v", err)
		}

		tags, err := db.GetTagsForItem(ctx, item.ContentID)
		if err != nil {
			t.Fatalf("failed to get tags for item: %v", err)
		}
		if len(tags) != 1 {
			t.Errorf("expected 1 tag after removal, got %d", len(tags))
		}
	})

	// Test RemoveTagFromItem not found
	t.Run("RemoveTagFromItem_NotFound", func(t *testing.T) {
		err := db.RemoveTagFromItem(ctx, item.ContentID, tag1.ID)
		if err == nil {
			t.Error("expected error when removing non-associated tag")
		}
	})
}

func TestGetOrCreateTag(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Test creating a new tag
	t.Run("CreateNew", func(t *testing.T) {
		tag, err := db.GetOrCreateTag(ctx, "NewTag", true, "folder:Test")
		if err != nil {
			t.Fatalf("failed to get or create tag: %v", err)
		}
		if tag.Name != "NewTag" {
			t.Errorf("expected name 'NewTag', got '%s'", tag.Name)
		}
		if !tag.IsAuto {
			t.Error("expected IsAuto to be true")
		}
		if tag.AutoRule != "folder:Test" {
			t.Errorf("expected AutoRule 'folder:Test', got '%s'", tag.AutoRule)
		}
	})

	// Test getting existing tag
	t.Run("GetExisting", func(t *testing.T) {
		// Get with same name
		tag, err := db.GetOrCreateTag(ctx, "NewTag", false, "")
		if err != nil {
			t.Fatalf("failed to get or create tag: %v", err)
		}
		if tag.Name != "NewTag" {
			t.Errorf("expected name 'NewTag', got '%s'", tag.Name)
		}
		// Should keep the original IsAuto value
		if !tag.IsAuto {
			t.Error("expected IsAuto to be true (from original)")
		}
	})

	// Test case-insensitive matching
	t.Run("CaseInsensitive", func(t *testing.T) {
		tag, err := db.GetOrCreateTag(ctx, "newtag", false, "")
		if err != nil {
			t.Fatalf("failed to get or create tag: %v", err)
		}
		if tag.Name != "NewTag" { // Original casing preserved
			t.Errorf("expected name 'NewTag', got '%s'", tag.Name)
		}
	})
}

func TestAutoTagPruning(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Test CountAutoTags
	t.Run("CountAutoTags", func(t *testing.T) {
		// Create some auto and manual tags
		for i := 0; i < 5; i++ {
			tag := &Tag{
				ID:     uuid.New().String(),
				Name:   uuid.New().String(),
				Color:  "#3B82F6",
				IsAuto: true,
			}
			err := db.CreateTag(ctx, tag)
			if err != nil {
				t.Fatalf("failed to create tag: %v", err)
			}
		}
		for i := 0; i < 3; i++ {
			tag := &Tag{
				ID:     uuid.New().String(),
				Name:   uuid.New().String(),
				Color:  "#10B981",
				IsAuto: false,
			}
			err := db.CreateTag(ctx, tag)
			if err != nil {
				t.Fatalf("failed to create tag: %v", err)
			}
		}

		count, err := db.CountAutoTags(ctx)
		if err != nil {
			t.Fatalf("failed to count auto tags: %v", err)
		}
		if count != 5 {
			t.Errorf("expected 5 auto tags, got %d", count)
		}
	})

	// Test GetLeastUsedAutoTags
	t.Run("GetLeastUsedAutoTags", func(t *testing.T) {
		tags, err := db.GetLeastUsedAutoTags(ctx, 3)
		if err != nil {
			t.Fatalf("failed to get least used auto tags: %v", err)
		}
		if len(tags) != 3 {
			t.Errorf("expected 3 tags, got %d", len(tags))
		}
		// All should be auto tags
		for _, tag := range tags {
			if !tag.IsAuto {
				t.Error("expected only auto tags")
			}
		}
	})
}

func TestDefaultTagColor(t *testing.T) {
	// Test that same name gives same color
	color1 := DefaultTagColor("TestTag")
	color2 := DefaultTagColor("TestTag")
	if color1 != color2 {
		t.Error("same name should give same color")
	}

	// Test that different names can give different colors
	// (not guaranteed, but highly likely with different enough names)
	colors := make(map[string]bool)
	for _, name := range []string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon", "Zeta", "Eta", "Theta"} {
		colors[DefaultTagColor(name)] = true
	}
	if len(colors) < 2 {
		t.Error("expected at least 2 different colors for different names")
	}

	// Test that color is from palette
	color := DefaultTagColor("RandomTag")
	found := false
	for _, c := range TagColors {
		if c == color {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("color %s is not from palette", color)
	}
}

func TestNormalizeTagName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"banques", "banques"},
		{"Banques", "banques"},
		{"BANQUES", "banques"},
		{"  Banques  ", "banques"},
		{"Banqués", "banques"},
		{"Banqués ", "banques"},
		{"Café", "cafe"},
		{"École", "ecole"},
		{"  multi   spaces  ", "multi spaces"},
		{"", ""},
		{"   ", ""},
	}
	for _, tc := range cases {
		if got := NormalizeTagName(tc.in); got != tc.want {
			t.Errorf("NormalizeTagName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMergeTags(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Two items, two tag variants — one item carries only the source variant,
	// the other carries both. The merged target should end up on both items
	// with no orphaned source rows.
	itemA := &KnowledgeItem{
		ContentID: uuid.New().String(), SourceType: "test", Title: "A",
		NormalizedText: "a", VersionID: "v1", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	itemB := &KnowledgeItem{
		ContentID: uuid.New().String(), SourceType: "test", Title: "B",
		NormalizedText: "b", VersionID: "v1", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := db.InsertKnowledgeItem(ctx, itemA); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertKnowledgeItem(ctx, itemB); err != nil {
		t.Fatal(err)
	}

	// Schema enforces UNIQUE COLLATE NOCASE on tags.name, so pure case dupes
	// can't coexist — accent variants ("Banques" vs "Banqués") are the
	// real-world dedup target.
	target := &Tag{ID: uuid.New().String(), Name: "Banques", Color: "#3B82F6"}
	source := &Tag{ID: uuid.New().String(), Name: "Banqués", Color: "#10B981"}
	if err := db.CreateTag(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTag(ctx, source); err != nil {
		t.Fatal(err)
	}

	// itemA only has the source; itemB has both — merge must dedupe.
	if err := db.AddTagToItem(ctx, itemA.ContentID, source.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.AddTagToItem(ctx, itemB.ContentID, source.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.AddTagToItem(ctx, itemB.ContentID, target.ID); err != nil {
		t.Fatal(err)
	}

	if err := db.MergeTags(ctx, source.ID, target.ID); err != nil {
		t.Fatalf("MergeTags failed: %v", err)
	}

	// Source tag is gone.
	gone, err := db.GetTag(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gone != nil {
		t.Error("source tag should be deleted after merge")
	}

	// Target now applies to both items.
	merged, err := db.GetTag(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if merged.ItemCount != 2 {
		t.Errorf("expected target item_count 2, got %d", merged.ItemCount)
	}
}

func TestMergeTagsRejectsSelf(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.MergeTags(context.Background(), "x", "x"); err == nil {
		t.Error("merging a tag into itself should error")
	}
}

func TestDedupeTags(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	// Schema's UNIQUE COLLATE NOCASE makes pure case dupes impossible at
	// insert time, so test the realistic case: accent variants that the DB
	// treats as distinct names. NormalizeTagName collapses them.
	heavy := &Tag{ID: uuid.New().String(), Name: "Banques", Color: "#3B82F6"}
	light := &Tag{ID: uuid.New().String(), Name: "Bànques", Color: "#10B981"}
	accented := &Tag{ID: uuid.New().String(), Name: "Banqués", Color: "#F59E0B"}
	for _, tg := range []*Tag{heavy, light, accented} {
		if err := db.CreateTag(ctx, tg); err != nil {
			t.Fatal(err)
		}
	}

	// Give `heavy` two items and the others one each.
	for i := 0; i < 2; i++ {
		item := &KnowledgeItem{
			ContentID: uuid.New().String(), SourceType: "test", Title: "h",
			NormalizedText: "x", VersionID: "v1", CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		if err := db.InsertKnowledgeItem(ctx, item); err != nil {
			t.Fatal(err)
		}
		if err := db.AddTagToItem(ctx, item.ContentID, heavy.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, tg := range []*Tag{light, accented} {
		item := &KnowledgeItem{
			ContentID: uuid.New().String(), SourceType: "test", Title: tg.Name,
			NormalizedText: "x", VersionID: "v1", CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		if err := db.InsertKnowledgeItem(ctx, item); err != nil {
			t.Fatal(err)
		}
		if err := db.AddTagToItem(ctx, item.ContentID, tg.ID); err != nil {
			t.Fatal(err)
		}
	}

	// Add an unrelated tag that must NOT be merged.
	other := &Tag{ID: uuid.New().String(), Name: "Voyages", Color: "#EC4899"}
	if err := db.CreateTag(ctx, other); err != nil {
		t.Fatal(err)
	}

	results, err := db.DedupeTags(ctx)
	if err != nil {
		t.Fatalf("DedupeTags failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 merged group, got %d", len(results))
	}
	if results[0].Canonical != heavy.ID {
		t.Errorf("expected heavy tag (id %s) to win, got %s", heavy.ID, results[0].Canonical)
	}
	if len(results[0].MergedIDs) != 2 {
		t.Errorf("expected 2 merged ids, got %d", len(results[0].MergedIDs))
	}

	// Survivor now owns all 4 items.
	survivor, err := db.GetTag(ctx, heavy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if survivor.ItemCount != 4 {
		t.Errorf("expected survivor item_count 4, got %d", survivor.ItemCount)
	}

	// Losers gone.
	for _, id := range []string{light.ID, accented.ID} {
		gone, err := db.GetTag(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if gone != nil {
			t.Errorf("tag %s should have been merged away", id)
		}
	}

	// Unrelated tag untouched.
	stillThere, err := db.GetTag(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillThere == nil {
		t.Error("unrelated tag should still exist")
	}
}

func TestGetOrCreateTagAccentInsensitive(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	first, err := db.GetOrCreateTag(ctx, "Banques", false, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.GetOrCreateTag(ctx, "banqués", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Errorf("expected accent variants to resolve to same tag: %s != %s", first.ID, second.ID)
	}
}

func TestItemTagsCascadeDelete(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Create item and tag
	item := &KnowledgeItem{
		ContentID:      uuid.New().String(),
		SourceType:     "test",
		Title:          "Test Item",
		NormalizedText: "Test content",
		VersionID:      "v1",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	err = db.InsertKnowledgeItem(ctx, item)
	if err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	tag := &Tag{ID: uuid.New().String(), Name: "CascadeTest", Color: "#3B82F6"}
	err = db.CreateTag(ctx, tag)
	if err != nil {
		t.Fatalf("failed to create tag: %v", err)
	}

	// Associate tag with item
	err = db.AddTagToItem(ctx, item.ContentID, tag.ID)
	if err != nil {
		t.Fatalf("failed to add tag to item: %v", err)
	}

	// Test: Delete tag should cascade and remove association
	t.Run("DeleteTagCascade", func(t *testing.T) {
		err := db.DeleteTag(ctx, tag.ID)
		if err != nil {
			t.Fatalf("failed to delete tag: %v", err)
		}

		// item_tags entry should be removed
		tags, err := db.GetTagsForItem(ctx, item.ContentID)
		if err != nil {
			t.Fatalf("failed to get tags: %v", err)
		}
		if len(tags) != 0 {
			t.Error("expected no tags after cascade delete")
		}
	})

	// Test: Delete item should cascade and remove association
	t.Run("DeleteItemCascade", func(t *testing.T) {
		// Create new tag and associate
		tag2 := &Tag{ID: uuid.New().String(), Name: "CascadeTest2", Color: "#10B981"}
		err = db.CreateTag(ctx, tag2)
		if err != nil {
			t.Fatalf("failed to create tag: %v", err)
		}

		err = db.AddTagToItem(ctx, item.ContentID, tag2.ID)
		if err != nil {
			t.Fatalf("failed to add tag to item: %v", err)
		}

		// Delete the item
		err = db.DeleteKnowledgeItem(ctx, item.ContentID)
		if err != nil {
			t.Fatalf("failed to delete item: %v", err)
		}

		// Tag should still exist, but item_count should be 0
		tag2Retrieved, err := db.GetTag(ctx, tag2.ID)
		if err != nil {
			t.Fatalf("failed to get tag: %v", err)
		}
		if tag2Retrieved == nil {
			t.Fatal("tag should still exist")
		}
		if tag2Retrieved.ItemCount != 0 {
			t.Errorf("expected item_count 0, got %d", tag2Retrieved.ItemCount)
		}
	})
}
