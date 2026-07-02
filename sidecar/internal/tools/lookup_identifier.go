package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/fact"
)

// LookupIdentifierTool exposes the deterministic (entity, identifier-type) → value lookup to
// the chat agent. The VALUE comes from the psyché's typed-identifier graph + proximity, never
// the model's memory; the model only voices it, at the confidence the tool reports. This is
// how "what is X's national number?" gets a grounded answer instead of a plausible guess.
type LookupIdentifierTool struct {
	store fact.Store
}

// NewLookupIdentifierTool builds the tool over the given store (a *store.DB).
func NewLookupIdentifierTool(s fact.Store) *LookupIdentifierTool {
	return &LookupIdentifierTool{store: s}
}

func (t *LookupIdentifierTool) Name() string { return "lookup_identifier" }

func (t *LookupIdentifierTool) Description() string {
	return "Get a person or org's exact labeled identifier (national number, VAT/enterprise number, IBAN). Use for 'what is X's national number / IBAN / VAT'."
}

func (t *LookupIdentifierTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"entity": map[string]any{
				"type":        "string",
				"description": "The person or organization the identifier belongs to (a name).",
			},
			"type": map[string]any{
				"type":        "string",
				"enum":        []string{"national_number", "enterprise_number", "iban"},
				"description": "Which identifier: national_number (numéro national/NISS), enterprise_number (TVA/BCE/KBO), or iban.",
			},
		},
		"required": []string{"entity", "type"},
	}
}

type lookupArgs struct {
	Entity string `json:"entity"`
	Type   string `json:"type"`
}

// lookupResponse is what the model receives. `guidance` tells it how to phrase the answer at
// the reported confidence — the tier is authoritative, the model must not upgrade it.
type lookupResponse struct {
	Type     string        `json:"type"`
	Value    string        `json:"value,omitempty"`
	Tier     fact.Tier     `json:"confidence"`
	Guidance string        `json:"guidance"`
	Sources  []fact.Source `json:"sources,omitempty"`
}

func (t *LookupIdentifierTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var a lookupArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	a.Entity = strings.TrimSpace(a.Entity)
	a.Type = strings.TrimSpace(a.Type)
	if a.Entity == "" || a.Type == "" {
		return nil, fmt.Errorf("entity and type are required")
	}
	res, err := fact.LookupIdentifier(ctx, t.store, contradict.NormKey(a.Entity), a.Type, time.Now())
	if err != nil {
		return nil, err
	}

	out := lookupResponse{Type: res.Type, Tier: res.Tier, Sources: res.Sources}
	switch res.Tier {
	case fact.TierHigh:
		out.Value = res.Value
		out.Guidance = "State this value plainly as the answer, and cite the source document(s)."
	case fact.TierMed:
		out.Value = res.Value
		out.Guidance = "Give this value but explicitly flag that you are NOT certain (e.g. 'I'm not sure, but…'), and cite the source so the user can verify."
	default: // TierNone
		switch res.Reason {
		case fact.ReasonAmbiguousSubject:
			out.Guidance = "The name matches SEVERAL different people. Do NOT give any number — ask the user which specific person they mean (e.g. by full name)."
		case fact.ReasonAmbiguousOwner:
			out.Guidance = "This value is associated with MORE THAN ONE person, so you cannot attribute it to the one asked about. Do NOT state it as theirs — say the ownership is unclear and offer the source(s) to check."
		default:
			out.Guidance = "Do NOT state a value — you could not confirm a reliable one. Say so, and offer the source document(s) for the user to check themselves."
		}
	}
	return json.Marshal(out)
}
