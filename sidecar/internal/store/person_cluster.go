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
func DistinctPeople(norms []string) int { return DistinctPeopleGuarded(norms, nil) }

// DistinctPeopleGuarded is DistinctPeople with a FATHER/SON subset guard. Two norms in a
// token-subset relation ({gérard,petit} ⊆ {denis,gérard,petit}) are normally merged as
// one person's name variants. That is wrong when the subset norm is a DIFFERENT person who
// merely shares a surname (and a middle name) with the superset — a father inside his son's
// full name. The guard blocks the merge when the two norms carry DISJOINT, non-empty
// national_number sets: two distinct NISS = two people, regardless of token-subset. natNums
// maps a norm (as passed in norms) to its own national_number values; an empty/absent entry
// means "no id known" and never blocks a merge, so genuine no-id name variants still collapse.
func DistinctPeopleGuarded(norms []string, natNums map[string][]string) int {
	var sets []map[string]bool
	var sigs []string
	seen := map[string]bool{}
	natBySig := map[string]map[string]bool{} // folded-signature → its national_number value set
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
		for _, v := range natNums[n] { // accumulate even on duplicate signatures
			if v == "" {
				continue
			}
			if natBySig[sig] == nil {
				natBySig[sig] = map[string]bool{}
			}
			natBySig[sig][v] = true
		}
		if seen[sig] {
			continue // identical token set → same person, count once
		}
		seen[sig] = true
		sets = append(sets, set)
		sigs = append(sigs, sig)
	}
	count := 0
	for i, a := range sets {
		maximal := true
		for j, b := range sets {
			// a is non-maximal iff some other set b is a strict superset of it. After the
			// signature dedup no two sets are equal, so len(a) < len(b) makes the subset strict.
			// The father/son guard keeps a maximal (unmerged) when a and b carry conflicting NISS.
			if i != j && len(a) < len(b) && tokenSubset(a, b) && !nissConflict(natBySig[sigs[i]], natBySig[sigs[j]]) {
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

// nissConflict reports whether two persons carry DISJOINT, non-empty national_number sets —
// the hard signal they are two distinct people even when one name's tokens subset the other's.
// Empty on either side = no conflict (a no-id name variant still merges); a shared value = same
// person (the founder's own variants both linked to his one NISS still collapse).
func nissConflict(a, b map[string]bool) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for v := range a {
		if b[v] {
			return false
		}
	}
	return true
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
