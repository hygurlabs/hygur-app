package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/fact"
	"github.com/hygur/sidecar/internal/identity"
	"github.com/hygur/sidecar/internal/labelfact"
)

// LookupIdentifierTool exposes the deterministic (entity, identifier-type) → value lookup to
// the chat agent. The VALUE comes from the psyché's typed-identifier graph + proximity, never
// the model's memory; the model only voices it, at the confidence the tool reports. This is
// how "what is X's national number?" gets a grounded answer instead of a plausible guess.
type LookupIdentifierTool struct {
	store fact.Store
	owner *identity.Matcher // first-class owner matcher (may be nil)
}

// NewLookupIdentifierTool builds the tool over the given store (a *store.DB) and the owner
// matcher, so the owner's OWN identifiers resolve via the owner anchor + dominance.
func NewLookupIdentifierTool(s fact.Store, owner *identity.Matcher) *LookupIdentifierTool {
	return &LookupIdentifierTool{store: s, owner: owner}
}

func (t *LookupIdentifierTool) Name() string { return "lookup_identifier" }

func (t *LookupIdentifierTool) Description() string {
	return "Get a person or org's exact labeled identifier by its label — a national number, VAT/enterprise number, IBAN, or ANY other labeled identifier (DUNS, SIRET, EIN, a client/reference number…). Use for 'what is X's national number / IBAN / VAT / DUNS / …'."
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
				"description": "The identifier's LABEL, exactly as the user names it — e.g. 'national number', 'NISS', 'VAT', 'TVA', 'IBAN', 'DUNS', 'SIRET', 'EIN', 'client number'. Pass the raw label; the lookup normalizes it and returns ONLY that identifier (never a different type).",
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
	// Normalize the raw label to its canonical id_type (label-EXACT): "DUNS" → id_duns, "vat" →
	// enterprise_number. The lookup then queries ONLY that type — asking for a DUNS can never
	// return an enterprise_number. An unusable label declines rather than guessing.
	idType := labelfact.NormalizeLabel(a.Type)
	if idType == "" {
		out := lookupResponse{Type: a.Type, Tier: fact.TierNone,
			Guidance: "Do NOT state a value — the identifier label was not understood. Ask the user to name the identifier more precisely."}
		return json.Marshal(out)
	}
	res, err := fact.LookupIdentifier(ctx, t.store, contradict.NormKey(a.Entity), idType, time.Now(), t.owner)
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
