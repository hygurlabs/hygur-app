package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// stubTool is a minimal Tool used only by these tests.
type stubTool struct {
	name       string
	desc       string
	sideEffect bool
	exec       func(ctx context.Context, args json.RawMessage) (json.RawMessage, error)
}

func (s *stubTool) Name() string                 { return s.name }
func (s *stubTool) Description() string          { return s.desc }
func (s *stubTool) SideEffect() bool             { return s.sideEffect }
func (s *stubTool) ParameterSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (s *stubTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	return s.exec(ctx, args)
}

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	tool := &stubTool{name: "create_note", desc: "Saves a note"}
	if err := r.Register(tool); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Lookup("create_note")
	if !ok {
		t.Fatalf("Lookup: tool not found")
	}
	if got.Name() != "create_note" {
		t.Fatalf("Lookup: got %q", got.Name())
	}
}

func TestRegistry_RegisterRejectsEmptyName(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubTool{name: ""}); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestRegistry_RegisterRejectsNil(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Fatal("expected error for nil tool")
	}
}

func TestRegistry_RegisterRejectsDuplicate(t *testing.T) {
	r := NewRegistry()
	tool := &stubTool{name: "dup"}
	if err := r.Register(tool); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register(tool)
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected 'already registered' error, got %v", err)
	}
}

func TestRegistry_OpenAIDefinitionsEmpty(t *testing.T) {
	r := NewRegistry()
	if defs := r.OpenAIDefinitions(); defs != nil {
		t.Fatalf("expected nil for empty registry, got %v", defs)
	}
}

func TestRegistry_OpenAIDefinitionsShape(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(&stubTool{name: "z_tool", desc: "Z"})
	r.MustRegister(&stubTool{name: "a_tool", desc: "A"})

	defs := r.OpenAIDefinitions()
	if len(defs) != 2 {
		t.Fatalf("got %d defs, want 2", len(defs))
	}
	// Ordering must be deterministic (alphabetical) so the LLM payload is
	// stable across runs.
	first := defs[0]["function"].(map[string]any)["name"]
	second := defs[1]["function"].(map[string]any)["name"]
	if first != "a_tool" || second != "z_tool" {
		t.Fatalf("expected alphabetical order, got %v / %v", first, second)
	}
	if defs[0]["type"] != "function" {
		t.Fatalf("expected type=function, got %v", defs[0]["type"])
	}
}

func TestRegistry_ExecuteUnknownTool(t *testing.T) {
	r := NewRegistry()
	_, err := r.Execute(context.Background(), "missing", json.RawMessage(`{}`))
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("expected ErrUnknownTool, got %v", err)
	}
}

func TestRegistry_ExecuteForwardsArgsAndResult(t *testing.T) {
	called := false
	r := NewRegistry()
	r.MustRegister(&stubTool{
		name: "echo",
		exec: func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
			called = true
			return args, nil
		},
	})

	got, err := r.Execute(context.Background(), "echo", json.RawMessage(`{"a":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !called {
		t.Fatal("Execute did not invoke the tool")
	}
	if string(got) != `{"a":1}` {
		t.Fatalf("got %q", string(got))
	}
}

// TestRegistry_SideEffectToolNeverExecutedWithoutConfirm is the WP3 gate
// guarantee (test a): a SideEffect tool routed through Execute is NEVER run —
// it is held pending and only runs via ExecuteConfirmed after the user approves.
func TestRegistry_SideEffectToolNeverExecutedWithoutConfirm(t *testing.T) {
	executed := false
	r := NewRegistry()
	r.SetPendingStore(NewPendingActionStore(PendingActionTTL))
	r.MustRegister(&stubTool{
		name:       "create_note",
		sideEffect: true,
		exec: func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
			executed = true
			return args, nil
		},
	})

	out, err := r.Execute(context.Background(), "create_note", json.RawMessage(`{"title":"x"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if executed {
		t.Fatal("SideEffect tool was executed by the registry without confirmation")
	}
	var pr PendingResult
	if err := json.Unmarshal(out, &pr); err != nil || !pr.Pending || pr.ActionID == "" {
		t.Fatalf("expected a pending envelope, got %s (err %v)", out, err)
	}

	// The confirm path runs it exactly once.
	if _, err := r.ExecuteConfirmed(context.Background(), "create_note", json.RawMessage(`{"title":"x"}`)); err != nil {
		t.Fatalf("ExecuteConfirmed: %v", err)
	}
	if !executed {
		t.Fatal("ExecuteConfirmed did not run the tool")
	}
}

// TestRegistry_SideEffectFailsClosedWithoutStore proves the fail-closed default:
// with no pending store configured, a SideEffect tool is refused, not run.
func TestRegistry_SideEffectFailsClosedWithoutStore(t *testing.T) {
	executed := false
	r := NewRegistry()
	r.MustRegister(&stubTool{
		name:       "create_note",
		sideEffect: true,
		exec: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			executed = true
			return nil, nil
		},
	})
	_, err := r.Execute(context.Background(), "create_note", json.RawMessage(`{}`))
	if !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("expected ErrConfirmationRequired, got %v", err)
	}
	if executed {
		t.Fatal("SideEffect tool ran despite missing confirmation gate")
	}
}

// TestPendingActionStore_TakeAndExpiry covers the TTL + one-shot semantics.
func TestPendingActionStore_TakeAndExpiry(t *testing.T) {
	s := NewPendingActionStore(10 * time.Minute)
	s.Add(PendingAction{ActionID: "a1", ToolName: "create_note", CreatedAt: time.Now()})
	if _, ok := s.Take("a1"); !ok {
		t.Fatal("expected to take a1")
	}
	if _, ok := s.Take("a1"); ok {
		t.Fatal("a1 should be gone after being taken (no replay)")
	}
	// Expired entry is refused.
	s.Add(PendingAction{ActionID: "old", ToolName: "create_note", CreatedAt: time.Now().Add(-11 * time.Minute)})
	if _, ok := s.Take("old"); ok {
		t.Fatal("expired entry should not be takeable")
	}
}

func TestRegistry_Names(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(&stubTool{name: "b"})
	r.MustRegister(&stubTool{name: "a"})
	names := r.Names()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("got %v, want [a b]", names)
	}
}
