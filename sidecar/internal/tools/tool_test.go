package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// stubTool is a minimal Tool used only by these tests.
type stubTool struct {
	name string
	desc string
	exec func(ctx context.Context, args json.RawMessage) (json.RawMessage, error)
}

func (s *stubTool) Name() string                 { return s.name }
func (s *stubTool) Description() string          { return s.desc }
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

func TestRegistry_Names(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(&stubTool{name: "b"})
	r.MustRegister(&stubTool{name: "a"})
	names := r.Names()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("got %v, want [a b]", names)
	}
}
