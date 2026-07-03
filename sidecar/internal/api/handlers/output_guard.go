package handlers

import (
	"github.com/hygur/sidecar/internal/identifier"
	"github.com/hygur/sidecar/internal/labelfact"
	"github.com/hygur/sidecar/internal/retrieval"
)

// unverifiedIdentifierDecline is the honest reply the output guard substitutes when the LLM's
// answer carries an identifier-grade value the engine never determined. English (UI strings are
// English); it asserts nothing and shows no number.
const unverifiedIdentifierDecline = "I don't have a verified value for that."

// determinedValueSet reduces the query's engine-determined facts (the AssembleQueryFacts result)
// to the SET of normalized identifier values that are allowed to appear in the answer prose —
// every identity value + its raw form + every active-claim value, each run through
// identifier.Normalize so a cosmetically-formatted value in the prose ("BE 1021.xxx.xxx") matches
// the canonical determined value. This is the membership oracle the guard compares against.
func determinedValueSet(subjects []retrieval.DeterminedFacts) map[string]bool {
	set := make(map[string]bool)
	add := func(s string) {
		if n := identifier.Normalize(s); n != "" {
			set[n] = true
		}
	}
	for _, s := range subjects {
		for _, id := range s.Identity {
			add(id.Value)
			add(id.Raw)
		}
		for _, c := range s.Claims {
			add(c.Value)
		}
	}
	return set
}

// addToolDeterminedValue unions an engine-verified value produced by the lookup_identifier TOOL
// (rendered as a determined_answer card, tier high/med) into the guard's membership set. The value
// is normalized exactly like determinedValueSet normalizes the LAYER values (identifier.Normalize),
// so a cosmetically-formatted form in the prose still matches. It initializes the set when nil —
// the determined-facts LAYER may have surfaced nothing for this query even though the TOOL resolved
// a value (the recall-floor path) — and ignores empty/undecodable values. This ONLY ADDS to the
// verified set; it never weakens the guard, so a value the engine determined by NEITHER path stays
// unverified and still declines.
func addToolDeterminedValue(set map[string]bool, value string) map[string]bool {
	n := identifier.Normalize(value)
	if n == "" {
		return set
	}
	if set == nil {
		set = make(map[string]bool)
	}
	set[n] = true
	return set
}

// guardAnswer is the deterministic OUTPUT guard (the P=0 backstop). After the LLM produces the
// answer text, it deterministically scans for identifier-grade values by REUSING the extractor's
// value-grade detector (labelfact.IdentifierGradeValues — a value-SHAPE test: ≥9-digit run, IBAN
// shape, or mixed alnum code; NOT amounts, dates, counts, percentages or years) and compares each
// against the engine-determined value set (set membership). This is a comparison, NOT a per-type
// keyword list and NOT type routing; no LLM, no DB.
//
// Any identifier-grade value NOT in the determined set is UNVERIFIED — an LLM that WROTE it has
// P(hallucination) > 0, so it must never reach the user. Because that value is the payload the
// surrounding sentence was built to deliver, a partial strip cannot deterministically guarantee
// the remaining prose is not still misleading, so the guard FAILS CLOSED: on any unverified
// identifier-grade value it replaces the WHOLE answer with an honest decline. Verified values
// (also rendered by the determined_answer card) and ordinary numbers below the value-grade floor
// pass through untouched. Returns the (possibly replaced) answer and whether it declined.
func guardAnswer(answer string, determined map[string]bool) (string, bool) {
	for _, v := range labelfact.IdentifierGradeValues(answer) {
		if !determined[v.Norm] {
			return unverifiedIdentifierDecline, true
		}
	}
	return answer, false
}
