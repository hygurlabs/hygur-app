package handlers

import (
	"strings"
	"testing"

	"github.com/hygur/sidecar/internal/fact"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/retrieval"
)

// The authoritative "Verified facts" block must (1) carry the value-source rule, (2) voice the
// determined VALUE from the deterministic layer, and (3) merge into the existing system message.
func TestInjectDeterminedFacts_BlockAndRule(t *testing.T) {
	in := []llm.Message{
		{Role: "system", Content: "PERSONA"},
		{Role: "user", Content: "what is my VAT?"},
	}
	subjects := []retrieval.DeterminedFacts{
		{
			Subject: retrieval.EngramSubject{Norm: "alex martin", Type: "person"},
			IsOwner: true,
			Identity: []retrieval.EngramIdentifier{
				{Type: "enterprise_number", Label: "enterprise number", Value: "0000000097", Tier: "high",
					Sources: []fact.Source{{ContentID: "c1", Title: "BCE letter"}}},
			},
		},
	}
	out := injectDeterminedFacts(in, subjects)
	if out[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", out[0].Role)
	}
	sys := out[0].Content
	if !strings.HasPrefix(sys, "PERSONA") {
		t.Errorf("existing system content was not preserved as the prefix")
	}
	// (1) the value-source rule is present.
	if !strings.Contains(sys, "DETERMINED by Hygur's deterministic resolver") {
		t.Errorf("value-source rule missing from the injected block:\n%s", sys)
	}
	if !strings.Contains(sys, "NEVER state such a number that is not in this block") {
		t.Errorf("the never-lift-from-documents rule is missing:\n%s", sys)
	}
	if !strings.Contains(sys, "do NOT substitute a different identifier from the documents") {
		t.Errorf("the no-substitute clause (SIRET->BCE reframe guard) is missing:\n%s", sys)
	}
	// The rule is IDENTIFIER-scoped so ordinary Q&A is unchanged — assert the scoping clause.
	if !strings.Contains(sys, "This rule is about identifiers and reference numbers only") {
		t.Errorf("identifier-scoping clause missing (would risk regressing normal Q&A):\n%s", sys)
	}
	// (2) the determined value + label + tier are voiced from the authoritative layer.
	if !strings.Contains(sys, "enterprise number: 0000000097") {
		t.Errorf("determined value not surfaced in the block:\n%s", sys)
	}
	if !strings.Contains(sys, "high confidence") || !strings.Contains(sys, "BCE letter") {
		t.Errorf("tier/source not surfaced in the block:\n%s", sys)
	}
	// The user message is preserved after the merged system message.
	if len(out) != 2 || out[1].Role != "user" {
		t.Fatalf("user message not preserved: %+v", out)
	}
}

// Empty subjects → the messages pass through untouched (additive layer; non-identifier turns
// with no resolvable subject are unaffected).
func TestInjectDeterminedFacts_EmptyNoop(t *testing.T) {
	in := []llm.Message{{Role: "user", Content: "summarize my week"}}
	out := injectDeterminedFacts(in, nil)
	if len(out) != 1 || out[0].Content != "summarize my week" {
		t.Errorf("empty subjects should be a no-op, got %+v", out)
	}
}
