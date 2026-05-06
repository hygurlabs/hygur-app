package store

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// NormalizeTagName collapses cosmetic variants of a tag name to a single key:
// "Banques", "banques", "BANQUES " and "Banqués" all map to "banques".
//
// We use NFD decomposition to split base letters from combining marks, drop
// the marks (so accents disappear), lowercase, and collapse internal runs of
// whitespace. This matters because users routinely type tags with mixed case
// or accidental accents — exact-match dedup misses those, leading to twin
// tags ("banques" vs "Banques") that fragment item counts.
func NormalizeTagName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	decomposed := norm.NFD.String(s)
	var b strings.Builder
	b.Grow(len(decomposed))
	prevSpace := false
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue // strip combining marks (accents)
		}
		if unicode.IsSpace(r) {
			if !prevSpace && b.Len() > 0 {
				b.WriteRune(' ')
			}
			prevSpace = true
			continue
		}
		prevSpace = false
		b.WriteRune(unicode.ToLower(r))
	}
	out := b.String()
	return strings.TrimRight(out, " ")
}
