package fact

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/identity"
	"github.com/hygur/sidecar/internal/store"
)

type fakeStore struct {
	resolve     []string // persons the query resolves to (nil → the appended exact query drives it)
	neighbors   []store.Neighbor
	types       map[string]string
	links       map[string][]store.IdentifierLink
	docs        map[string][]string
	natNums     map[string][]string // person norm → its own national_number values (father/son guard)
	personNorms []string            // owner-variant candidates returned by PersonNormsContainingTokens
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
func (f *fakeStore) IdentifierValuesForPersonsOfType(_ context.Context, norms []string, idType string) ([]string, error) {
	in := map[string]bool{}
	for _, n := range norms {
		in[n] = true
	}
	seen := map[string]bool{}
	var out []string
	for idNorm, links := range f.links {
		for _, l := range links {
			if l.IDType == idType && in[l.PersonNorm] && !seen[idNorm] {
				seen[idNorm] = true
				out = append(out, idNorm)
			}
		}
	}
	return out, nil
}
func (f *fakeStore) SearchByIdentifier(_ context.Context, key string, _ int) ([]string, error) {
	return f.docs[key], nil
}
func (f *fakeStore) GetKnowledgeItem(_ context.Context, id string) (*store.KnowledgeItem, error) {
	return &store.KnowledgeItem{ContentID: id, Title: "doc " + id}, nil
}
func (f *fakeStore) NationalNumbersByPersons(_ context.Context, norms []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, n := range norms {
		if v := f.natNums[n]; len(v) > 0 {
			out[n] = v
		}
	}
	return out, nil
}
func (f *fakeStore) PersonNormsContainingTokens(_ context.Context, _ []string) ([]string, error) {
	return f.personNorms, nil
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
	r, err := LookupIdentifier(ctx, f, "elric", "national_number", now, nil)
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
	if r, _ := LookupIdentifier(ctx, f, "elric", "national_number", now, nil); r.Tier == TierHigh {
		t.Errorf("no-proximity lone candidate should not affirm; got tier=%q conf=%.2f", r.Tier, r.Confidence)
	}

	// No typed-identifier neighbor at all → decline.
	f.types = map[string]string{"nnparent": "ner_person"}
	if r, _ := LookupIdentifier(ctx, f, "elric", "national_number", now, nil); r.Tier != TierNone || r.Value != "" {
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
	r, err := LookupIdentifier(ctx, f, "bernard", "national_number", now, nil)
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
	r, err := LookupIdentifier(ctx, f, "alice bernard", "national_number", now, nil)
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

// TestLookupIdentifier_FamilyBCappedAtMed — a FAMILY-B label-derived type (id_duns) with the
// SAME proximity-linked, well-corroborated evidence that affirms a checksum type HIGH must be
// capped at MED: a label binding is not intrinsic proof. Checksum types (family A) still affirm.
func TestLookupIdentifier_FamilyBCappedAtMed(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	mk := func(attr string) *fakeStore {
		return &fakeStore{
			resolve:   []string{"acme sprl"},
			neighbors: []store.Neighbor{{Norm: "idval", Weight: 0.030}},
			types:     map[string]string{"idval": attr},
			links:     map[string][]store.IdentifierLink{"idval": {{PersonNorm: "acme sprl", IDNorm: "idval", Prox: 1}}},
			docs:      map[string][]string{"idval": {"d1", "d2", "d3"}},
		}
	}
	// Family B: DUNS — capped at med even though proximity + corroboration would score high.
	r, err := LookupIdentifier(ctx, mk("id_duns"), "acme sprl", "duns", now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier != TierMed {
		t.Errorf("family-B duns: tier = %q (conf %.2f), want medium (capped)", r.Tier, r.Confidence)
	}
	if r.Value != "idval" {
		t.Errorf("family-B duns: value = %q, want idval", r.Value)
	}
	// Family A: enterprise_number — the identical evidence still affirms HIGH (checksum path intact).
	r, err = LookupIdentifier(ctx, mk("id_enterprise_number"), "acme sprl", "enterprise_number", now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier != TierHigh {
		t.Errorf("family-A enterprise_number: tier = %q, want high (unchanged)", r.Tier)
	}
}

// TestLookupIdentifier_PersonVariantsResolve — the over-decline regression. A query that pools
// several norms which are really ONE person (reversed order + a middle name: {bernard,alice} and
// {alice,marie,bernard}) must NOT decline as ambiguous_subject. Its number is proximity-linked to
// both variant norms; the owner cluster is one person, so it resolves to high (not ambiguous_owner).
func TestLookupIdentifier_PersonVariantsResolve(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	f := &fakeStore{
		resolve:   []string{"bernard alice", "alice marie bernard"}, // one person, two variants
		neighbors: []store.Neighbor{{Norm: "nnown", Weight: 0.030}},
		types:     map[string]string{"nnown": "id_national_number"},
		links: map[string][]store.IdentifierLink{"nnown": {
			{PersonNorm: "bernard alice", IDNorm: "nnown", Prox: 1},
			{PersonNorm: "alice marie bernard", IDNorm: "nnown", Prox: 1},
		}},
		docs: map[string][]string{"nnown": {"d1", "d2", "d3"}},
	}
	r, err := LookupIdentifier(ctx, f, "alice marie bernard", "national_number", now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier != TierHigh {
		t.Errorf("single-person variants: tier = %q (conf %.2f, reason %q), want high", r.Tier, r.Confidence, r.Reason)
	}
	if r.Value != "nnown" {
		t.Errorf("value = %q, want nnown", r.Value)
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
		r, err := LookupIdentifier(ctx, base(who), who, "national_number", now, nil)
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
	r, err := LookupIdentifier(ctx, shared, "acme", "enterprise_number", now, nil)
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
	if r, _ := LookupIdentifier(ctx, distinct, "acme", "enterprise_number", now, nil); r.Tier != TierNone || r.Reason != ReasonAmbiguousSubject {
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
	if r, _ := LookupIdentifier(ctx, lone, "bernard", "national_number", now, nil); r.Tier != TierNone || r.Reason != ReasonAmbiguousSubject {
		t.Errorf("single-owner value in a distinct-people pool must decline; got tier=%q reason=%q", r.Tier, r.Reason)
	}
}

// ownerMatcher is the fictional corpus owner "Alex Martin" used by the dominance tests.
// Same shape as the real config: two ambiguous bare variants + discriminative names + email.
func ownerMatcher() *identity.Matcher {
	return identity.NewMatcher([]string{"Alex Martin", "Alex", "Alex M", "Martin", "am@example.com"})
}

// dominanceStore builds a store where the owner "alex martin" and one institution "acme sa"
// are both proximity-linked to ONE value across the given per-holder document counts.
func dominanceStore(ownerDocs, instDocs int) *fakeStore {
	var links []store.IdentifierLink
	docIDs := []string{}
	d := 0
	for i := 0; i < ownerDocs; i++ {
		id := "o" + string(rune('a'+d))
		d++
		links = append(links, store.IdentifierLink{ContentID: id, PersonNorm: "alex martin", IDNorm: "nnval", Prox: 1})
		docIDs = append(docIDs, id)
	}
	for i := 0; i < instDocs; i++ {
		id := "i" + string(rune('a'+d))
		d++
		links = append(links, store.IdentifierLink{ContentID: id, PersonNorm: "acme sa", IDNorm: "nnval", Prox: 1})
		docIDs = append(docIDs, id)
	}
	return &fakeStore{
		resolve:   []string{"alex martin"},
		neighbors: []store.Neighbor{{Norm: "nnval", Weight: 0.030}},
		types:     map[string]string{"nnval": "id_national_number"},
		links:     map[string][]store.IdentifierLink{"nnval": links},
		docs:      map[string][]string{"nnval": docIDs},
	}
}

// TestLookupIdentifier_OwnerDominanceResolves — the owner's OWN reference number, reprinted by
// an institution, looks contested (2 distinct owners) yet the owner holds a decisive plurality
// of the proximity docs (5 vs 1). Owner anchor + dominance → affirm, not ambiguous_owner.
func TestLookupIdentifier_OwnerDominanceResolves(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	f := dominanceStore(5, 1)
	r, err := LookupIdentifier(ctx, f, "alex martin", "national_number", now, ownerMatcher())
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier != TierHigh {
		t.Errorf("owner dominant 5-vs-1: tier=%q (conf %.2f, reason %q), want high", r.Tier, r.Confidence, r.Reason)
	}
	if r.Value != "nnval" {
		t.Errorf("value = %q, want nnval", r.Value)
	}
}

// TestLookupIdentifier_OwnerDominanceDeclines — an EVEN 2-vs-2 split and a non-decisive 3-vs-2
// (fails the ≥2× margin) are NOT a plurality: keep declining ambiguous_owner (fail closed).
func TestLookupIdentifier_OwnerDominanceDeclines(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	for _, tc := range []struct {
		name        string
		owner, inst int
	}{
		{"even 2-vs-2", 2, 2},
		{"no decisive margin 3-vs-2", 3, 2},
	} {
		f := dominanceStore(tc.owner, tc.inst)
		r, err := LookupIdentifier(ctx, f, "alex martin", "national_number", now, ownerMatcher())
		if err != nil {
			t.Fatal(err)
		}
		if r.Tier == TierHigh {
			t.Errorf("%s: tier=high, want NOT high", tc.name)
		}
		if r.Reason != ReasonAmbiguousOwner {
			t.Errorf("%s: reason=%q, want %q", tc.name, r.Reason, ReasonAmbiguousOwner)
		}
	}
}

// TestLookupIdentifier_NonOwnerNeverGetsOwnerValue — the mis-attribution guard. Querying the
// institution (a non-owner) for a value the OWNER dominates must NOT hand back the owner's
// number: dominance only resolves when the queried subject IS the dominant owner.
func TestLookupIdentifier_NonOwnerNeverGetsOwnerValue(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	f := dominanceStore(5, 1)
	f.resolve = []string{"acme sa"} // the institution asks
	r, err := LookupIdentifier(ctx, f, "acme sa", "national_number", now, ownerMatcher())
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier == TierHigh {
		t.Errorf("non-owner query: tier=high, want NOT high (no mis-attribution)")
	}
	if r.Reason != ReasonAmbiguousOwner {
		t.Errorf("non-owner query: reason=%q, want %q", r.Reason, ReasonAmbiguousOwner)
	}
}

// TestLookupIdentifier_OwnerCrossVariantProximity — the owner anchor's proximity half. The
// value neighbors the given-first variant ("alex martin") but is proximity-linked under the
// surname-first variant ("martin alex"), which the query did not resolve to. Because the owner
// is ONE unified subject, that cross-variant link still counts as proximity to him → affirm.
func TestLookupIdentifier_OwnerCrossVariantProximity(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	var links []store.IdentifierLink
	docs := []string{}
	for i := 0; i < 5; i++ {
		id := "cv" + string(rune('a'+i))
		links = append(links, store.IdentifierLink{ContentID: id, PersonNorm: "martin alex", IDNorm: "nnval", Prox: 1})
		docs = append(docs, id)
	}
	links = append(links, store.IdentifierLink{ContentID: "iz", PersonNorm: "acme sa", IDNorm: "nnval", Prox: 1})
	docs = append(docs, "iz")
	f := &fakeStore{
		resolve:   []string{"alex martin"}, // given-first; the link is under surname-first
		neighbors: []store.Neighbor{{Norm: "nnval", Weight: 0.030}},
		types:     map[string]string{"nnval": "id_national_number"},
		links:     map[string][]store.IdentifierLink{"nnval": links},
		docs:      map[string][]string{"nnval": docs},
		// PersonNormsContainingTokens returns nothing extra: prox must come from owner-awareness,
		// not from pooling the surname-first variant.
	}
	r, err := LookupIdentifier(ctx, f, "alex martin", "national_number", now, ownerMatcher())
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier != TierHigh {
		t.Errorf("owner cross-variant proximity: tier=%q (conf %.2f, reason %q), want high", r.Tier, r.Confidence, r.Reason)
	}
}

// TestLookupIdentifier_RareIdentifierRecallFloor reproduces the founder-DUNS regression: a
// single-document identifier (a DUNS printed once, in one Apple mail) that is proximity-linked
// to a highly-connected subject is crowded OUT of that subject's top-K Hebbian neighbors, so the
// old candidate enumeration (neighbors only) missed it and declined with candidates:0. The
// proximity link is authoritative, so the value must still be surfaced — hedged at medium
// (family-B label fact, no checksum → never affirmed high). Values below are fictional.
func TestLookupIdentifier_RareIdentifierRecallFloor(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	// The subject's top neighbors are its frequent correspondents — the rare DUNS value is NOT
	// among them (the bug). It is only reachable via the proximity link.
	f := &fakeStore{
		neighbors: []store.Neighbor{
			{Norm: "some org", Weight: 0.9},
			{Norm: "a contact", Weight: 0.8},
		},
		types: map[string]string{"some org": "ner_org", "a contact": "ner_person"},
		links: map[string][]store.IdentifierLink{
			"824190537": {{PersonNorm: "denis petit", IDNorm: "824190537", IDType: "duns", Prox: 1}},
		},
		docs: map[string][]string{"824190537": {"apple-mail"}},
	}
	r, err := LookupIdentifier(ctx, f, "denis petit", "duns", now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Candidates == 0 {
		t.Fatalf("recall floor: DUNS is proximity-linked but candidates=0 (crowded out of neighbors)")
	}
	if r.Value != "824190537" {
		t.Errorf("value = %q, want 824190537 (the proximity-linked DUNS)", r.Value)
	}
	if r.Tier != TierMed {
		t.Errorf("tier = %q (conf %.2f), want medium (label fact, hedged)", r.Tier, r.Confidence)
	}
}

// TestLookupIdentifier_ParasiteDeclines — the live "property-tax parasite" regression. The
// owner's real enterprise number is not typed for him, but a 10-digit reference from a single
// property-tax document coincidentally passes the enterprise-number checksum, gets typed
// id_enterprise_number and sits (proximity) next to his name in that ONE doc — competing with
// other candidate values. It must DECLINE (fail closed), never affirm the coincidental match.
func TestLookupIdentifier_ParasiteDeclines(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	// Two candidate values compete: the parasite (prox to owner, 1 doc) and a graph-only other.
	f := &fakeStore{
		resolve:   []string{"alex martin"},
		neighbors: []store.Neighbor{{Norm: "parasite", Weight: 0.030}, {Norm: "other", Weight: 0.020}},
		types:     map[string]string{"parasite": "id_enterprise_number", "other": "id_enterprise_number"},
		links:     map[string][]store.IdentifierLink{"parasite": {{ContentID: "tax1", PersonNorm: "alex martin", IDNorm: "parasite", Prox: 1}}},
		docs:      map[string][]string{"parasite": {"tax1"}, "other": {"d9"}},
	}
	r, err := LookupIdentifier(ctx, f, "alex martin", "enterprise_number", now, ownerMatcher())
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier != TierNone {
		t.Errorf("single-doc parasite amid competitors: tier=%q (conf %.2f), want none (decline)", r.Tier, r.Confidence)
	}
	if r.Reason != ReasonUncorroborated {
		t.Errorf("reason=%q, want %q", r.Reason, ReasonUncorroborated)
	}
	if len(r.Sources) == 0 {
		t.Error("expected the source doc surfaced for human verification even on decline")
	}
}

// corroborationStore builds an enterprise_number graph reproducing the live VAT-ranking bug:
// several candidate values compete, each proximity-linked to the owner across a different number
// of distinct documents (ownerDocs), plus optionally one institutional co-link so the value looks
// "contested" like the founder's reprinted BCE. weight sets the Hebbian/NPMI edge — a RARE value
// (few owner docs) is given a HIGH weight so NPMI would wrongly rank it first; the owner's real,
// frequently-reprinted value is given a diluted (low) weight. corpusDocs sets SearchByIdentifier's
// corroboration count. This is the founder's real shape: 1021 (11 owner docs, diluted edge) vs a
// single-doc parasite 152 (npmi≈1) vs the old 1014 (2 owner docs).
func corroborationStore(query string, ownerVariant string, specs []evSpec) *fakeStore {
	links := map[string][]store.IdentifierLink{}
	docs := map[string][]string{}
	neigh := []store.Neighbor{}
	types := map[string]string{}
	for _, sp := range specs {
		var ll []store.IdentifierLink
		var dd []string
		for i := 0; i < sp.ownerDocs; i++ {
			id := sp.value + "-o" + string(rune('a'+i))
			ll = append(ll, store.IdentifierLink{ContentID: id, PersonNorm: ownerVariant, IDNorm: sp.value, IDType: "enterprise_number", Prox: 1})
			dd = append(dd, id)
		}
		for i := 0; i < sp.instDocs; i++ {
			id := sp.value + "-i" + string(rune('a'+i))
			ll = append(ll, store.IdentifierLink{ContentID: id, PersonNorm: sp.inst, IDNorm: sp.value, IDType: "enterprise_number", Prox: 1})
			dd = append(dd, id)
		}
		links[sp.value] = ll
		// corpusDocs drives corroboration (SearchByIdentifier); fall back to the link docs.
		if sp.corpusDocs > 0 {
			cd := make([]string, sp.corpusDocs)
			for i := range cd {
				cd[i] = sp.value + "-c" + string(rune('a'+i))
			}
			docs[sp.value] = cd
		} else {
			docs[sp.value] = dd
		}
		neigh = append(neigh, store.Neighbor{Norm: sp.value, Weight: sp.weight})
		types[sp.value] = "id_enterprise_number"
	}
	return &fakeStore{
		resolve:   []string{query},
		neighbors: neigh,
		types:     types,
		links:     links,
		docs:      docs,
	}
}

type evSpec struct {
	value      string
	ownerDocs  int     // distinct docs the OWNER is proximity-linked in
	instDocs   int     // distinct docs one institution is proximity-linked in (runner-up)
	inst       string  // that institution's norm
	weight     float64 // Hebbian/NPMI edge — rare values get HIGH weight to game NPMI
	corpusDocs int     // SearchByIdentifier corroboration count (0 → use link docs)
}

// TestLookupIdentifier_OwnerCorroborationSelectsRecurringValue — the VAT-ranking bug, fixed.
// NPMI would rank the single-doc parasite "152" first (rare edge → npmi≈1). The owner's REAL
// BCE "1021" recurs across 11 of his docs but has a diluted edge (npmi≈0 → floor score). The
// owner-corroboration selection must OVERRIDE the NPMI pick and choose "1021" (11 owner docs ≥ 2×
// the old "1014"'s 2), affirming HIGH — never the parasite "152" (1 doc) nor the old "1014" (2).
func TestLookupIdentifier_OwnerCorroborationSelectsRecurringValue(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	f := corroborationStore("alex martin", "alex martin", []evSpec{
		// The real, frequently-reprinted BCE: 11 owner docs, 1 institutional co-link, DILUTED edge.
		{value: "1021", ownerDocs: 11, instDocs: 1, inst: "acme sa", weight: 0.001, corpusDocs: 12},
		// The single-doc parasite (a tax rôle): 1 owner doc, but a RARE, undiluted edge → npmi≈1.
		{value: "152", ownerDocs: 1, inst: "", weight: 0.030, corpusDocs: 1},
		// The stale former number: 2 owner docs.
		{value: "1014", ownerDocs: 2, inst: "", weight: 0.010, corpusDocs: 2},
	})
	r, err := LookupIdentifier(ctx, f, "alex martin", "enterprise_number", now, ownerMatcher())
	if err != nil {
		t.Fatal(err)
	}
	if r.Value != "1021" {
		t.Errorf("value = %q, want 1021 (owner-corroboration must beat the NPMI-rare parasite)", r.Value)
	}
	if r.Value == "152" {
		t.Error("selected the single-doc parasite 152 — NPMI rarity leaked through")
	}
	if r.Value == "1014" {
		t.Error("selected the stale former number 1014 — margin gate failed")
	}
	if r.Tier != TierHigh {
		t.Errorf("tier = %q (conf %.2f), want high (checksum type, owner-dominant)", r.Tier, r.Confidence)
	}
}

// TestLookupIdentifier_OwnerCorroborationNearTieDeclines — no ≥2× owner-corroboration winner.
// Two contested candidates each with 3 owner docs (and a 2-doc institutional runner-up so neither
// is owner-dominant): the selection must NOT force a pick, and the existing guards must decline
// (fail closed) rather than affirm the NPMI winner of a near-tie.
func TestLookupIdentifier_OwnerCorroborationNearTieDeclines(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	f := corroborationStore("alex martin", "alex martin", []evSpec{
		{value: "aaa", ownerDocs: 3, instDocs: 2, inst: "acme sa", weight: 0.030, corpusDocs: 5},
		{value: "bbb", ownerDocs: 3, instDocs: 2, inst: "beta bv", weight: 0.028, corpusDocs: 5},
	})
	r, err := LookupIdentifier(ctx, f, "alex martin", "enterprise_number", now, ownerMatcher())
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier == TierHigh {
		t.Errorf("near-tie: tier = high (value %q), want NOT high (no ≥2× winner → decline)", r.Value)
	}
	if r.Reason != ReasonAmbiguousOwner {
		t.Errorf("near-tie: reason = %q, want %q", r.Reason, ReasonAmbiguousOwner)
	}
}

// TestLookupIdentifier_OwnerCorroborationRejectsParasiteAndStale — the selection must not pick a
// value that fails owner-dominance even when it is the max-ownerDocs proximity candidate. Here the
// stale "1014" (2 owner docs) leads the parasite "152" (1) but 2 < 3 → not owner-dominant → no
// selection; and neither is corroborated enough to affirm → decline. Guards the fail-closed floor.
func TestLookupIdentifier_OwnerCorroborationRejectsParasiteAndStale(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	f := corroborationStore("alex martin", "alex martin", []evSpec{
		{value: "152", ownerDocs: 1, inst: "", weight: 0.030, corpusDocs: 1},
		{value: "1014", ownerDocs: 2, inst: "", weight: 0.010, corpusDocs: 1},
	})
	r, err := LookupIdentifier(ctx, f, "alex martin", "enterprise_number", now, ownerMatcher())
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier == TierHigh {
		t.Errorf("no owner-dominant candidate: tier = high (value %q), want NOT high", r.Value)
	}
}

// TestLookupIdentifier_CorroboratedSurvivesGuard — a well-corroborated winner (≥2 docs) amid
// competing candidates is UNAFFECTED by the corroboration guard: it still affirms. Guards the
// no-regression boundary (only the single-doc coincidence declines).
func TestLookupIdentifier_CorroboratedSurvivesGuard(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	f := &fakeStore{
		resolve:   []string{"acme sprl"},
		neighbors: []store.Neighbor{{Norm: "real", Weight: 0.030}, {Norm: "other", Weight: 0.020}},
		types:     map[string]string{"real": "id_enterprise_number", "other": "id_enterprise_number"},
		links:     map[string][]store.IdentifierLink{"real": {{ContentID: "d1", PersonNorm: "acme sprl", IDNorm: "real", Prox: 1}}},
		docs:      map[string][]string{"real": {"d1", "d2", "d3"}, "other": {"d9"}},
	}
	r, err := LookupIdentifier(ctx, f, "acme sprl", "enterprise_number", now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier != TierHigh {
		t.Errorf("corroborated winner amid competitors: tier=%q, want high (guard must not fire)", r.Tier)
	}
	if r.Value != "real" {
		t.Errorf("value=%q, want real", r.Value)
	}
}
