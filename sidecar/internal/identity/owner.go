// Package identity recognizes the corpus OWNER among entity norms, from the configured
// owner name/email variants (config Identity.OwnerNames). It is the first-class,
// deterministic notion of "self" the psyché uses to affirm the owner's OWN identifiers
// (owner anchor) and to unify his dossier — while never mis-attributing a family
// member's data to him. Pure and testable: no store, no LLM.
package identity

import (
	"strings"
	"unicode"

	"github.com/hygur/sidecar/internal/contradict"
	"golang.org/x/text/unicode/norm"
)

// Matcher decides whether an entity norm denotes the owner.
//
// A norm is the owner iff it carries a DISCRIMINATIVE owner signal: it contains every
// token of some MULTI-TOKEN owner variant — a full-name variant ("denis petit",
// "denis l") or the owner email (which normalizes to ≥2 tokens, e.g. "dle 0x0800 com").
// A BARE single-token variant (a lone surname or given name) is NEVER sufficient: the
// configured owner names deliberately include the whole family's surname and an
// ambiguous given name, and matching either alone would capture children, a parent, or
// unrelated namesakes. Requiring the full multi-token signal ⊆ the candidate means the
// candidate must share BOTH the discriminative given name AND the surname, so
// "denis petit" / "petit denis" / "denis gérard petit" (the founder's variants)
// match, while "gérard petit" (father), "elric petit durand" (child), and a bare
// "petit" / "denis" do not.
type Matcher struct {
	signals [][]string // each: the folded, deduped token set of one discriminative owner variant
	tokens  []string   // distinct folded tokens across all signals (candidate-gathering)
}

// NewMatcher builds an owner matcher from the configured owner name/email variants.
// Single-token variants are dropped as non-discriminative; the remaining multi-token
// variants (names and email) are the owner signals. A nil/empty result matches nobody.
func NewMatcher(ownerNames []string) *Matcher {
	m := &Matcher{}
	seenTok := map[string]bool{}
	for _, raw := range ownerNames {
		toks := foldTokens(contradict.NormKey(raw))
		if len(toks) < 2 {
			continue // bare surname / given name — NOT discriminative, never sufficient
		}
		m.signals = append(m.signals, toks)
		for _, t := range toks {
			if !seenTok[t] {
				seenTok[t] = true
				m.tokens = append(m.tokens, t)
			}
		}
	}
	return m
}

// IsOwnerNorm reports whether an entity norm denotes the owner. Nil-safe (false).
func (m *Matcher) IsOwnerNorm(entityNorm string) bool {
	if m == nil || len(m.signals) == 0 {
		return false
	}
	have := map[string]bool{}
	for _, t := range foldTokens(entityNorm) {
		have[t] = true
	}
	if len(have) == 0 {
		return false
	}
	for _, sig := range m.signals {
		if subset(sig, have) {
			return true
		}
	}
	return false
}

// Tokens returns the distinct folded tokens across every owner signal — a bounded set a
// caller uses to gather candidate person norms (which it then filters with IsOwnerNorm).
// Nil-safe (nil).
func (m *Matcher) Tokens() []string {
	if m == nil {
		return nil
	}
	return m.tokens
}

// subset reports whether every token of want is present in have.
func subset(want []string, have map[string]bool) bool {
	for _, t := range want {
		if !have[t] {
			return false
		}
	}
	return true
}

// foldTokens splits a norm into accent-folded, deduped, lowercased tokens — the same
// NFD-drop-combining-marks folding store.DistinctPeople uses, so "gérard" and "gerard"
// compare equal.
func foldTokens(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tok := range strings.Fields(s) {
		f := foldToken(tok)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

func foldToken(tok string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(tok) {
		if unicode.Is(unicode.Mn, r) {
			continue // combining mark (accent) → drop
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
