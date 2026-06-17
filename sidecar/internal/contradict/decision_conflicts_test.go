package contradict

import (
	"testing"

	"github.com/hygur/sidecar/internal/store"
)

func dcItem(id string, claims ...Claim) *store.KnowledgeItem {
	return &store.KnowledgeItem{ContentID: id, Metadata: map[string]any{"extracted_claims": claims}}
}

func dcClaim(entity, attr, value, assertedAt string) Claim {
	return Claim{Entity: entity, Attribute: attr, Value: value, Quote: value, AssertedAt: assertedAt}
}

func TestDetectDecisionConflicts(t *testing.T) {
	decisions := []*store.KnowledgeItem{
		dcItem("decision:1", dcClaim("Atlas", "design tool", "Figma", "")),
	}
	decidedAt := map[string]string{"decision:1": "2025-01-01T00:00:00Z"}

	t.Run("fresh divergent capture → conflict", func(t *testing.T) {
		items := []*store.KnowledgeItem{
			dcItem("mail:x", dcClaim("Atlas", "design tool", "Adobe XD", "2025-06-01T00:00:00Z")),
		}
		got := DetectDecisionConflicts(decisions, decidedAt, items, "")
		if len(got) != 1 {
			t.Fatalf("want 1 conflict, got %d", len(got))
		}
		if len(got[0].Members) != 2 || got[0].Members[0].SourceID != "decision:1" {
			t.Errorf("want [decision:1, mail:x], got %+v", got[0].Members)
		}
	})

	t.Run("capture OLDER than the decision → no conflict", func(t *testing.T) {
		items := []*store.KnowledgeItem{
			dcItem("mail:old", dcClaim("Atlas", "design tool", "Sketch", "2024-06-01T00:00:00Z")),
		}
		if got := DetectDecisionConflicts(decisions, decidedAt, items, ""); len(got) != 0 {
			t.Errorf("a pre-decision capture is not a contradiction, got %d", len(got))
		}
	})

	t.Run("same value → no conflict", func(t *testing.T) {
		items := []*store.KnowledgeItem{
			dcItem("mail:agree", dcClaim("Atlas", "design tool", "Figma", "2025-06-01T00:00:00Z")),
		}
		if got := DetectDecisionConflicts(decisions, decidedAt, items, ""); len(got) != 0 {
			t.Errorf("agreeing capture is not a contradiction, got %d", len(got))
		}
	})

	t.Run("different entity/attribute → no conflict", func(t *testing.T) {
		items := []*store.KnowledgeItem{
			dcItem("mail:other", dcClaim("Borealis", "vendor", "Acme", "2025-06-01T00:00:00Z")),
		}
		if got := DetectDecisionConflicts(decisions, decidedAt, items, ""); len(got) != 0 {
			t.Errorf("unrelated entity is not a contradiction, got %d", len(got))
		}
	})

	t.Run("stale capture (before since) → excluded", func(t *testing.T) {
		items := []*store.KnowledgeItem{
			dcItem("mail:stale", dcClaim("Atlas", "design tool", "Adobe XD", "2025-06-01T00:00:00Z")),
		}
		if got := DetectDecisionConflicts(decisions, decidedAt, items, "2025-09-01T00:00:00Z"); len(got) != 0 {
			t.Errorf("capture before the recency cutoff must be excluded, got %d", len(got))
		}
	})
}
