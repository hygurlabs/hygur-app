package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hygur/sidecar/internal/store"
)

// FindDecisionsTool exposes the user's logged decisions to the chat path. Unlike
// memories and the agenda, decisions are NOT pre-injected into the turn, so this
// is the only way the assistant can answer "what did I decide about X?". Pure
// read over store.ListDecisions — deterministic, no LLM, no side effect.
type FindDecisionsTool struct {
	store *store.DB
}

// NewFindDecisionsTool creates a FindDecisionsTool backed by the given store.
func NewFindDecisionsTool(db *store.DB) *FindDecisionsTool {
	return &FindDecisionsTool{store: db}
}

// Name implements tools.Tool.
func (t *FindDecisionsTool) Name() string { return "find_decisions" }

// Description implements tools.Tool.
func (t *FindDecisionsTool) Description() string {
	return "Find the user's previously logged decisions and their rationale, optionally about a topic."
}

// ParameterSchema implements tools.Tool. Both fields are optional: no query
// returns every decision; status narrows to one lifecycle state.
func (t *FindDecisionsTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Optional topic to match against the decision statement and rationale.",
			},
			"status": map[string]any{
				"type":        "string",
				"description": "Optional lifecycle filter.",
				"enum":        []string{store.DecisionStanding, store.DecisionProposed, store.DecisionSuperseded},
			},
		},
	}
}

// Execute implements tools.Tool: list decisions (optionally by status), then
// substring-filter by query in memory. Read-only.
func (t *FindDecisionsTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Query  string `json:"query"`
		Status string `json:"status"`
	}
	// The LLM may call with no/empty arguments to list everything.
	if len(args) > 0 {
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}

	// Only honour a known status; anything else is treated as "all".
	status := ""
	switch req.Status {
	case store.DecisionStanding, store.DecisionProposed, store.DecisionSuperseded:
		status = req.Status
	}

	decisions, err := t.store.ListDecisions(ctx, "", status)
	if err != nil {
		return nil, fmt.Errorf("list decisions failed: %w", err)
	}

	q := strings.ToLower(strings.TrimSpace(req.Query))
	type decOut struct {
		Statement string   `json:"statement"`
		Rationale string   `json:"rationale,omitempty"`
		Status    string   `json:"status"`
		DecidedOn string   `json:"decided_on,omitempty"`
		Sources   []string `json:"sources,omitempty"`
	}
	out := make([]decOut, 0, len(decisions))
	for _, d := range decisions {
		if q != "" && !strings.Contains(strings.ToLower(d.Statement+" "+d.Rationale), q) {
			continue
		}
		out = append(out, decOut{
			Statement: d.Statement,
			Rationale: d.Rationale,
			Status:    d.Status,
			DecidedOn: d.DecidedOn,
			Sources:   d.SourceRefs,
		})
	}
	return json.Marshal(map[string]any{"decisions": out})
}
