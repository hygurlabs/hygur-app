package handlers

import (
	"strings"
	"testing"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

func TestInjectBrainContext(t *testing.T) {
	base := []llm.Message{
		{Role: "system", Content: "You are Hygur."},
		{Role: "user", Content: "what about the lease?"},
	}

	// Empty signals → messages unchanged.
	if got := injectBrainContext(base, nil, "", "", "", nil); len(got) != len(base) || got[0].Content != base[0].Content {
		t.Fatalf("empty signals should not change messages, got %+v", got)
	}

	decisions := []*store.Decision{
		{Statement: "Sign the lease by Friday", DecidedOn: "2026-06-10T00:00:00Z"},
		{Statement: "Proceed with vendor A"},
	}
	contras := []contradict.ReconciledConflict{{ClaimConflict: contradict.ClaimConflict{Entity: "ACME invoice", Attribute: "due date", Members: []contradict.ClaimRef{{Value: "June 10"}, {Value: "June 20"}}}, Verdict: contradict.Verdict{Kind: "conflict", Reason: "two due dates"}}}

	// With positions present, the standing facts are shown ONCE — the synthesized
	// positions, NOT the raw decision list (#3). Cached signals carry an "as of" stamp (#5).
	out := injectBrainContext(base, decisions, "The user has committed to the office move and to vendor A.", "Things are moving on the office move.", "2026-06-15", contras)
	if len(out) != len(base) {
		t.Fatalf("injection must not add/remove messages (it folds into system), got %d", len(out))
	}
	sys := out[0].Content
	if !strings.Contains(sys, "You are Hygur.") {
		t.Error("must preserve the existing system prompt")
	}
	if !strings.Contains(sys, "Where the user stands") || !strings.Contains(sys, "committed to the office move") {
		t.Errorf("positions block missing/incomplete: %q", sys)
	}
	if strings.Contains(sys, "standing decisions") || strings.Contains(sys, "Sign the lease by Friday") {
		t.Errorf("#3: raw decisions must NOT appear when positions is present: %q", sys)
	}
	if !strings.Contains(sys, "as of 2026-06-15") {
		t.Errorf("#5: cached signals must be stamped 'as of': %q", sys)
	}
	if !strings.Contains(sys, "story so far") || !strings.Contains(sys, "office move") {
		t.Errorf("synopsis block missing: %q", sys)
	}
	if !strings.Contains(sys, "Open contradictions") || !strings.Contains(sys, "June 10 vs June 20") {
		t.Errorf("contradiction block missing/incomplete: %q", sys)
	}

	// No positions → the raw decision list IS the standing-facts section (fallback).
	rawOut := injectBrainContext(base, decisions, "", "", "", nil)[0].Content
	if !strings.Contains(rawOut, "standing decisions") || !strings.Contains(rawOut, "Sign the lease by Friday") || !strings.Contains(rawOut, "(2026-06-10)") {
		t.Errorf("fallback raw-decisions block missing/incomplete: %q", rawOut)
	}

	// #2: a tight budget keeps the top-priority section and drops the lowest (synopsis).
	bigSynopsis := strings.Repeat("narrative ", 600) // ~6000 chars, over budget
	budgeted := injectBrainContext(base, nil, "You stand by local-first.", bigSynopsis, "", contras)[0].Content
	if !strings.Contains(budgeted, "local-first") {
		t.Error("#2: highest-priority section must always survive the budget")
	}
	if strings.Contains(budgeted, "story so far") {
		t.Error("#2: lowest-priority synopsis must be dropped when over budget")
	}

	// No system message → a fresh system message is prepended.
	out2 := injectBrainContext([]llm.Message{{Role: "user", Content: "hi"}}, decisions, "", "", "", nil)
	if len(out2) != 2 || out2[0].Role != "system" || !strings.Contains(out2[0].Content, "Proceed with vendor A") {
		t.Errorf("should prepend a system message, got %+v", out2)
	}
}
