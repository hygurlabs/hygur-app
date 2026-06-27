package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Tool is the canonical contract every callable tool implements so the chat
// path can dispatch LLM tool-calls uniformly. Existing handler-style tools
// (CreateNoteTool, SearchTool, …) are registered through small adapters that
// wrap their typed Run() method behind Execute(); the chat loop never has to
// know which concrete tool it's invoking.
type Tool interface {
	// Name is the function-call identifier the LLM sees. Must be unique
	// within a Registry. Stable across versions — the LLM is trained
	// against names.
	Name() string

	// Description tells the LLM when to call this tool. Short, action-
	// oriented; OpenAI's docs suggest <80 chars. Returned as-is in the
	// `tools[].function.description` field.
	Description() string

	// ParameterSchema is the JSON Schema for the arguments object. Mirror
	// the shape OpenAI expects: typically
	//   {"type": "object", "properties": {...}, "required": [...]}.
	ParameterSchema() map[string]any

	// Execute runs the tool. args is the raw JSON object the LLM emitted
	// (already validated against ParameterSchema by the LLM, but the tool
	// is responsible for re-validating before any side effect). The
	// returned bytes become the body of a `role:"tool"` message that gets
	// fed back to the LLM in the next turn — typically a small JSON object.
	Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error)
}

// ErrUnknownTool is returned by Registry.Execute when the LLM names a tool
// that isn't registered. Wrapped with the requested name in the error string.
var ErrUnknownTool = errors.New("unknown tool")

// Registry tracks the tools available to a chat session. Safe for concurrent
// use — the chat handler reads the registry on every request while
// registration happens once at startup.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds t to the registry. Returns an error when the name is empty
// or already taken — silent overwrites would mask collisions between modules.
func (r *Registry) Register(t Tool) error {
	if t == nil {
		return errors.New("tool must not be nil")
	}
	name := t.Name()
	if name == "" {
		return errors.New("tool name must not be empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool already registered: %s", name)
	}
	r.tools[name] = t
	return nil
}

// MustRegister is Register's panicking sibling, intended for startup wiring
// where a failure is a developer bug rather than a runtime condition.
func (r *Registry) MustRegister(t Tool) {
	if err := r.Register(t); err != nil {
		panic(fmt.Sprintf("tools: %v", err))
	}
}

// Lookup returns the tool registered under name. The boolean is false when
// the name isn't known.
func (r *Registry) Lookup(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Names returns the registered tool names sorted alphabetically — stable
// ordering keeps the OpenAI request payload deterministic across runs.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// OpenAIDefinitions returns the tool list shaped for the `tools` field of an
// OpenAI chat completion request. Returns nil when no tools are registered
// so callers can omit the field entirely (some servers reject empty arrays).
func (r *Registry) OpenAIDefinitions() []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	defs := make([]map[string]any, 0, len(names))
	for _, name := range names {
		t := r.tools[name]
		defs = append(defs, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name(),
				"description": t.Description(),
				"parameters":  t.ParameterSchema(),
			},
		})
	}
	return defs
}

// Execute runs the named tool with the supplied raw arguments. Errors:
//   - ErrUnknownTool (wrapped) when name isn't registered
//   - the tool's own error otherwise
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	t, ok := r.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, name)
	}
	return t.Execute(ctx, args)
}
