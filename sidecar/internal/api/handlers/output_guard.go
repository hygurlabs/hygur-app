package handlers

import (
	"strings"

	"github.com/hygur/sidecar/internal/figure"
	"github.com/hygur/sidecar/internal/identifier"
	"github.com/hygur/sidecar/internal/labelfact"
	"github.com/hygur/sidecar/internal/retrieval"
)

// unverifiedIdentifierDecline is the honest reply the output guard substitutes when the LLM's
// answer carries a value in NO source (INVENTÉ). English (UI strings are English); it asserts
// nothing and shows no number.
const unverifiedIdentifierDecline = "I don't have a verified value for that."

// provenanceValueSet is the two-grade membership oracle for ONE provenance side — either D (the
// query's engine-DETERMINED values) or R (the values present in the retrieved UNTRUSTED excerpts).
// The two grades are kept apart because they NORMALIZE differently: an identifier folds every
// separator away (identifier.Normalize("BE 1021.456.789") → "be1021456789") while a monetary
// amount keeps a dot-decimal (figure.NormalizeAmount("7 421,85 €") → "7421.85"), so a single flat
// set would never let a figure and an identifier match their own canonical form. Membership is
// tested per grade against the matching sub-set.
type provenanceValueSet struct {
	id  map[string]bool // identifier-grade norms (identifier.Normalize)
	amt map[string]bool // figure-grade norms (figure.NormalizeAmount / AmountValues)
}

func newProvenanceValueSet() provenanceValueSet {
	return provenanceValueSet{id: map[string]bool{}, amt: map[string]bool{}}
}

func (s *provenanceValueSet) empty() bool { return len(s.id) == 0 && len(s.amt) == 0 }

// addValue folds one TRUSTED string (a determined identifier/claim value) into BOTH grades: its
// identifier norm, and — when it parses as an amount — its amount norm. Adding both forms lets a
// determined value match the answer whichever grade the answer's rendering falls under (a figure
// claim written "7421.85" still matches "7 421,85 €" in the prose). Empty/undecodable → ignored.
func (s *provenanceValueSet) addValue(v string) {
	if n := identifier.Normalize(v); n != "" {
		s.id[n] = true
	}
	if n, ok := figure.NormalizeAmount(v); ok {
		s.amt[n] = true
	}
}

// scanExcerpt folds one UNTRUSTED retrieved excerpt into R using the SAME detectors the guard runs
// on the answer: identifier-grade values (labelfact) and figure-grade amounts (figure). This is
// what makes a value RETROUVÉ — traceable to a document — rather than INVENTÉ.
func (s *provenanceValueSet) scanExcerpt(text string) {
	for _, v := range labelfact.IdentifierGradeValues(text) {
		s.id[v.Norm] = true
	}
	for _, a := range figure.AmountValues(text) {
		s.amt[a.Value] = true
	}
}

// determinedValueSet reduces the query's engine-determined facts (the AssembleQueryFacts result)
// to the provenance set D — the values that are allowed to appear in the answer AS VERIFIED FACT:
// every identity value + its raw form + every active-claim value, each folded into both grades so a
// cosmetically-formatted value in the prose ("BE 1021.xxx.xxx", "7 421,85 €") matches its canonical
// determined value. This is the membership oracle the firewall's DÉTERMINÉ branch compares against.
func determinedValueSet(subjects []retrieval.DeterminedFacts) provenanceValueSet {
	set := newProvenanceValueSet()
	for _, s := range subjects {
		for _, id := range s.Identity {
			set.addValue(id.Value)
			set.addValue(id.Raw)
		}
		for _, c := range s.Claims {
			set.addValue(c.Value)
		}
	}
	return set
}

// retrievedValueSet builds the provenance set R — every identifier/figure value present in the
// UNTRUSTED retrieved excerpts of this turn. A value in R (but not D) is RETROUVÉ: honestly
// traceable to a document, so it is KEPT but MARKED as unverified rather than asserted as fact. A
// value in NEITHER D nor R is INVENTÉ (in no source) and is stripped.
func retrievedValueSet(sources []RAGSource) provenanceValueSet {
	set := newProvenanceValueSet()
	for _, src := range sources {
		if src.Excerpt != "" {
			set.scanExcerpt(src.Excerpt)
		}
		// The mail subject is retrieved untrusted text too and often carries the amount ("Your
		// ChargePoint receipt: 12.50 EUR").
		if src.MailSubject != "" {
			set.scanExcerpt(src.MailSubject)
		}
	}
	return set
}

// addToolDeterminedValue unions an engine-verified value produced by the lookup TOOL (rendered as a
// determined_answer card, tier high/med) into D. The value is folded exactly like determinedValueSet
// folds the LAYER values (both grades), so a cosmetically-formatted form in the prose still matches.
// This ONLY ADDS to the verified set; it never weakens the firewall, so a value the engine
// determined by NEITHER path stays unverified and is classified by its excerpt membership.
func addToolDeterminedValue(set provenanceValueSet, value string) provenanceValueSet {
	if set.id == nil || set.amt == nil {
		set = newProvenanceValueSet()
	}
	set.addValue(value)
	return set
}

// answerValue is one value detected in the answer prose, resolved to its provenance so the firewall
// can enforce per value: its normalized key, the raw substring as written (for the marker), and
// whether it was DETERMINED / RETRIEVED / neither.
type answerValue struct {
	raw        string
	determined bool
	retrieved  bool
}

// classifyAnswerValues detects every identifier- and figure-grade VALUE in the answer (reusing the
// exact extractor detectors — a value-SHAPE test, not a type list) and classifies each by
// PROVENANCE, not by type: in D → DÉTERMINÉ, else in R → RETROUVÉ, else INVENTÉ. Prose numbers with
// no identifier grade and no currency unit (counts, percentages, years, dates) are not detected at
// all, so they are never classified — the calibration is in the detectors.
func classifyAnswerValues(answer string, determined, retrieved provenanceValueSet) []answerValue {
	var out []answerValue
	for _, v := range labelfact.IdentifierGradeValues(answer) {
		out = append(out, answerValue{
			raw:        v.Raw,
			determined: determined.id[v.Norm],
			retrieved:  retrieved.id[v.Norm],
		})
	}
	for _, a := range figure.AmountValues(answer) {
		out = append(out, answerValue{
			raw:        a.Raw,
			determined: determined.amt[a.Value],
			retrieved:  retrieved.amt[a.Value],
		})
	}
	return out
}

// provenanceMarker renders the RETROUVÉ marker appended to an answer that carries retrieved-but-
// unverified values — an inline, always-visible English annotation (it travels with the message
// into the transcript, so provenance is never lost). It lists the distinct raw values so the
// founder SEES which numbers came from a document and are NOT engine-verified. `raws` is non-empty.
func provenanceMarker(raws []string) string {
	seen := map[string]bool{}
	var uniq []string
	for _, r := range raws {
		r = strings.TrimSpace(r)
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		uniq = append(uniq, r)
	}
	list := strings.Join(uniq, ", ")
	if len(uniq) == 1 {
		return "\n\n📄 Note: " + list + " comes from your documents and is not engine-verified — please double-check it against the source."
	}
	return "\n\n📄 Note: the values above (" + list + ") come from your documents and are not engine-verified — please double-check them against the source."
}

// guardAnswer is the deterministic PROVENANCE FIREWALL on voie B (the P=0 backstop). After the LLM
// produces the answer, it deterministically detects every identifier- and figure-grade VALUE in the
// prose and classifies each by PROVENANCE — decided by the ENGINE, not the LLM's wording:
//
//   - DÉTERMINÉ (value ∈ D, the engine-determined set) → KEEP as a verified fact (also on the card).
//   - RETROUVÉ  (value ∈ R, the retrieved untrusted excerpts, but ∉ D) → KEEP but MARK it visibly as
//     unverified / from a document (the safe default: honest in all cases, incl. a legit retrieval
//     the founder wants — a car-charge list). Never asserted as an engine-verified fact.
//   - INVENTÉ   (value in NEITHER D nor R) → the LLM wrote a value in no source: P(hallucination) > 0.
//     Because that value is the payload the surrounding sentence was built to deliver, a partial
//     strip cannot guarantee the remaining prose is not still misleading, so the guard FAILS CLOSED:
//     it replaces the WHOLE answer with an honest decline.
//
// This SUBSUMES the previous guard (INVENTÉ → strip was its only branch) and ADDS RETROUVÉ → mark.
// Ordinary numbers below the value-grade floor and unit-less prose numbers are never detected, so
// they pass through untouched. Returns the (possibly replaced/annotated) answer and whether it
// declined (INVENTÉ path).
func guardAnswer(answer string, determined, retrieved provenanceValueSet) (string, bool) {
	var retrievedRaws []string
	for _, v := range classifyAnswerValues(answer, determined, retrieved) {
		if v.determined {
			continue // verified — keep, no marker
		}
		if !v.retrieved {
			// INVENTÉ — in no source. Fail closed on the whole answer.
			return unverifiedIdentifierDecline, true
		}
		retrievedRaws = append(retrievedRaws, v.raw) // RETROUVÉ — keep, mark below
	}
	if len(retrievedRaws) > 0 {
		return answer + provenanceMarker(retrievedRaws), false
	}
	return answer, false
}
