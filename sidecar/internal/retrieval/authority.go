package retrieval

import (
	"context"

	"github.com/hygur/sidecar/internal/store"
)

// AuthorityTier is the kind-of-claim ladder used to rank what "fait foi": a
// user-confirmed decision outranks a derived artifact, which outranks a raw
// capture. A proposed decision is a CANDIDATE — detected but not yet confirmed,
// so it is NOT authoritative until the user validates it.
type AuthorityTier string

const (
	TierConfirmed AuthorityTier = "confirmed" // standing decision (user-confirmed)
	TierCandidate AuthorityTier = "candidate" // proposed decision (awaiting confirmation)
	TierCapture   AuthorityTier = "capture"   // raw mail / file / note / event
	// TierDerived (chronicle / reconciled fact) is deferred until chronicle
	// storage is wired into the join — M1b.
)

// Validity is whether the claim still holds. M1a derives it from the decision
// status only; capture-level supersession/conflict (from reconcile verdicts) is
// M1b.
type Validity string

const (
	ValidityCurrent    Validity = "current"
	ValiditySuperseded Validity = "superseded"
	// ValidityConflicted (an unresolved reconcile conflict) is M1b — it is the
	// guardrail signal that must SURFACE rather than demote.
)

// classifyAuthority maps a result's (source_type, decision status) to its tier
// and validity. Pure + deterministic — no model, no clock. decisionStatus is the
// empty string when the content carries no decision_attrs row (a plain capture).
func classifyAuthority(sourceType, decisionStatus string) (AuthorityTier, Validity) {
	if sourceType == store.SourceTypeDecision {
		switch decisionStatus {
		case store.DecisionSuperseded:
			return TierConfirmed, ValiditySuperseded
		case store.DecisionProposed:
			return TierCandidate, ValidityCurrent
		case store.DecisionStanding:
			return TierConfirmed, ValidityCurrent
		default:
			// A decision item with no attrs row defaults to standing (matches
			// UpsertDecisionAttrs' blank-status default).
			return TierConfirmed, ValidityCurrent
		}
	}
	return TierCapture, ValidityCurrent
}

// annotateAuthority tags each result's Tier/Validity from the decision graph in a
// single batch query. Annotate-only: it never reorders results (that is M2). It
// fails open — on a store error the results stay unannotated and the
// relevance-pure path is unaffected.
func (us *UnifiedSearcher) annotateAuthority(ctx context.Context, results []UnifiedResult) {
	if len(results) == 0 {
		return
	}
	ids := make([]string, 0, len(results))
	for i := range results {
		ids = append(ids, results[i].ContentID)
	}
	status, err := us.store.DecisionStatuses(ctx, ids)
	if err != nil {
		return
	}
	for i := range results {
		results[i].Tier, results[i].Validity = classifyAuthority(results[i].SourceType, status[results[i].ContentID])
	}
}
