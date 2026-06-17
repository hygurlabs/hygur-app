package contradict

import (
	"testing"

	"github.com/hygur/sidecar/internal/store"
)

// dec builds a decision knowledge_item carrying one cached claim.
func dec(id, entity, attribute, value string) *store.KnowledgeItem {
	return &store.KnowledgeItem{
		ContentID: id,
		Metadata: map[string]any{"extracted_claims": []Claim{
			{Entity: entity, Attribute: attribute, Value: value},
		}},
	}
}

func TestDetectDecisionEvolution(t *testing.T) {
	t.Run("divergent value, later updates earlier", func(t *testing.T) {
		items := []*store.KnowledgeItem{
			dec("decision:a", "accountant", "firm", "Alpha"),
			dec("decision:b", "accountant", "firm", "Beta"),
		}
		at := map[string]string{"decision:a": "2026-01-01T00:00:00Z", "decision:b": "2026-03-01T00:00:00Z"}
		got := DetectDecisionEvolution(items, at)
		if len(got) != 1 {
			t.Fatalf("want 1 evolution, got %d: %+v", len(got), got)
		}
		e := got[0]
		if e.PredecessorID != "decision:a" || e.SuccessorID != "decision:b" {
			t.Errorf("want a→b, got %s→%s", e.PredecessorID, e.SuccessorID)
		}
		if e.OldValue != "Alpha" || e.NewValue != "Beta" {
			t.Errorf("want Alpha→Beta, got %s→%s", e.OldValue, e.NewValue)
		}
	})

	t.Run("date order, not insertion order, decides successor", func(t *testing.T) {
		// b inserted first but decided later → still the successor.
		items := []*store.KnowledgeItem{
			dec("decision:b", "accountant", "firm", "Beta"),
			dec("decision:a", "accountant", "firm", "Alpha"),
		}
		at := map[string]string{"decision:a": "2026-01-01T00:00:00Z", "decision:b": "2026-03-01T00:00:00Z"}
		got := DetectDecisionEvolution(items, at)
		if len(got) != 1 || got[0].SuccessorID != "decision:b" {
			t.Fatalf("want successor b, got %+v", got)
		}
	})

	t.Run("same value reaffirmed → no evolution", func(t *testing.T) {
		items := []*store.KnowledgeItem{
			dec("decision:a", "accountant", "firm", "Alpha"),
			dec("decision:b", "accountant", "firm", "alpha"), // case/space-insensitive
		}
		at := map[string]string{"decision:a": "2026-01-01T00:00:00Z", "decision:b": "2026-03-01T00:00:00Z"}
		if got := DetectDecisionEvolution(items, at); len(got) != 0 {
			t.Errorf("reaffirmation must not be an evolution, got %+v", got)
		}
	})

	t.Run("different attribute → no evolution", func(t *testing.T) {
		items := []*store.KnowledgeItem{
			dec("decision:a", "accountant", "firm", "Alpha"),
			dec("decision:b", "accountant", "fee", "Beta"),
		}
		at := map[string]string{"decision:a": "2026-01-01T00:00:00Z", "decision:b": "2026-03-01T00:00:00Z"}
		if got := DetectDecisionEvolution(items, at); len(got) != 0 {
			t.Errorf("distinct attributes must not pair, got %+v", got)
		}
	})

	t.Run("undated decision is skipped", func(t *testing.T) {
		items := []*store.KnowledgeItem{
			dec("decision:a", "accountant", "firm", "Alpha"),
			dec("decision:b", "accountant", "firm", "Beta"),
		}
		at := map[string]string{"decision:a": "2026-01-01T00:00:00Z"} // b undated
		if got := DetectDecisionEvolution(items, at); len(got) != 0 {
			t.Errorf("an undated side cannot be ordered → no evolution, got %+v", got)
		}
	})

	t.Run("three decisions → two transitions", func(t *testing.T) {
		items := []*store.KnowledgeItem{
			dec("decision:a", "bank", "primary", "X"),
			dec("decision:b", "bank", "primary", "Y"),
			dec("decision:c", "bank", "primary", "Z"),
		}
		at := map[string]string{
			"decision:a": "2026-01-01T00:00:00Z",
			"decision:b": "2026-02-01T00:00:00Z",
			"decision:c": "2026-03-01T00:00:00Z",
		}
		got := DetectDecisionEvolution(items, at)
		if len(got) != 2 {
			t.Fatalf("want 2 transitions, got %d: %+v", len(got), got)
		}
		if got[0].PredecessorID != "decision:a" || got[0].SuccessorID != "decision:b" ||
			got[1].PredecessorID != "decision:b" || got[1].SuccessorID != "decision:c" {
			t.Errorf("want a→b then b→c, got %+v", got)
		}
	})
}
