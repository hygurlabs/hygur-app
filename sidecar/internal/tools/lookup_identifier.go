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
	// Read-only: embeds NoSideEffect so it is never gated by the confirmation flow.
	NoSideEffect
	store fact.Store
	owner *identity.Matcher // first-class owner matcher (may be nil)
	// ownerSubject is a representative configured owner name. It is what a FIRST-PERSON subject
	// ("moi"/"mon"/"my") resolves to — the SAME anchor the determined-facts layer uses — so the
	// owner's own identifiers resolve instead of the tool declining an un-mappable pronoun. Empty
	// (owner unconfigured) makes first-person an honest decline rather than a guess.
	ownerSubject string
}

// NewLookupIdentifierTool builds the tool over the given store (a *store.DB), the owner matcher,
// and a representative owner name (ownerSubject) so the owner's OWN identifiers resolve via the
// owner anchor + dominance — including when the user names themselves in the first person.
func NewLookupIdentifierTool(s fact.Store, owner *identity.Matcher, ownerSubject string) *LookupIdentifierTool {
	return &LookupIdentifierTool{store: s, owner: owner, ownerSubject: strings.TrimSpace(ownerSubject)}
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

// LookupResponse is what the model receives — and, verbatim, what the chat handler turns into
// the authoritative `determined_answer` render (value + label + subject + confidence + sources).
// The engine PRODUCES this; the model only voices it. `guidance` tells the model how to phrase
// the answer at the reported confidence — the tier is authoritative, the model must not upgrade
// it. Exported so the handler can decode the tool result and render the value cut-LLM-safe.
type LookupResponse struct {
	Type     string        `json:"type"`
	Label    string        `json:"label,omitempty"`   // the identifier as the user named it (render label)
	Subject  string        `json:"subject,omitempty"` // whose identifier ("you" for the owner)
	Value    string        `json:"value,omitempty"`
	Tier     fact.Tier     `json:"confidence"`
	Reason   string        `json:"reason,omitempty"` // decline code (mirrors fact.Result.Reason)
	Guidance string        `json:"guidance"`
	Sources  []fact.Source `json:"sources,omitempty"`
}

// firstPersonSubjects are the pronouns/possessives that denote the app user (the owner). The
// TRIGGER is the model's own language understanding: when it extracts a first-person subject for
// an identifier question ("mon numéro de TVA" → entity "moi"/"mon"), the tool resolves it to the
// OWNER — reusing the same owner anchor the determined-facts layer uses — instead of declining a
// pronoun that maps to no person norm. NOT a keyword classifier of the query: this only maps an
// already-extracted first-person SUBJECT to "self".
var firstPersonSubjects = map[string]bool{
	"moi": true, "mon": true, "ma": true, "mes": true, "je": true, "me": true,
	"moi-même": true, "moi-meme": true, "mien": true, "mienne": true, "miens": true, "miennes": true,
	"my": true, "mine": true, "myself": true, "i": true,
}

// isFirstPersonSubject reports whether an extracted entity denotes the user themselves.
func isFirstPersonSubject(entity string) bool {
	return firstPersonSubjects[strings.ToLower(strings.TrimSpace(entity))]
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

	// First-person subject resolution. The user IS the owner, so a first-person subject
	// ("moi"/"mon"/"my") resolves to the configured owner anchor — the SAME subject the
	// determined-facts layer uses — so "quel est mon numéro de TVA" yields the owner's determined
	// value instead of declining an un-mappable pronoun. subjectLabel is what the render shows.
	resolveQuery := a.Entity
	subjectLabel := a.Entity
	if isFirstPersonSubject(a.Entity) {
		subjectLabel = "you"
		if t.ownerSubject == "" {
			out := LookupResponse{Type: a.Type, Label: a.Type, Subject: subjectLabel, Tier: fact.TierNone,
				Guidance: "Do NOT state a value — the owner is not configured, so a first-person identifier cannot be resolved. Say you don't have a verified value."}
			return json.Marshal(out)
		}
		resolveQuery = t.ownerSubject
	}

	// Normalize the raw label to its canonical id_type (label-EXACT): "DUNS" → id_duns, "vat" →
	// enterprise_number. The lookup then queries ONLY that type — asking for a DUNS can never
	// return an enterprise_number. An unusable label declines rather than guessing.
	idType := labelfact.NormalizeLabel(a.Type)
	if idType == "" {
		out := LookupResponse{Type: a.Type, Label: a.Type, Subject: subjectLabel, Tier: fact.TierNone,
			Guidance: "Do NOT state a value — the identifier label was not understood. Ask the user to name the identifier more precisely."}
		return json.Marshal(out)
	}
	res, err := fact.LookupIdentifier(ctx, t.store, contradict.NormKey(resolveQuery), idType, time.Now(), t.owner)
	if err != nil {
		return nil, err
	}

	out := LookupResponse{Type: res.Type, Label: a.Type, Subject: subjectLabel, Tier: res.Tier, Reason: res.Reason, Sources: res.Sources}
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
		case fact.ReasonUncorroborated:
			out.Guidance = "Several candidate values compete for this identifier and none is corroborated enough to trust (the best is backed by a single document — likely a coincidental match, not the real number). Do NOT state a value — say you don't have a verified one and offer the source(s) to check."
		default:
			out.Guidance = "Do NOT state a value — you could not confirm a reliable one. Say so, and offer the source document(s) for the user to check themselves."
		}
	}
	return json.Marshal(out)
}
