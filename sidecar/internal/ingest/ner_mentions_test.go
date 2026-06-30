package ingest

import (
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/store"
)

// nerEntityMentions makes Tier-2 NER people/orgs first-class in entity_mentions so the
// subject detector and the graph can see them — handling both []string and []any (JSON
// round-trip) metadata, normalizing like the claim path, tagging with ner_* attributes.
func TestNERMentions(t *testing.T) {
	item := &store.KnowledgeItem{
		CreatedAt: time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC),
		Metadata: map[string]any{
			"extracted_persons": []any{"Alice Bernard", "Bob Carter", ""},
			"extracted_orgs":    []string{"Acme"},
			"extracted_topics":  []any{"Topic1"},
		},
	}
	got := nerEntityMentions(item)
	if len(got) != 4 { // 2 persons (blank dropped) + 1 org + 1 topic
		t.Fatalf("want 4 mentions, got %d: %+v", len(got), got)
	}
	byNorm := map[string]string{} // norm -> attribute
	for _, m := range got {
		byNorm[m.EntityNorm] = m.Attribute
		if m.AssertedAt == "" {
			t.Errorf("mention %q missing asserted_at (item date)", m.EntityNorm)
		}
	}
	if a := byNorm[contradict.NormKey("Alice Bernard")]; a != "ner_person" {
		t.Errorf("person attribute = %q, want ner_person", a)
	}
	if a := byNorm[contradict.NormKey("Acme")]; a != "ner_org" {
		t.Errorf("org attribute = %q, want ner_org", a)
	}

	// Robustness: nil metadata / nil item → no panic, no rows.
	if n := nerEntityMentions(&store.KnowledgeItem{}); n != nil {
		t.Errorf("nil metadata should yield nil, got %v", n)
	}
	if n := nerEntityMentions(nil); n != nil {
		t.Errorf("nil item should yield nil, got %v", n)
	}
}
