package contradict

import (
	"sort"

	"github.com/hygur/sidecar/internal/store"
)

// Angle A-2a — the self-model noticing its own evolution. Where the user decided
// the SAME (entity, attribute) more than once with a DIVERGENT value, the later
// decision UPDATES the earlier one: a position revisited. Deterministic, no LLM —
// it reads the claims G4 already cached on each decision and orders them by the date
// each was decided. Unlike DetectDecisionConflicts (decision vs fresh capture), both
// sides here are the user's OWN confirmed decisions.

// DecisionEvolution records one such update: the same (entity, attribute) decided
// again with a different value. SuccessorID is the later decision (the current
// position); PredecessorID the earlier one it supersedes.
type DecisionEvolution struct {
	PredecessorID string `json:"predecessor_id"`
	SuccessorID   string `json:"successor_id"`
	Entity        string `json:"entity"`
	Attribute     string `json:"attribute"`
	OldValue      string `json:"old_value"`
	NewValue      string `json:"new_value"`
}

// DetectDecisionEvolution finds, among the user's own decisions, where a position
// was revisited. decidedAt maps a decision's content_id → the date it was decided
// (RFC3339); a decision without a date can't be ordered and is skipped. Decisions
// must already carry extracted_claims in metadata. Within each (entity, attribute)
// group, decisions are ordered oldest-first and each transition to a different value
// emits one evolution (predecessor → successor). Reaffirming the same value, or
// deciding a different attribute, emits nothing.
func DetectDecisionEvolution(decisions []*store.KnowledgeItem, decidedAt map[string]string) []DecisionEvolution {
	type dref struct{ id, value, at string }
	groups := map[string][]dref{}
	var order []string // (entity,attribute) keys, first-seen order — deterministic output
	meta := map[string][2]string{}
	for _, dec := range decisions {
		if dec == nil {
			continue
		}
		at := decidedAt[dec.ContentID]
		if at == "" {
			continue // can't place it on the timeline
		}
		for _, c := range claimsFromMetadata(dec.Metadata) {
			ea := normKey(c.Entity) + "\x1f" + normKey(c.Attribute)
			if _, ok := groups[ea]; !ok {
				order = append(order, ea)
				meta[ea] = [2]string{c.Entity, c.Attribute}
			}
			groups[ea] = append(groups[ea], dref{id: dec.ContentID, value: c.Value, at: at})
		}
	}

	var out []DecisionEvolution
	for _, ea := range order {
		refs := groups[ea]
		if len(refs) < 2 {
			continue
		}
		sort.SliceStable(refs, func(i, j int) bool {
			if refs[i].at == refs[j].at {
				return refs[i].id < refs[j].id
			}
			return refs[i].at < refs[j].at // RFC3339 sorts lexicographically
		})
		for k := 1; k < len(refs); k++ {
			a, b := refs[k-1], refs[k]
			if a.id == b.id {
				continue // two claims from one decision — not an evolution
			}
			if normKey(a.value) == normKey(b.value) || normKey(b.value) == "" {
				continue // reaffirmed, or no new value to record
			}
			out = append(out, DecisionEvolution{
				PredecessorID: a.id, SuccessorID: b.id,
				Entity: meta[ea][0], Attribute: meta[ea][1],
				OldValue: a.value, NewValue: b.value,
			})
		}
	}
	return out
}
