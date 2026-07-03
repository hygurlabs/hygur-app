package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hygur/sidecar/internal/fact"
	"github.com/hygur/sidecar/internal/figure"
	"github.com/hygur/sidecar/internal/identity"
	"github.com/hygur/sidecar/internal/store"
)

type figFake struct{ nodes []store.FigureNode }

func (f *figFake) ResolvePersonNorms(_ context.Context, q string, _ int) ([]string, error) {
	return []string{q}, nil
}
func (f *figFake) PersonNormsContainingTokens(_ context.Context, _ []string) ([]string, error) {
	return nil, nil
}
func (f *figFake) FigureNodesForEntities(_ context.Context, norms []string, label string) ([]store.FigureNode, error) {
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
func (f *figFake) GetKnowledgeItem(_ context.Context, id string) (*store.KnowledgeItem, error) {
	return &store.KnowledgeItem{ContentID: id, Title: "VAT declaration"}, nil
}

func figTool() *LookupFigureTool {
	fs := &figFake{nodes: []store.FigureNode{
		{ContentID: "q3", EntityNorm: "denis petit", Label: "vat", Value: "7421.85", Raw: "7 421,85", Unit: "EUR", Period: "2026-Q3", Direction: figure.DirPayable, Prox: 1},
	}}
	return NewLookupFigureTool(fs, identity.NewMatcher([]string{"denis petit"}), "denis petit")
}

// The first-person "my VAT to pay" resolves through the owner anchor and composes a display value
// (amount + unit) and heading (label + direction + period) the determined_answer card renders.
func TestLookupFigureTool_firstPersonPayable(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{"entity": "moi", "label": "TVA", "direction": "à payer"})
	res, err := figTool().Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var fr FigureResponse
	if err := json.Unmarshal(res, &fr); err != nil {
		t.Fatal(err)
	}
	if fr.Tier != fact.TierHigh {
		t.Fatalf("tier=%q reason=%q; want high", fr.Tier, fr.Reason)
	}
	if fr.Value != "7 421,85 €" {
		t.Errorf("display value = %q; want '7 421,85 €'", fr.Value)
	}
	if fr.Label != "VAT to pay · Q3 2026" {
		t.Errorf("heading = %q; want 'VAT to pay · Q3 2026'", fr.Label)
	}
	if fr.Subject != "you" {
		t.Errorf("subject = %q; want 'you'", fr.Subject)
	}
}

// An unconfigured owner makes first-person an honest decline (no value), never a guess.
func TestLookupFigureTool_firstPersonNoOwner(t *testing.T) {
	tool := NewLookupFigureTool(&figFake{}, identity.NewMatcher(nil), "")
	raw, _ := json.Marshal(map[string]string{"entity": "moi", "label": "TVA"})
	res, _ := tool.Execute(context.Background(), raw)
	var fr FigureResponse
	_ = json.Unmarshal(res, &fr)
	if fr.Tier != fact.TierNone || fr.Value != "" {
		t.Errorf("expected decline with no value, got tier=%q value=%q", fr.Tier, fr.Value)
	}
}
