package tools

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/identity"
	"github.com/hygur/sidecar/internal/recognize"
	"github.com/hygur/sidecar/internal/store"
)

// This file implements "Plan A" fact-store reconciliation: the deterministic
// rules that keep the autonomous memory writer from (a) storing the same soft
// fact twice and (b) storing typed identifiers that belong in the identifier
// graph. The functions are pure so they can be unit-tested without a DB and
// reused by both the write path (persistExtracted) and the one-time reconcile
// endpoint.

// foldText lowercases s and strips accents (NFD decomposition, dropping
// combining marks). Mirrors the folding used elsewhere in the codebase
// (contradict.foldOutcome, identity.foldTokens) so "Numéro" and "numero"
// compare equal.
func foldText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if unicode.Is(unicode.Mn, r) { // combining mark (accent) → drop
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// SignContent reduces a memory's content to a normalized signature for
// exact-content dedup: accent-folded, lowercased, every non-alphanumeric run
// collapsed to a single space, trimmed. Two memories with the same signature
// are treated as the same fact. Deliberately conservative — it does not merge
// digit runs written with different separators, so distinct facts that differ
// only in a number are never collapsed (FAIL CLOSED: keep, don't lose).
func SignContent(content string) string {
	folded := foldText(content)
	var b strings.Builder
	b.Grow(len(folded))
	prevSpace := true // leading-space suppressor
	for _, r := range folded {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') {
			b.WriteRune(r)
			prevSpace = false
			continue
		}
		if !prevSpace {
			b.WriteByte(' ')
			prevSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

// idLabelRe matches an explicit typed-identifier label in accent-folded,
// lowercased text. The label set is the one named in the plan (national
// number / numéro national / NISS / TVA / VAT / BCE / IBAN) plus the obvious
// enterprise-number spellings, matched on word boundaries so "vat" in
// "vat declaration" only fires when an identifier-grade value also appears.
var idLabelRe = regexp.MustCompile(`\b(national number|numero national|niss|tva|vat|bce|kbo|iban|numero d entreprise|numero de tva)\b`)

// idValueRe matches a numeric run including in-value separators (space . - / :),
// mirroring recognize.numRe so a spaced or separator-formatted identifier value
// is captured as one run.
var idValueRe = regexp.MustCompile(`[0-9][0-9 .\-/:]*[0-9]|[0-9]`)

// ibanLikeRe matches an IBAN-shaped token (2 letters, 2 digits, 10+ alnum)
// regardless of checksum — the label-backstop for a value recognize would miss.
var ibanLikeRe = regexp.MustCompile(`[a-z]{2}[0-9]{2}[0-9a-z]{10,30}`)

// identifierGradeDigits is the digit floor for treating a labelled value as an
// identifier (not a plain quantity). 9 matches identifier.minIdentifierDigits:
// phone (9–10), VAT (10), national number (11), IBAN (14+) clear it; a
// financial amount like "4500 EUR" (4 digits) or a quarter "Q1 2024" does not.
const identifierGradeDigits = 9

// hasIdentifierGradeValue reports whether folded text carries an
// identifier-grade value: a digit run with >= identifierGradeDigits digits, or
// an IBAN-shaped token.
func hasIdentifierGradeValue(folded string) bool {
	for _, run := range idValueRe.FindAllString(folded, -1) {
		digits := 0
		for _, r := range run {
			if r >= '0' && r <= '9' {
				digits++
			}
		}
		if digits >= identifierGradeDigits {
			return true
		}
	}
	return ibanLikeRe.MatchString(folded)
}

// IsTypedIdentifierAssertion reports whether content asserts a typed identifier
// (national number / enterprise number / IBAN) and therefore belongs in the
// deterministic identifier graph rather than the soft-fact memory store. The
// decision is CLASS-based (no fragile per-fact graph matching):
//
//  1. recognize.Recognize finds a checksum-valid identifier in the content, OR
//  2. the content carries an explicit identifier LABEL next to an
//     identifier-grade value (>= 9 digits, or an IBAN shape).
//
// Soft facts that carry no identifier value/label (accounting firm, tools,
// financial amounts, preferences) are never matched.
func IsTypedIdentifierAssertion(content string) bool {
	if len(recognize.Recognize(content)) > 0 {
		return true
	}
	folded := foldText(content)
	if idLabelRe.MatchString(folded) && hasIdentifierGradeValue(folded) {
		return true
	}
	return false
}

// ownerIdentityPrefixes are the short, fixed self-identity phrasings an extracted
// fact-memory candidate can open with. Matched on the accent-folded, lowercased,
// edge-punctuation-trimmed content — anchored at the START, so a longer sentence
// that merely mentions the owner in passing never matches (there is no prefix for
// "user works with…" or "user uses…").
var ownerIdentityPrefixes = []string{
	"the user's name is ", "user's name is ",
	"the user is ", "user is ",
	"my name is ", "i am ", "i'm ",
}

// isNameWord reports whether w looks like part of a person's name: letters plus an
// internal hyphen (Jean-Paul). No digits, no apostrophe, no other punctuation clause
// markers. An apostrophe is deliberately excluded even though it costs the rare
// O'Brien-style name: it almost always signals a possessive ("Vance's accountant"),
// i.e. a relationship/role clause about someone else rather than a pure identity
// assertion, and conservative means missing an identity assertion beats dropping a
// soft fact.
func isNameWord(w string) bool {
	if w == "" {
		return false
	}
	for _, r := range w {
		if r == '-' {
			continue
		}
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// IsOwnerIdentityAssertion reports whether content is PURELY a re-assertion of the
// owner's own name/identity (e.g. "User is Daniel Petit", "User's name is Denis")
// — already held by the first-class identity system (Identity.OwnerNames /
// identity.Matcher) and therefore redundant in the soft-fact memory store.
//
// Deliberately conservative (FAIL CLOSED — keep unless certain): content must open
// with one of a small set of fixed self-identity phrasings, the remainder must be a
// short (<=3 word) name-shaped phrase with no appended clause, and that phrase must
// itself resolve to the owner via the SAME strict matcher the dossier/identifier
// lookup use (never a bare surname/given name — identity.Matcher already guards
// that). A soft fact that merely MENTIONS the owner ("User works with Fiduciaire de
// la Cense", "User uses Falco") never matches: it neither opens with one of these
// prefixes NOR reduces to a bare name after the prefix.
func IsOwnerIdentityAssertion(content string, owner *identity.Matcher) bool {
	if owner == nil {
		return false
	}
	folded := strings.TrimSpace(strings.Trim(foldText(content), ".! "))
	var name string
	matched := false
	for _, p := range ownerIdentityPrefixes {
		if strings.HasPrefix(folded, p) {
			name = strings.TrimSpace(strings.TrimPrefix(folded, p))
			matched = true
			break
		}
	}
	if !matched || name == "" {
		return false
	}
	words := strings.Fields(name)
	if len(words) == 0 || len(words) > 3 {
		return false
	}
	for _, w := range words {
		if !isNameWord(w) {
			return false
		}
	}
	return owner.IsOwnerNorm(contradict.NormKey(name))
}

// Reconcile-pass planning over an existing set of rows.

// ReconcileReason explains why a row is slated for deletion.
type ReconcileReason string

const (
	// ReasonDuplicate: an equivalent-content row is kept elsewhere.
	ReasonDuplicate ReconcileReason = "duplicate"
	// ReasonIdentifier: the content is a typed-identifier assertion (deferred
	// to the identifier graph).
	ReasonIdentifier ReconcileReason = "identifier"
	// ReasonOwnerIdentity: the content is purely a re-assertion of the owner's
	// own name/identity (deferred to the identity system).
	ReasonOwnerIdentity ReconcileReason = "owner_identity"
)

// ReconcileDeletion is one row the plan would remove.
type ReconcileDeletion struct {
	Memory store.Memory
	Reason ReconcileReason
}

// ReconcilePlan is the disposition over a set of memories: which rows to delete
// (and why) and which soft facts are kept. It removes ONLY exact content
// duplicates (keeping the strongest survivor) and identifier-bearing rows;
// every unique soft fact is kept.
type ReconcilePlan struct {
	Deletions []ReconcileDeletion
	Kept      []store.Memory
}

// DuplicateCount / IdentifierCount / OwnerIdentityCount summarise the deletions by reason.
func (p ReconcilePlan) DuplicateCount() int     { return p.countReason(ReasonDuplicate) }
func (p ReconcilePlan) IdentifierCount() int    { return p.countReason(ReasonIdentifier) }
func (p ReconcilePlan) OwnerIdentityCount() int { return p.countReason(ReasonOwnerIdentity) }

func (p ReconcilePlan) countReason(r ReconcileReason) int {
	n := 0
	for _, d := range p.Deletions {
		if d.Reason == r {
			n++
		}
	}
	return n
}

// memoryStrength ranks a row for survivor selection: accepted beats pending.
func memoryStrength(m store.Memory) int {
	if m.AcceptedAt != nil {
		return 1
	}
	return 0
}

// PlanReconcile computes the conservative reconcile plan for mems. owner (may be
// nil) is the first-class owner matcher: when set, a row that is PURELY a
// re-assertion of the owner's own name/identity is also deferred (ReasonOwnerIdentity)
// — see IsOwnerIdentityAssertion. nil disables that rule (behavior-preserving for
// callers that don't have an owner matcher).
//
//   - Any typed-identifier assertion is removed (ReasonIdentifier), regardless
//     of type — it is redundant with the identifier graph.
//   - Any pure owner-identity assertion is removed (ReasonOwnerIdentity) — it is
//     redundant with the identity system.
//   - Among the remaining rows, rows sharing a content signature are collapsed
//     to a single survivor; the rest are removed (ReasonDuplicate). The survivor
//     is the STRONGEST row (accepted wins over pending; ties broken by oldest
//     created_at then memory_id for determinism), so "accepted wins" holds
//     without a separate status-promotion step.
//   - Every other row is kept.
//
// Pure and idempotent: running it again over the post-apply set yields no
// deletions.
func PlanReconcile(mems []store.Memory, owner *identity.Matcher) ReconcilePlan {
	var plan ReconcilePlan

	// Pass 1: peel off identifier-bearing and owner-identity rows.
	remaining := make([]store.Memory, 0, len(mems))
	for _, m := range mems {
		if IsTypedIdentifierAssertion(m.Content) {
			plan.Deletions = append(plan.Deletions, ReconcileDeletion{Memory: m, Reason: ReasonIdentifier})
			continue
		}
		if IsOwnerIdentityAssertion(m.Content, owner) {
			plan.Deletions = append(plan.Deletions, ReconcileDeletion{Memory: m, Reason: ReasonOwnerIdentity})
			continue
		}
		remaining = append(remaining, m)
	}

	// Pass 2: group the rest by content signature, keep the strongest survivor.
	groups := make(map[string][]store.Memory)
	order := make([]string, 0)
	for _, m := range remaining {
		sig := SignContent(m.Content)
		if _, ok := groups[sig]; !ok {
			order = append(order, sig)
		}
		groups[sig] = append(groups[sig], m)
	}
	for _, sig := range order {
		grp := groups[sig]
		if len(grp) == 1 {
			plan.Kept = append(plan.Kept, grp[0])
			continue
		}
		// Strongest first: accepted over pending, then oldest, then id.
		sort.SliceStable(grp, func(i, j int) bool {
			si, sj := memoryStrength(grp[i]), memoryStrength(grp[j])
			if si != sj {
				return si > sj
			}
			if !grp[i].CreatedAt.Equal(grp[j].CreatedAt) {
				return grp[i].CreatedAt.Before(grp[j].CreatedAt)
			}
			return grp[i].MemoryID < grp[j].MemoryID
		})
		plan.Kept = append(plan.Kept, grp[0])
		for _, dup := range grp[1:] {
			plan.Deletions = append(plan.Deletions, ReconcileDeletion{Memory: dup, Reason: ReasonDuplicate})
		}
	}
	return plan
}

// RedactContent produces a PII-safe sample of a memory body for operator
// reports/logs: accent-folded, every digit masked to '#', collapsed
// whitespace, truncated. Never emits a raw name/number verbatim beyond the
// leading, digit-masked fragment.
func RedactContent(content string) string {
	folded := foldText(content)
	var b strings.Builder
	prevSpace := true
	for _, r := range folded {
		switch {
		case r >= '0' && r <= '9':
			b.WriteByte('#')
			prevSpace = false
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			prevSpace = false
		default:
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
		}
	}
	s := strings.TrimSpace(b.String())
	const max = 32
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
