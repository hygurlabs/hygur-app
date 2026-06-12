package contradict

import (
	"testing"

	"github.com/hygur/sidecar/internal/store"
)

func recItem(id, title, dateRFC string) *store.KnowledgeItem {
	return &store.KnowledgeItem{ContentID: id, Title: title, Metadata: map[string]any{"canonical_date": dateRFC}}
}

func TestDetectRecurrence(t *testing.T) {
	t.Run("quarterly cluster → recurrence ~90d + next prediction", func(t *testing.T) {
		items := []*store.KnowledgeItem{
			recItem("m1", "Déclaration TVA", "2025-01-10T00:00:00Z"),
			recItem("m2", "Déclaration TVA", "2025-04-12T00:00:00Z"), // +92
			recItem("m3", "Déclaration TVA", "2025-07-10T00:00:00Z"), // +89
			recItem("m4", "Déclaration TVA", "2025-10-11T00:00:00Z"), // +93
		}
		got := DetectRecurrence(items, 3)
		if len(got) != 1 {
			t.Fatalf("want 1 recurrence, got %d", len(got))
		}
		r := got[0]
		if r.Count != 4 {
			t.Errorf("count = %d, want 4", r.Count)
		}
		if r.PeriodDays < 85 || r.PeriodDays > 95 {
			t.Errorf("period = %d, want ~90", r.PeriodDays)
		}
		if r.NextAt == "" || r.NextAt <= r.LastAt {
			t.Errorf("nextAt %q must be after lastAt %q", r.NextAt, r.LastAt)
		}
	})

	t.Run("irregular gaps → not a recurrence", func(t *testing.T) {
		items := []*store.KnowledgeItem{
			recItem("a", "Random thread", "2025-01-01T00:00:00Z"),
			recItem("b", "Random thread", "2025-02-01T00:00:00Z"), // +31
			recItem("c", "Random thread", "2025-09-01T00:00:00Z"), // +212 (irregular)
		}
		if got := DetectRecurrence(items, 3); len(got) != 0 {
			t.Errorf("irregular gaps must not detect, got %d", len(got))
		}
	})

	t.Run("fewer than minCount → nothing", func(t *testing.T) {
		items := []*store.KnowledgeItem{
			recItem("x", "Thing", "2025-01-01T00:00:00Z"),
			recItem("y", "Thing", "2025-04-01T00:00:00Z"),
		}
		if got := DetectRecurrence(items, 3); len(got) != 0 {
			t.Errorf("2 < minCount 3 must not detect, got %d", len(got))
		}
	})

	t.Run("same-day duplicates don't fake a 0-gap", func(t *testing.T) {
		items := []*store.KnowledgeItem{
			recItem("d1a", "Monthly report", "2025-01-05T08:00:00Z"),
			recItem("d1b", "Monthly report", "2025-01-05T18:00:00Z"), // same day → collapsed
			recItem("d2", "Monthly report", "2025-02-05T09:00:00Z"),
			recItem("d3", "Monthly report", "2025-03-05T09:00:00Z"),
		}
		got := DetectRecurrence(items, 3)
		if len(got) != 1 || got[0].Count != 3 {
			t.Errorf("want 1 recurrence with count=3 (same-day collapsed), got %+v", got)
		}
	})
}
