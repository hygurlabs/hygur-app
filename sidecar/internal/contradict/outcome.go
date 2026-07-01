package contradict

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// replyPrefix matches a leading reply/forward marker on a normalized (folded) subject.
var replyPrefix = regexp.MustCompile(`^\s*(re|ref|rep|fwd|fw|tr|aw|wg)\s*:\s*`)

// ThreadKey normalizes a message subject into a stable thread key: it strips repeated
// reply/forward prefixes (Re:, Fwd:, TR:, …), lowercases + strips accents, and collapses
// whitespace, so the messages of one conversation share a key. Deterministic — a coarse
// grouping when no thread id is available in the mail metadata.
func ThreadKey(title string) string {
	s := foldOutcome(title)
	for {
		t := replyPrefix.ReplaceAllString(s, "")
		if t == s {
			break
		}
		s = t
	}
	return strings.Join(strings.Fields(s), " ")
}

// Thread-outcome detection (docs/PSYCHE_GROUNDING_PLAN.md §11). Purely deterministic:
// no LLM. It reads the status/outcome CLAIMS already extracted for an item and decides
// whether the matter ended in a terminal-negative outcome (rejection, cancellation,
// …), so a closed thread is not surfaced as a "follow up / chase this" item.

// terminalNegativeRoots are normalized (lowercase, accent-free) substrings that mark a
// terminal-negative outcome on a status/decision value. Curated + multilingual (FR/EN);
// extend as missed phrasings surface. Chosen to avoid common false positives (e.g.
// "closed" not the bare "clos", which hides in "disclosure").
var terminalNegativeRoots = []string{
	"refus", "rejet", "reject", "declin", "decline", "annul", "cancel", "resili",
	"perdu", "lost", "cloture", "closed", "abandon", "demission", "withdrawn", "retire",
	"dropped", "sans suite", "sans traitement", "non retenu", "unsuccessful", "not retained",
	"negativ", // "réponse négative" / negative response — a rejection
}

// isOutcomeAttribute reports whether a claim attribute carries a thread's outcome —
// "status", "delivery status", "project status", "outcome", "decision", "result"…
func isOutcomeAttribute(attr string) bool {
	a := foldOutcome(attr)
	for _, k := range []string{"status", "outcome", "issue", "decision", "result"} {
		if strings.Contains(a, k) {
			return true
		}
	}
	return false
}

// ClosedNegative reports whether a set of claims shows the matter ended in a terminal-
// negative outcome: the LATEST outcome-bearing claim (by AssertedAt) has a value
// containing a terminal-negative root. Deterministic, no LLM. Returns the closure date
// (the claim's AssertedAt) when true.
func ClosedNegative(claims []Claim) (bool, string) {
	var latest *Claim
	for i := range claims {
		c := &claims[i]
		if !isOutcomeAttribute(c.Attribute) {
			continue
		}
		if latest == nil || c.AssertedAt > latest.AssertedAt {
			latest = c
		}
	}
	if latest == nil {
		return false, ""
	}
	v := foldOutcome(latest.Value)
	for _, root := range terminalNegativeRoots {
		if strings.Contains(v, root) {
			return true, latest.AssertedAt
		}
	}
	return false, ""
}

// foldOutcome lowercases and strips accents, keeping spaces so multi-word roots (e.g.
// "sans traitement") still match.
func foldOutcome(s string) string {
	d := norm.NFD.String(strings.ToLower(s))
	var b strings.Builder
	b.Grow(len(d))
	for _, r := range d {
		if unicode.Is(unicode.Mn, r) { // combining mark (accent)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
