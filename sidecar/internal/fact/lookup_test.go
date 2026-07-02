package fact

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

type fakeStore struct {
	resolve   []string // persons the query resolves to (nil → the appended exact query drives it)
	neighbors []store.Neighbor
	types     map[string]string
	links     map[string][]store.IdentifierLink
	docs      map[string][]string
}

func (f *fakeStore) ResolvePersonNorms(_ context.Context, _ string, _ int) ([]string, error) {
	return f.resolve, nil
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

// TestLookupIdentifier_AmbiguousSubject — a bare surname that resolves to two DISTINCT people
// (Alice Bernard + Bob Bernard) must decline and clarify, never hand back one's number at high.
// Mirrors the real surname/first-name over-match leak (a shared name pooling distinct people). (O1)
func TestLookupIdentifier_AmbiguousSubject(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	// Two DISTINCT people sharing the surname, each with their OWN proximity-linked number.
	// The query pooled both → two distinct proximity values → genuinely ambiguous subject:
	// decline and clarify, never hand back one person's number at high.
	f := &fakeStore{
		resolve:   []string{"alice bernard", "bob bernard"},
		neighbors: []store.Neighbor{{Norm: "nnalice", Weight: 0.030}, {Norm: "nnbob", Weight: 0.028}},
		types:     map[string]string{"nnalice": "id_national_number", "nnbob": "id_national_number"},
		links: map[string][]store.IdentifierLink{
			"nnalice": {{PersonNorm: "alice bernard", IDNorm: "nnalice", Prox: 1}},
			"nnbob":   {{PersonNorm: "bob bernard", IDNorm: "nnbob", Prox: 1}},
		},
		docs: map[string][]string{"nnalice": {"d1", "d2", "d3"}, "nnbob": {"d4", "d5"}},
	}
	r, err := LookupIdentifier(ctx, f, "bernard", "national_number", now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier != TierNone {
		t.Errorf("ambiguous subject: tier = %q (conf %.2f), want none", r.Tier, r.Confidence)
	}
	if r.Value != "" {
		t.Errorf("ambiguous subject must NOT return a value; got %q", r.Value)
	}
	if r.Reason != ReasonAmbiguousSubject {
		t.Errorf("reason = %q, want %q", r.Reason, ReasonAmbiguousSubject)
	}
}

// TestLookupIdentifier_MonoPersonStillHigh — ANTI-OVER-CORRECTION. An unambiguous full-name
// query (one distinct person) with a proximity-linked, single-owner value must STILL affirm.
// Proves the ambiguity/uniqueness guards did not start declining the good cases.
func TestLookupIdentifier_MonoPersonStillHigh(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	f := &fakeStore{
		resolve:   []string{"alice bernard"}, // exactly one distinct person
		neighbors: []store.Neighbor{{Norm: "nnalice", Weight: 0.030}},
		types:     map[string]string{"nnalice": "id_national_number"},
		links:     map[string][]store.IdentifierLink{"nnalice": {{PersonNorm: "alice bernard", IDNorm: "nnalice", Prox: 1}}},
		docs:      map[string][]string{"nnalice": {"d1", "d2", "d3"}},
	}
	r, err := LookupIdentifier(ctx, f, "alice bernard", "national_number", now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier != TierHigh {
		t.Errorf("mono-person good case: tier = %q (conf %.2f), want high", r.Tier, r.Confidence)
	}
	if r.Value != "nnalice" {
		t.Errorf("value = %q, want nnalice", r.Value)
	}
	if len(r.Sources) == 0 {
		t.Error("expected sources for human verification")
	}
}

// TestLookupIdentifier_ContestedValueNotHigh — a value proximity-linked to TWO distinct
// persons violates the uniqueness invariant (one value = one owner). It must NOT be affirmed
// for EITHER queried person, even though each query is itself unambiguous. (O2/V3)
func TestLookupIdentifier_ContestedValueNotHigh(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	base := func(resolvedPerson string) *fakeStore {
		return &fakeStore{
			resolve:   []string{resolvedPerson},
			neighbors: []store.Neighbor{{Norm: "nnshared", Weight: 0.030}},
			types:     map[string]string{"nnshared": "id_national_number"},
			links: map[string][]store.IdentifierLink{"nnshared": {
				{PersonNorm: "alice bernard", IDNorm: "nnshared", Prox: 1},
				{PersonNorm: "bob bernard", IDNorm: "nnshared", Prox: 1},
			}},
			docs: map[string][]string{"nnshared": {"d1", "d2", "d3"}},
		}
	}
	for _, who := range []string{"alice bernard", "bob bernard"} {
		r, err := LookupIdentifier(ctx, base(who), who, "national_number", now)
		if err != nil {
			t.Fatal(err)
		}
		if r.Tier == TierHigh {
			t.Errorf("contested value for %q: tier = high (conf %.2f), want NOT high", who, r.Confidence)
		}
		if r.Reason != ReasonAmbiguousOwner {
			t.Errorf("contested value for %q: reason = %q, want %q", who, r.Reason, ReasonAmbiguousOwner)
		}
	}
}

// TestLookupIdentifier_CollapseSharedID — Flag 2. A single-token query ("acme") that resolves
// to several org name-variants which all SHARE ONE enterprise number is ONE entity → resolve,
// do NOT decline as ambiguous. Fictional Acme Inc. / Acme Corp. (a rename) with one shared BCE.
func TestLookupIdentifier_CollapseSharedID(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	// Two org norm-variants, both proximity-linked to the SAME enterprise number → same entity.
	shared := &fakeStore{
		resolve:   []string{"acme inc", "acme corp"},
		neighbors: []store.Neighbor{{Norm: "en0", Weight: 0.030}},
		types:     map[string]string{"en0": "id_enterprise_number"},
		links: map[string][]store.IdentifierLink{"en0": {
			{PersonNorm: "acme inc", IDNorm: "en0", Prox: 1},
			{PersonNorm: "acme corp", IDNorm: "en0", Prox: 1},
		}},
		docs: map[string][]string{"en0": {"d1", "d2", "d3"}},
	}
	r, err := LookupIdentifier(ctx, shared, "acme", "enterprise_number", now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier == TierNone {
		t.Errorf("shared-id variants should resolve as one entity; got tier=none reason=%q", r.Reason)
	}
	if r.Value != "en0" {
		t.Errorf("value = %q, want en0 (the shared enterprise number)", r.Value)
	}

	// Contrast: two DISTINCT orgs, each with its OWN enterprise number → distinct values →
	// genuinely ambiguous subject → decline (mirrors distinct-person national numbers).
	distinct := &fakeStore{
		resolve:   []string{"acme inc", "beta llc"},
		neighbors: []store.Neighbor{{Norm: "en0", Weight: 0.030}, {Norm: "en1", Weight: 0.029}},
		types:     map[string]string{"en0": "id_enterprise_number", "en1": "id_enterprise_number"},
		links: map[string][]store.IdentifierLink{
			"en0": {{PersonNorm: "acme inc", IDNorm: "en0", Prox: 1}},
			"en1": {{PersonNorm: "beta llc", IDNorm: "en1", Prox: 1}},
		},
		docs: map[string][]string{"en0": {"d1"}, "en1": {"d2"}},
	}
	if r, _ := LookupIdentifier(ctx, distinct, "acme", "enterprise_number", now); r.Tier != TierNone || r.Reason != ReasonAmbiguousSubject {
		t.Errorf("distinct orgs must decline as ambiguous_subject; got tier=%q reason=%q", r.Tier, r.Reason)
	}

	// The bare-surname leak: two distinct people, only ONE with a proximity-linked number (owner
	// count 1). The single value is that person's OWN, not a shared entity identifier → the pool
	// is genuinely ambiguous → decline, never collapse to that one number.
	lone := &fakeStore{
		resolve:   []string{"alice bernard", "bob bernard"},
		neighbors: []store.Neighbor{{Norm: "nnalice", Weight: 0.030}, {Norm: "nnbob", Weight: 0.028}},
		types:     map[string]string{"nnalice": "id_national_number", "nnbob": "id_national_number"},
		links:     map[string][]store.IdentifierLink{"nnalice": {{PersonNorm: "alice bernard", IDNorm: "nnalice", Prox: 1}}},
		docs:      map[string][]string{"nnalice": {"d1", "d2", "d3"}, "nnbob": {"d4"}},
	}
	if r, _ := LookupIdentifier(ctx, lone, "bernard", "national_number", now); r.Tier != TierNone || r.Reason != ReasonAmbiguousSubject {
		t.Errorf("single-owner value in a distinct-people pool must decline; got tier=%q reason=%q", r.Tier, r.Reason)
	}
}
