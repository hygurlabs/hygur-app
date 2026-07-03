package figure

import (
	"context"
	"testing"

	"github.com/hygur/sidecar/internal/fact"
	"github.com/hygur/sidecar/internal/identity"
	"github.com/hygur/sidecar/internal/store"
)

// fakeStore implements figure.Store over in-memory figure nodes keyed by entity+label — enough to
// exercise the resolution traversal deterministically (no DB, no LLM).
type fakeStore struct {
	nodes   []store.FigureNode
	persons map[string][]string // query token → resolved person norms
}

func (f *fakeStore) ResolvePersonNorms(_ context.Context, query string, _ int) ([]string, error) {
	return f.persons[query], nil
}
func (f *fakeStore) PersonNormsContainingTokens(_ context.Context, _ []string) ([]string, error) {
	return nil, nil
}
func (f *fakeStore) FigureNodesForEntities(_ context.Context, norms []string, label string) ([]store.FigureNode, error) {
	set := map[string]bool{}
	for _, n := range norms {
		set[n] = true
	}
	var out []store.FigureNode
	for _, n := range f.nodes {
		if n.Label == label && set[n.EntityNorm] {
			out = append(out, n)
		}
	}
	return out, nil
}
func (f *fakeStore) GetKnowledgeItem(_ context.Context, contentID string) (*store.KnowledgeItem, error) {
	return &store.KnowledgeItem{ContentID: contentID, Title: "mail:" + contentID}, nil
}

// ownerNode is the masked founder VAT series: a to-pay figure per quarter + one refund, all
// attributed to the owner entity ("denis petit"). Amounts are masked but internally consistent.
func ownerNodes() []store.FigureNode {
	e := "denis petit"
	return []store.FigureNode{
		{ContentID: "q1", EntityNorm: e, Label: "vat", Value: "5100.00", Raw: "5 100,00", Unit: "EUR", Period: "2026-Q1", Direction: DirPayable, Prox: 1},
		{ContentID: "q2", EntityNorm: e, Label: "vat", Value: "6300.00", Raw: "6 300,00", Unit: "EUR", Period: "2026-Q2", Direction: DirPayable, Prox: 1},
		{ContentID: "q3", EntityNorm: e, Label: "vat", Value: "7421.85", Raw: "7 421,85", Unit: "EUR", Period: "2026-Q3", Direction: DirPayable, Prox: 1},
		{ContentID: "r2", EntityNorm: e, Label: "vat", Value: "1250.00", Raw: "1 250,00", Unit: "EUR", Period: "2026-Q2", Direction: DirRefund, Prox: 1},
	}
}

func ownerMatcher() *identity.Matcher {
	return identity.NewMatcher([]string{"denis petit"})
}

func newFake() *fakeStore {
	return &fakeStore{
		nodes:   ownerNodes(),
		persons: map[string][]string{"denis petit": {"denis petit"}},
	}
}

// FIXTURE 1 — "montant de la dernière TVA à payer" → the latest-quarter to-pay value (Q3), WITH its
// period + direction. NOT a random/RAG number, and NOT the refund.
func TestResolve_latestVATPayable(t *testing.T) {
	res, err := Resolve(context.Background(), newFake(), "denis petit", "TVA", "à payer", "", ownerMatcher())
	if err != nil {
		t.Fatal(err)
	}
	if res.Tier != fact.TierHigh {
		t.Fatalf("tier = %q reason=%q; want high", res.Tier, res.Reason)
	}
	if res.Value != "7421.85" || res.Period != "2026-Q3" || res.Direction != DirPayable {
		t.Errorf("got value=%q period=%q dir=%q; want 7421.85 / 2026-Q3 / payable", res.Value, res.Period, res.Direction)
	}
	if len(res.Sources) == 0 {
		t.Error("expected a source")
	}
}

// FIXTURE 2 — "TVA remboursée" (different direction) → the refund figure, not the to-pay one.
func TestResolve_VATRefund(t *testing.T) {
	res, err := Resolve(context.Background(), newFake(), "denis petit", "TVA", "remboursée", "", ownerMatcher())
	if err != nil {
		t.Fatal(err)
	}
	if res.Tier != fact.TierHigh {
		t.Fatalf("tier = %q reason=%q; want high", res.Tier, res.Reason)
	}
	if res.Value != "1250.00" || res.Direction != DirRefund {
		t.Errorf("got value=%q dir=%q; want 1250.00 / refund", res.Value, res.Direction)
	}
}

// FIXTURE 3 — "le montant de TVA" with NO period and NO direction, multiple candidates spanning
// both directions → DECLINE (ambiguous direction). Never a guessed number.
func TestResolve_ambiguousDirection(t *testing.T) {
	res, err := Resolve(context.Background(), newFake(), "denis petit", "TVA", "", "", ownerMatcher())
	if err != nil {
		t.Fatal(err)
	}
	if res.Tier != fact.TierNone || res.Reason != ReasonAmbiguousDir {
		t.Errorf("got tier=%q reason=%q; want none / ambiguous_direction", res.Tier, res.Reason)
	}
	if res.Value != "" {
		t.Errorf("declined result must carry no value, got %q", res.Value)
	}
}

// FIXTURE 4 — a specific past quarter named → that quarter's figure (Q1 to-pay), not the latest.
func TestResolve_namedQuarter(t *testing.T) {
	res, err := Resolve(context.Background(), newFake(), "denis petit", "TVA", "à payer", "Q1 2026", ownerMatcher())
	if err != nil {
		t.Fatal(err)
	}
	if res.Tier != fact.TierHigh || res.Value != "5100.00" || res.Period != "2026-Q1" {
		t.Errorf("got tier=%q value=%q period=%q; want high / 5100.00 / 2026-Q1", res.Tier, res.Value, res.Period)
	}
}

// A period with no matching figure → honest decline (no figure), never the latest as a fallback.
func TestResolve_unknownPeriodDeclines(t *testing.T) {
	res, _ := Resolve(context.Background(), newFake(), "denis petit", "TVA", "à payer", "Q4 2027", ownerMatcher())
	if res.Tier != fact.TierNone || res.Reason != ReasonNoFigure {
		t.Errorf("got tier=%q reason=%q; want none / no_figure", res.Tier, res.Reason)
	}
}

// An unseeded label → decline (unknown label), never a value.
func TestResolve_unknownLabelDeclines(t *testing.T) {
	res, _ := Resolve(context.Background(), newFake(), "denis petit", "chiffre d'affaires", "", "", ownerMatcher())
	if res.Tier != fact.TierNone || res.Reason != ReasonUnknownLabel {
		t.Errorf("got tier=%q reason=%q; want none / unknown_figure_label", res.Tier, res.Reason)
	}
}

// A subject with no figure nodes → decline (no figure).
func TestResolve_noNodesDeclines(t *testing.T) {
	fs := newFake()
	fs.persons["alice bernard"] = []string{"alice bernard"}
	res, _ := Resolve(context.Background(), fs, "alice bernard", "TVA", "à payer", "", ownerMatcher())
	if res.Tier != fact.TierNone || res.Reason != ReasonNoFigure {
		t.Errorf("got tier=%q reason=%q; want none / no_figure", res.Tier, res.Reason)
	}
}
