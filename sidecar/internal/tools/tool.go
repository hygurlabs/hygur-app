package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
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

	// SideEffect reports whether calling this tool mutates state the user would
	// want to approve first (creating a note, sending, etc.). When true, the
	// registry does NOT execute the tool directly — it registers a pending
	// action and returns a confirmation envelope (WP3, Décision 2). Read-only
	// tools embed NoSideEffect to inherit false without restating it.
	SideEffect() bool
}

// NoSideEffect is embedded by read-only tools so they satisfy the SideEffect()
// half of the Tool interface with a false default — existing tools stay
// untouched apart from the one-line embed. A tool that DOES mutate state
// overrides SideEffect() to return true (see CreateNoteTool).
type NoSideEffect struct{}

// SideEffect reports false — the default for pure/read-only tools.
func (NoSideEffect) SideEffect() bool { return false }

// PreviewProvider is the optional interface a SideEffect tool implements to turn
// its raw args into a human-readable one-line summary for the confirmation card
// (e.g. `create_note` → the note title). Tools that don't implement it fall back
// to a generic preview built from the tool name.
type PreviewProvider interface {
	Preview(args json.RawMessage) string
}

// ErrUnknownTool is returned by Registry.Execute when the LLM names a tool
// that isn't registered. Wrapped with the requested name in the error string.
var ErrUnknownTool = errors.New("unknown tool")

// ErrConfirmationRequired is returned by Registry.Execute when a SideEffect tool
// is invoked but no pending-action gate is configured. Fail-closed: the tool is
// never executed in that state.
var ErrConfirmationRequired = errors.New("side-effect tool requires confirmation gate")

// Registry tracks the tools available to a chat session. Safe for concurrent
// use — the chat handler reads the registry on every request while
// registration happens once at startup.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	// pending gates SideEffect tools: when set, Execute registers a pending
	// action instead of running the tool. nil in tests that don't exercise the
	// gate — in that case Execute fail-CLOSES on a SideEffect tool rather than
	// running it silently.
	pending *PendingActionStore
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// SetPendingStore wires the confirmation gate. After this, any SideEffect tool
// invoked through Execute is held pending in the store (and NOT executed) until
// the user confirms via ExecuteConfirmed.
func (r *Registry) SetPendingStore(p *PendingActionStore) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = p
}

// IsSideEffect reports whether the named tool declares a side effect. False for
// unknown tools. Lets the chat handler decide whether a tool result is the
// pending-confirmation envelope (so it can surface the SSE card).
func (r *Registry) IsSideEffect(name string) bool {
	t, ok := r.Lookup(name)
	return ok && t.SideEffect()
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

// Execute runs the named tool with the supplied raw arguments — UNLESS the tool
// declares a side effect, in which case it is NOT executed. Instead the registry
// records a pending action and returns a PendingResult envelope (marshaled) for
// the model + client to surface a confirmation card; the tool runs later, once,
// via ExecuteConfirmed. Errors:
//   - ErrUnknownTool (wrapped) when name isn't registered
//   - ErrConfirmationRequired when a SideEffect tool is invoked without a
//     configured pending store (fail-closed: never run it silently)
//   - the tool's own error otherwise
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	t, ok := r.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, name)
	}
	if t.SideEffect() {
		r.mu.RLock()
		pending := r.pending
		r.mu.RUnlock()
		// Fail-closed: a side-effecting tool must never execute without the gate.
		if pending == nil {
			return nil, fmt.Errorf("%w: %s", ErrConfirmationRequired, name)
		}
		actionID := uuid.NewString()
		preview := previewFor(t, name, args)
		pending.Add(PendingAction{
			ActionID:  actionID,
			ToolName:  name,
			Args:      args,
			Preview:   preview,
			CreatedAt: time.Now(),
		})
		return json.Marshal(PendingResult{Pending: true, ActionID: actionID, Preview: preview})
	}
	return t.Execute(ctx, args)
}

// ExecuteConfirmed runs the named tool directly, bypassing the SideEffect gate.
// This is the confirm path: POST /actions/{action_id}/confirm calls it AFTER the
// user approved the pending action. It performs no gating of its own — the
// caller is responsible for having taken (and thus validated) the pending entry.
func (r *Registry) ExecuteConfirmed(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	t, ok := r.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, name)
	}
	return t.Execute(ctx, args)
}

// previewFor builds the human-readable confirmation summary. A tool that
// implements PreviewProvider supplies its own; otherwise we fall back to a
// generic line naming the tool.
func previewFor(t Tool, name string, args json.RawMessage) string {
	if p, ok := t.(PreviewProvider); ok {
		if s := p.Preview(args); s != "" {
			return s
		}
	}
	return fmt.Sprintf("Run %s", name)
}
