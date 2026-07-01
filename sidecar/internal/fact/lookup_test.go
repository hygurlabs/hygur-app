package fact

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

type fakeStore struct {
	neighbors []store.Neighbor
	types     map[string]string
	links     map[string][]store.IdentifierLink
	docs      map[string][]string
}

func (f *fakeStore) ResolvePersonNorms(_ context.Context, _ string, _ int) ([]string, error) {
	return nil, nil // the appended exact query drives the test
}
func (f *fakeStore) HebbianNeighborsWeighted(_ context.Context, _ string, _ time.Time, _ float64, _ int) ([]store.Neighbor, error) {
	return f.neighbors, nil
}
func (f *fakeStore) EntityDominantTypes(_ context.Context, _ []string) (map[string]string, error) {
	return f.types, nil
}
func (f *fakeStore) IdentifierLinksForID(_ context.Context, id string) ([]store.IdentifierLink, error) {
	return f.links[id], nil
}
func (f *fakeStore) SearchByIdentifier(_ context.Context, key string, _ int) ([]string, error) {
	return f.docs[key], nil
}
func (f *fakeStore) GetKnowledgeItem(_ context.Context, id string) (*store.KnowledgeItem, error) {
	return &store.KnowledgeItem{ContentID: id, Title: "doc " + id}, nil
}

func TestLookupIdentifier(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	// Two national-number candidates for "elric": the child's own (proximity-linked, slightly
	// LOWER NPMI, well corroborated) vs a parent's (higher NPMI, no proximity to the child).
	// Proximity must break the tie in favor of the child's own number.
	f := &fakeStore{
		neighbors: []store.Neighbor{
			{Norm: "nnchild", Weight: 0.020},
			{Norm: "nnparent", Weight: 0.025},
			{Norm: "anne bernard", Weight: 0.030},
		},
		types: map[string]string{
			"nnchild": "id_national_number", "nnparent": "id_national_number", "anne bernard": "ner_person",
		},
		links: map[string][]store.IdentifierLink{
			"nnchild": {{PersonNorm: "elric", IDNorm: "nnchild", Prox: 1}},
		},
		docs: map[string][]string{
			"nnchild": {"d1", "d2", "d3", "d4"}, "nnparent": {"d5", "d6"},
		},
	}
	r, err := LookupIdentifier(ctx, f, "elric", "national_number", now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Value != "nnchild" {
		t.Errorf("value = %q, want nnchild (proximity should win)", r.Value)
	}
	if r.Tier != TierHigh {
		t.Errorf("tier = %q (conf %.2f), want high", r.Tier, r.Confidence)
	}
	if len(r.Sources) == 0 {
		t.Error("expected sources for human verification")
	}

	// No proximity anywhere → a lone weak candidate lands in the hedge band, not affirm.
	f.links = map[string][]store.IdentifierLink{}
	f.neighbors = []store.Neighbor{{Norm: "nnparent", Weight: 0.025}}
	f.docs = map[string][]string{"nnparent": {"d5"}}
	if r, _ := LookupIdentifier(ctx, f, "elric", "national_number", now); r.Tier == TierHigh {
		t.Errorf("no-proximity lone candidate should not affirm; got tier=%q conf=%.2f", r.Tier, r.Confidence)
	}

	// No typed-identifier neighbor at all → decline.
	f.types = map[string]string{"nnparent": "ner_person"}
	if r, _ := LookupIdentifier(ctx, f, "elric", "national_number", now); r.Tier != TierNone || r.Value != "" {
		t.Errorf("no candidate should decline; got %+v", r)
	}
}
