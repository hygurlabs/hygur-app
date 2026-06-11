package handlers

import (
	"strings"
	"testing"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

func TestInjectBrainContext(t *testing.T) {
	base := []llm.Message{
		{Role: "system", Content: "You are Hygur."},
		{Role: "user", Content: "what about the lease?"},
	}

	// Empty signals → messages unchanged.
	if got := injectBrainContext(base, nil, ""); len(got) != len(base) || got[0].Content != base[0].Content {
		t.Fatalf("empty signals should not change messages, got %+v", got)
	}

	decisions := []*store.Decision{
		{Statement: "Sign the lease by Friday", DecidedOn: "2026-06-10T00:00:00Z"},
		{Statement: "Proceed with vendor A"},
	}
	out := injectBrainContext(base, decisions, "Things are moving on the office move.")
	if len(out) != len(base) {
		t.Fatalf("injection must not add/remove messages (it folds into system), got %d", len(out))
	}
	sys := out[0].Content
	if !strings.Contains(sys, "You are Hygur.") {
		t.Error("must preserve the existing system prompt")
	}
	if !strings.Contains(sys, "standing decisions") ||
		!strings.Contains(sys, "Sign the lease by Friday") ||
		!strings.Contains(sys, "(2026-06-10)") { // dated statement carries a grounding date
		t.Errorf("decisions block missing/incomplete: %q", sys)
	}
	if !strings.Contains(sys, "story so far") || !strings.Contains(sys, "office move") {
		t.Errorf("synopsis block missing: %q", sys)
	}

	// No system message → a fresh system message is prepended.
	out2 := injectBrainContext([]llm.Message{{Role: "user", Content: "hi"}}, decisions, "")
	if len(out2) != 2 || out2[0].Role != "system" || !strings.Contains(out2[0].Content, "Proceed with vendor A") {
		t.Errorf("should prepend a system message, got %+v", out2)
	}
}
