package store

import (
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// DistinctPeople counts how many distinct natural persons a set of entity norms denotes.
//
// Two norms are treated as the SAME person when one's token set is a subset of the other's — a
// name-order / middle-name / OCR variant of one person ({petit,denis} ⊆ {denis,gerard,petit}).
// The answer is the number of MAXIMAL norms: those NOT a subset of any other norm in the set.
// Tokens are accent-folded first, so an OCR accent split ("gérard" vs "gerard") does not
// masquerade as a second person.
//
// It deliberately counts maximals, NOT connected components. A bare first name that is a subset
// of two UNRELATED full names leaves TWO maximals (correctly two distinct people) and is never
// transitively merged into one through that shared short norm:
//
//	{denis}, {denis,gerard,petit}, {denis,martin}  -> 2 maximals (two Denises), not 1.
//	{petit,denis}, {denis,gerard,petit}         -> 1 maximal (one person, two variants).
//
// This is the read/write measure of "how many people" behind a pooled name query or a value's
// owners, so a person's own name-variant norms resolve while genuinely distinct people stay split.
func DistinctPeople(norms []string) int {
	var sets []map[string]bool
	seen := map[string]bool{}
	for _, n := range norms {
		set := map[string]bool{}
		for _, tok := range strings.Fields(n) {
			if f := foldNameToken(tok); f != "" {
				set[f] = true
			}
		}
		if len(set) == 0 {
			continue
		}
		sig := tokenSetSig(set)
		if seen[sig] {
			continue // identical token set → same person, count once
		}
		seen[sig] = true
		sets = append(sets, set)
	}
	count := 0
	for i, a := range sets {
		maximal := true
		for j, b := range sets {
			// a is non-maximal iff some other set b is a strict superset of it. After the
			// signature dedup no two sets are equal, so len(a) < len(b) makes the subset strict.
			if i != j && len(a) < len(b) && tokenSubset(a, b) {
				maximal = false
				break
			}
		}
		if maximal {
			count++
		}
	}
	return count
}

// tokenSubset reports whether every token of a is present in b.
func tokenSubset(a, b map[string]bool) bool {
	for t := range a {
		if !b[t] {
			return false
		}
	}
	return true
}

// tokenSetSig is the order-independent signature of a token set (sorted, space-joined).
func tokenSetSig(set map[string]bool) string {
	toks := make([]string, 0, len(set))
	for t := range set {
		toks = append(toks, t)
	}
	sort.Strings(toks)
	return strings.Join(toks, " ")
}

// foldNameToken lowercases a name token and strips diacritics (NFD decomposition then drop
// combining marks), so accent/OCR variants of the same token compare equal.
func foldNameToken(tok string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(tok) {
		if unicode.Is(unicode.Mn, r) {
			continue // combining mark (accent) → drop
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
