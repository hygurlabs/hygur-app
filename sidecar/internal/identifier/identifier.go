// Package identifier provides deterministic normalization + detection for exact-lookup
// queries (national numbers, VAT, IBAN, phone, invoice/reference numbers…). It reduces a
// value to its identifier core — lowercase, digits and letters only — so every cosmetic
// format canonicalizes the same way. No separator list to maintain (a missed separator was
// the classic whack-a-mole); anything that isn't [0-9a-z] is dropped. No LLM, no embeddings.
package identifier

import "strings"

// MinIdentifierDigits is the digit floor for treating a token as an exact-lookup
// identifier. Set to 9 so an 8-digit date (YYYYMMDD, e.g. a normalized "2024-03-15") stays
// on the semantic path, while real identifiers — phone (9–10), VAT (10), national number
// (11), IBAN (14+) — are caught. Shorter identifiers fall back to semantic, never worse
// than today. Exported so the label-fact extractor shares the same value-grade floor.
const MinIdentifierDigits = 9

// Normalize reduces s to its identifier core: lowercase, keeping only [0-9a-z] and dropping
// every separator, punctuation and space. General by construction — "12.34.56:789-01",
// "12 34 56 789 01" and "BE 0123.456.789" all canonicalize identically.
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ExtractQuery inspects a search query and, if it carries an exact identifier, returns its
// canonical form and true. It tokenizes the RAW query on whitespace, normalizes each token,
// and picks the one with the most digits — accepting it only when that token has at least
// MinIdentifierDigits digits. So "invoice 12345678901" yields "12345678901", while a prose
// query or a short code yields ("", false) and stays on the semantic path. (A single
// identifier the user types WITH spaces is a known gap — whitespace splits it into tokens.)
func ExtractQuery(query string) (string, bool) {
	best, bestDigits := "", 0
	for _, tok := range strings.Fields(query) {
		n := Normalize(tok)
		if d := digitCount(n); d > bestDigits {
			best, bestDigits = n, d
		}
	}
	if bestDigits < MinIdentifierDigits {
		return "", false
	}
	return best, true
}

func digitCount(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}
