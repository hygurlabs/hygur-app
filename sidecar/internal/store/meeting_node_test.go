package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestMeetingNodes_RoundTrip proves meeting-time nodes persist with their source + assertion edges,
// read back by entity across sources, that a re-run is idempotent (replace), and that unrelated
// entities are excluded. Fictional entity.
func TestMeetingNodes_RoundTrip(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "meet.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	mkItem(t, db, "cal-1")
	mkItem(t, db, "eml-1")

	when14 := time.Date(2026, 7, 10, 14, 0, 0, 0, time.UTC)
	when15 := time.Date(2026, 7, 10, 15, 0, 0, 0, time.UTC)
	if err := db.ReplaceMeetingNodes(ctx, "cal-1", []MeetingNode{
		{ContentID: "cal-1", EntityNorm: "acme", When: when14, Source: "calendar", AssertedAt: time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC), Title: "Acme sync"},
	}); err != nil {
		t.Fatalf("replace cal-1: %v", err)
	}
	if err := db.ReplaceMeetingNodes(ctx, "eml-1", []MeetingNode{
		{ContentID: "eml-1", EntityNorm: "acme", When: when15, Source: "email", AssertedAt: time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC), Title: "Re: moved to 3pm"},
	}); err != nil {
		t.Fatalf("replace eml-1: %v", err)
	}

	got, err := db.MeetingNodesForEntities(ctx, []string{"acme"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 meeting nodes for acme (email + calendar), got %d", len(got))
	}
	bySource := map[string]MeetingNode{}
	for _, n := range got {
		bySource[n.Source] = n
	}
	if !bySource["email"].When.Equal(when15) || !bySource["calendar"].When.Equal(when14) {
		t.Fatalf("times not preserved across sources: %+v", bySource)
	}

	// A different entity returns nothing.
	other, _ := db.MeetingNodesForEntities(ctx, []string{"nobody"})
	if len(other) != 0 {
		t.Errorf("expected no nodes for a different entity, got %d", len(other))
	}
}
