package retrieval

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
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
	// storage is wired into the join — M1b+.
)

// Validity is whether the claim still holds. M1a derives it from the decision
// status; M1b overlays capture-level supersession/conflict from the reconcile
// verdicts (see annotateConflictValidity).
type Validity string

const (
	ValidityCurrent    Validity = "current"
	ValiditySuperseded Validity = "superseded"
	// ValidityConflicted is an unresolved reconcile conflict — the guardrail
	// signal that must SURFACE (M2) rather than be silently demoted.
	ValidityConflicted Validity = "conflicted"
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

// annotateAuthority tags each result's Tier/Validity from the decision graph
// (pass 1) and overlays capture-level validity from the cached reconciled
// conflicts (pass 2). Annotate-only: it never reorders results (that is M2). Both
// passes fail open — on a store error the results stay (partly) unannotated and
// the relevance-pure path is unaffected.
func (us *UnifiedSearcher) annotateAuthority(ctx context.Context, results []UnifiedResult) {
	if len(results) == 0 {
		return
	}
	ids := make([]string, 0, len(results))
	for i := range results {
		ids = append(ids, results[i].ContentID)
	}
	// Pass 1 — tier/validity from the decision graph.
	if status, err := us.store.DecisionStatuses(ctx, ids); err == nil {
		for i := range results {
			results[i].Tier, results[i].Validity = classifyAuthority(results[i].SourceType, status[results[i].ContentID])
		}
	}
	// Pass 2 — capture-level supersession/conflict from the reconcile cache.
	us.annotateConflictValidity(ctx, results)
}

// annotateConflictValidity overlays superseded/conflicted onto CAPTURE-tier
// results that are members of a cached reconciled conflict. M1b scope:
// capture↔capture (the conflict cache is built over claims in mail/notes);
// decision-vs-capture is a later guardrail. Fail-open; never touches a confirmed
// decision.
func (us *UnifiedSearcher) annotateConflictValidity(ctx context.Context, results []UnifiedResult) {
	js, _, _, found, err := us.store.GetContradictionCache(ctx, "")
	if err != nil || !found || js == "" {
		return
	}
	var conflicts []contradict.ReconciledConflict
	if err := json.Unmarshal([]byte(js), &conflicts); err != nil {
		return
	}
	cv := conflictValidity(conflicts)
	if len(cv) == 0 {
		return
	}
	for i := range results {
		if results[i].Tier != TierCapture {
			continue
		}
		if v, ok := cv[results[i].ContentID]; ok {
			results[i].Validity = v
		}
	}
}

// conflictValidity maps content_id → capture-level validity from reconciled
// conflicts. "conflict" → every member is conflicted (surface). "supersedes" →
// the latest-asserted member stays current; earlier members are superseded.
// Dismissed and "none"/empty verdicts contribute nothing. Conflicted dominates
// superseded (the stronger "needs attention" signal) when an id appears twice.
func conflictValidity(conflicts []contradict.ReconciledConflict) map[string]Validity {
	out := map[string]Validity{}
	set := func(id string, v Validity) {
		if id == "" || out[id] == ValidityConflicted {
			return
		}
		out[id] = v
	}
	for _, c := range conflicts {
		if c.Dismissed || len(c.Members) < 2 {
			continue
		}
		switch c.Verdict.Kind {
		case "conflict":
			for _, m := range c.Members {
				set(m.SourceID, ValidityConflicted)
			}
		case "supersedes":
			cur := latestMember(c.Members)
			for i, m := range c.Members {
				if i != cur {
					set(m.SourceID, ValiditySuperseded)
				}
			}
		}
	}
	return out
}

// latestMember returns the index of the member with the most recent AssertedAt.
func latestMember(members []contradict.ClaimRef) int {
	best := 0
	for i := 1; i < len(members); i++ {
		if assertedAfter(members[i].AssertedAt, members[best].AssertedAt) {
			best = i
		}
	}
	return best
}

// assertedAfter reports whether a is later than b. It parses common date layouts
// and compares as time; if either won't parse, it falls back to lexical order
// (ISO timestamps sort chronologically).
func assertedAfter(a, b string) bool {
	ta, oka := parseAsserted(a)
	tb, okb := parseAsserted(b)
	if oka && okb {
		return ta.After(tb)
	}
	return a > b
}

func parseAsserted(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ─── M2: authority re-score ──────────────────────────────────────────────────

// AuthorityWeights are the multipliers applied to a result's relevance score by
// the authority re-score. A weighted-feature shape (not a fixed formula) so future
// signals (consequence/angle/attention) can be added without rewriting. All 1.0 =
// identity (no reordering) — the safe default when no authority signal is present.
type AuthorityWeights struct {
	Confirmed  float64 // confirmed decision, current → boost (this is what "fait foi")
	Candidate  float64 // proposed decision → neutral (not authoritative until confirmed)
	Capture    float64 // raw capture, current → baseline
	Superseded float64 // superseded (decision or capture) → demote (the resolved loser)
	Conflicted float64 // unresolved conflict → SURFACE, never bury (guardrail G4)
}

// DefaultAuthorityWeights boosts the confirmed-and-current, demotes the resolved
// loser, and keeps an unresolved conflict visible (slightly above baseline so the
// user sees the tension rather than it sinking under the "winning" side).
func DefaultAuthorityWeights() AuthorityWeights {
	return AuthorityWeights{Confirmed: 1.6, Candidate: 1.0, Capture: 1.0, Superseded: 0.3, Conflicted: 1.15}
}

// multiplier picks the weight for a result. Validity is checked first: a conflict
// must surface and a superseded item must demote regardless of tier (a superseded
// *decision* still loses). Otherwise the current item is weighted by its tier.
func (w AuthorityWeights) multiplier(tier AuthorityTier, v Validity) float64 {
	switch v {
	case ValidityConflicted:
		return w.Conflicted
	case ValiditySuperseded:
		return w.Superseded
	}
	switch tier {
	case TierConfirmed:
		return w.Confirmed
	case TierCandidate:
		return w.Candidate
	default:
		return w.Capture
	}
}

// applyAuthorityRescore multiplies each result's score by its authority weight and
// re-sorts (stable, so equal weights preserve the relevance order — when nothing
// carries authority every weight is 1.0 and the order is byte-identical: no
// regression). No-op unless the searcher has authority rerank enabled. Requires
// Tier/Validity to be set first (annotateAuthority).
func (us *UnifiedSearcher) applyAuthorityRescore(results []UnifiedResult) {
	if !us.useAuthorityRerank || len(results) == 0 {
		return
	}
	w := us.authorityWeights
	for i := range results {
		results[i].Score *= w.multiplier(results[i].Tier, results[i].Validity)
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
}
