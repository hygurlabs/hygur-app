package store

import (
	"context"
	"testing"
	"time"
)

func TestChronicleChapterAndActs(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if c, _ := db.GetChronicleChapter(ctx, "life"); c != nil {
		t.Fatalf("want nil for missing chapter, got %+v", c)
	}

	// Create + read (status defaults to open).
	if err := db.UpsertChronicleChapter(ctx, &ChronicleChapter{
		ID: "life", Title: "Life", Synopsis: "s0", Watermark: "2026-06-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	c, _ := db.GetChronicleChapter(ctx, "life")
	if c == nil || c.Title != "Life" || c.Synopsis != "s0" || c.Status != "open" {
		t.Fatalf("get: %+v", c)
	}

	// Update in place.
	c.Synopsis, c.Watermark = "s1", "2026-06-02T00:00:00Z"
	if err := db.UpsertChronicleChapter(ctx, c); err != nil {
		t.Fatal(err)
	}
	if c2, _ := db.GetChronicleChapter(ctx, "life"); c2 == nil || c2.Synopsis != "s1" || c2.Watermark != "2026-06-02T00:00:00Z" {
		t.Fatalf("update: %+v", c2)
	}
	if chs, _ := db.ListChronicleChapters(ctx); len(chs) != 1 {
		t.Fatalf("list chapters = %d", len(chs))
	}

	// Acts: two for "life" + one for another chapter (must be excluded), in date order.
	now := time.Now()
	mk := func(id string) {
		if err := db.InsertKnowledgeItem(ctx, &KnowledgeItem{
			ContentID: id, SourceType: SourceTypeChronicleAct, Title: id, NormalizedText: "act " + id,
			Metadata: map[string]any{}, VersionID: "v1", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	mk("chronicle:life:2026-06-03")
	mk("chronicle:life:2026-06-01")
	mk("chronicle:proj:x:2026-06-02") // different chapter — must not leak in

	acts, err := db.ListChronicleActs(ctx, "life")
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 2 {
		t.Fatalf("want 2 life acts, got %d", len(acts))
	}
	if acts[0].ContentID != "chronicle:life:2026-06-01" || acts[1].ContentID != "chronicle:life:2026-06-03" {
		t.Fatalf("acts not in date order: %s, %s", acts[0].ContentID, acts[1].ContentID)
	}
}
