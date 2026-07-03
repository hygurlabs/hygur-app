package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/fact"
	"github.com/hygur/sidecar/internal/identity"
	"github.com/hygur/sidecar/internal/store"
)

// lookupFakeStore is a minimal fact.Store for the tool's first-person tests. It models the
// owner "alex martin" proximity-linked to ONE well-corroborated enterprise number ("1021")
// across three of his own documents — the real "my VAT" shape, reduced to the essentials.
type lookupFakeStore struct {
	resolve   []string
	neighbors []store.Neighbor
	types     map[string]string
	links     map[string][]store.IdentifierLink
	docs      map[string][]string
}

func (f *lookupFakeStore) ResolvePersonNorms(context.Context, string, int) ([]string, error) {
	return f.resolve, nil
}
func (f *lookupFakeStore) HebbianNeighborsWeighted(context.Context, string, time.Time, float64, int) ([]store.Neighbor, error) {
	return f.neighbors, nil
}
func (f *lookupFakeStore) EntityDominantTypes(context.Context, []string) (map[string]string, error) {
	return f.types, nil
}
func (f *lookupFakeStore) IdentifierLinksForID(_ context.Context, id string) ([]store.IdentifierLink, error) {
	return f.links[id], nil
}
func (f *lookupFakeStore) IdentifierValuesForPersonsOfType(_ context.Context, norms []string, idType string) ([]string, error) {
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
func (f *lookupFakeStore) SearchByIdentifier(_ context.Context, key string, _ int) ([]string, error) {
	return f.docs[key], nil
}
func (f *lookupFakeStore) GetKnowledgeItem(_ context.Context, id string) (*store.KnowledgeItem, error) {
	return &store.KnowledgeItem{ContentID: id, Title: "doc " + id}, nil
}
func (f *lookupFakeStore) NationalNumbersByPersons(context.Context, []string) (map[string][]string, error) {
	return map[string][]string{}, nil
}
func (f *lookupFakeStore) PersonNormsContainingTokens(context.Context, []string) ([]string, error) {
	return []string{"alex martin"}, nil
}

func ownerVATStore() *lookupFakeStore {
	return &lookupFakeStore{
		resolve:   []string{"alex martin"},
		neighbors: []store.Neighbor{{Norm: "1021", Weight: 0.03}},
		types:     map[string]string{"1021": "id_enterprise_number"},
		links: map[string][]store.IdentifierLink{"1021": {
			{ContentID: "o1", PersonNorm: "alex martin", IDNorm: "1021", IDType: "enterprise_number", Prox: 1},
			{ContentID: "o2", PersonNorm: "alex martin", IDNorm: "1021", IDType: "enterprise_number", Prox: 1},
			{ContentID: "o3", PersonNorm: "alex martin", IDNorm: "1021", IDType: "enterprise_number", Prox: 1},
		}},
		docs: map[string][]string{"1021": {"o1", "o2", "o3"}},
	}
}

func ownerMatcherTest() *identity.Matcher {
	return identity.NewMatcher([]string{"Alex Martin", "Alex", "Martin", "am@example.com"})
}

func execLookup(t *testing.T, tool *LookupIdentifierTool, entity, typ string) LookupResponse {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"entity": entity, "type": typ})
	out, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute(%q,%q): %v", entity, typ, err)
	}
	var lr LookupResponse
	if err := json.Unmarshal(out, &lr); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return lr
}

// TestLookupIdentifier_FirstPersonResolvesToOwner is the barrier fixture for « quel est mon
// numéro de TVA » — a FIRST-PERSON subject ("moi"/"mon"/"my") must resolve to the OWNER and
// yield his determined value ("1021"), never decline the un-mappable pronoun. The render label
// echoes the user's wording and the subject reads "you".
func TestLookupIdentifier_FirstPersonResolvesToOwner(t *testing.T) {
	tool := NewLookupIdentifierTool(ownerVATStore(), ownerMatcherTest(), "Alex Martin")
	for _, entity := range []string{"moi", "mon", "ma", "mes", "je", "my", "mine"} {
		lr := execLookup(t, tool, entity, "TVA")
		if lr.Value != "1021" {
			t.Errorf("entity %q: value = %q, want 1021 (owner's determined VAT)", entity, lr.Value)
		}
		if lr.Tier != fact.TierHigh {
			t.Errorf("entity %q: tier = %q, want high", entity, lr.Tier)
		}
		if lr.Subject != "you" {
			t.Errorf("entity %q: subject = %q, want you", entity, lr.Subject)
		}
		if lr.Label != "TVA" {
			t.Errorf("entity %q: label = %q, want the user's wording TVA", entity, lr.Label)
		}
	}
}

// TestLookupIdentifier_FirstPersonAbsentDeclines — a first-person identifier the engine has no
// value for is an HONEST decline (no value, tier none), never a fabrication.
func TestLookupIdentifier_FirstPersonAbsentDeclines(t *testing.T) {
	tool := NewLookupIdentifierTool(ownerVATStore(), ownerMatcherTest(), "Alex Martin")
	lr := execLookup(t, tool, "mon", "IBAN") // no id_iban candidate in the store
	if lr.Tier != fact.TierNone {
		t.Errorf("absent identifier: tier = %q, want none", lr.Tier)
	}
	if lr.Value != "" {
		t.Errorf("absent identifier must not return a value; got %q", lr.Value)
	}
	if lr.Subject != "you" {
		t.Errorf("subject = %q, want you", lr.Subject)
	}
}

// TestLookupIdentifier_FirstPersonNoOwnerDeclines — when the owner is unconfigured, a
// first-person subject cannot resolve to "self": decline honestly instead of guessing.
func TestLookupIdentifier_FirstPersonNoOwnerDeclines(t *testing.T) {
	tool := NewLookupIdentifierTool(ownerVATStore(), ownerMatcherTest(), "") // no owner subject
	lr := execLookup(t, tool, "moi", "TVA")
	if lr.Tier != fact.TierNone || lr.Value != "" {
		t.Errorf("no owner configured: want honest decline, got tier=%q value=%q", lr.Tier, lr.Value)
	}
}

// TestLookupIdentifier_NonFirstPersonUnchanged — a NAMED subject is unaffected by the
// first-person path: the query still routes to that entity's lookup (regression guard).
func TestLookupIdentifier_NonFirstPersonUnchanged(t *testing.T) {
	// "someone else" resolves to nobody in the store → decline, and the subject is echoed as-is
	// (NOT "you"), proving the first-person branch did not capture a named subject.
	tool := NewLookupIdentifierTool(&lookupFakeStore{resolve: []string{"someone else"}}, ownerMatcherTest(), "Alex Martin")
	lr := execLookup(t, tool, "someone else", "TVA")
	if lr.Subject == "you" {
		t.Errorf("named subject must not be treated as first-person; subject=%q", lr.Subject)
	}
}
