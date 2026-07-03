// Package recognize types the identifier runs found in a document deterministically, by
// CHECKSUM — the Presidio-style "typed recognizer" idea. A number is emitted only if it
// passes the checksum of a known type, so a birth-certificate act number (which fails the
// national-number checksum) is never mistaken for a national number. No LLM, no heuristics.
//
// v1 covers the checksum-bearing types (Belgian national number, enterprise/VAT, IBAN);
// each new type is one validator. Phone (github.com/nyaruka/phonenumbers) plugs in the same
// way when needed. All examples in tests are fictional — never real PII.
package recognize

import (
	"regexp"
	"strconv"
	"time"

	"github.com/hygur/sidecar/internal/identifier"
)

// Identifier type tags (also used as the entity_mentions attribute of the typed node).
const (
	TypeNationalNumber = "national_number"
	TypeEnterprise     = "enterprise_number" // Belgian BCE/KBO, incl. a "BE…" VAT prefix
	TypeIBAN           = "iban"
)

// IsChecksumType reports whether idType is a FAMILY-A (self-verifying, checksum-validated)
// identifier type owned by this package — as opposed to a FAMILY-B label-derived type
// (labelfact). It is the single source of truth for the family split: family A may be
// affirmed HIGH (the checksum is intrinsic proof); family B is capped at MED (a label
// binding is not proof). Callers pass a normalized/canonical type (e.g. from
// labelfact.NormalizeLabel), which maps VAT synonyms onto TypeEnterprise, so "vat" reaches
// here as "enterprise_number".
func IsChecksumType(idType string) bool {
	switch idType {
	case TypeNationalNumber, TypeEnterprise, TypeIBAN:
		return true
	}
	return false
}

// Typed is one recognized identifier: its type, canonical value (the graph-node key), the
// raw substring as written, and its byte offsets (for proximity scoring later).
type Typed struct {
	Type  string
	Value string
	Raw   string
	Start int
	End   int
}

// numRe matches a numeric run: a maximal span of digits with the in-value separators
// ". - / : space" between them, bounded by letters/other. Letters (labels, words) bound a
// run, so "numéro national: 23.02.23:347-71" yields the value's digits without the label.
var numRe = regexp.MustCompile(`[0-9][0-9 .\-/:]*[0-9]|[0-9]`)

// ibanRe matches an (unspaced) IBAN: two country letters, two check digits, then the BBAN.
var ibanRe = regexp.MustCompile(`[A-Za-z]{2}[0-9]{2}[0-9A-Za-z]{11,30}`)

// Recognize returns every checksum-valid typed identifier in text. For the pure-digit types
// (national number, enterprise/VAT) it slides a fixed-length checksum window WITHIN each
// numeric run — so a value works whether it stands alone, is separator-formatted, or is
// embedded in a longer digit sequence (real OCR'd documents do all three). The checksum is
// the guard: an arbitrary window has ~1% chance of passing, further cut by the NISS date
// sanity, and a spurious node barely co-occurs so NPMI dampens it downstream. Greedy within
// a run: on a hit, skip past the consumed digits.
func Recognize(text string) []Typed {
	var out []Typed
	for _, m := range numRe.FindAllStringIndex(text, -1) {
		run := text[m[0]:m[1]]
		digits := identifier.Normalize(run) // separators gone → pure digits
		for i := 0; i+10 <= len(digits); {
			switch {
			case i+11 <= len(digits) && isNISS(digits[i:i+11]):
				out = append(out, Typed{TypeNationalNumber, digits[i : i+11], run, m[0], m[1]})
				i += 11
			case isBCE(digits[i : i+10]):
				out = append(out, Typed{TypeEnterprise, digits[i : i+10], run, m[0], m[1]})
				i += 10
			default:
				i++
			}
		}
	}
	for _, m := range ibanRe.FindAllStringIndex(text, -1) {
		raw := text[m[0]:m[1]]
		if canon, ok := validIBAN(identifier.Normalize(raw)); ok {
			out = append(out, Typed{TypeIBAN, canon, raw, m[0], m[1]})
		}
	}
	return out
}

func isNISS(d string) bool { _, ok := validNISS(d); return ok }
func isBCE(d string) bool  { _, ok := validBCE(d); return ok }

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// validNISS validates a Belgian national number (11 digits, YYMMDD·SSS·CC) by its mod-97
// checksum — with the "2"-prefix variant for births in 2000+ — then DECODES the embedded
// birthdate and rejects it if it is not a plausible human birthdate. The matching checksum
// branch fixes the century (pre-2000 vs 2000+), so the leading YYMMDD becomes an unambiguous
// date we can sanity-check. The checksum alone lets ~1% of arbitrary 11-digit windows through
// (see Recognize); requiring the decoded date to be a real, in-range, non-future calendar date
// kills the random false positives whose decoded date is nonsense (month 00, day 30 in
// February, a future or absurd year).
func validNISS(norm string) (string, bool) {
	if len(norm) != 11 || !allDigits(norm) {
		return "", false
	}
	base, _ := strconv.ParseInt(norm[:9], 10, 64)
	check, _ := strconv.ParseInt(norm[9:], 10, 64)
	yy, _ := strconv.Atoi(norm[0:2])
	mm, _ := strconv.Atoi(norm[2:4])
	dd, _ := strconv.Atoi(norm[4:6])
	if 97-(base%97) == check && plausibleBirthdate(1900+yy, mm, dd) { // born < 2000
		return norm, true
	}
	base2, _ := strconv.ParseInt("2"+norm[:9], 10, 64) // born >= 2000
	if 97-(base2%97) == check && plausibleBirthdate(2000+yy, mm, dd) {
		return norm, true
	}
	return "", false
}

// plausibleBirthdate reports whether (year, month, day) is a real human birthdate: a valid
// calendar date (so 31 Feb is rejected), within [1900, this year], and not in the future.
// It is deliberately strict on the calendar (round-trips through time.Date) because that is
// exactly what separates a decoded NISS date from a random checksum-passing digit run.
func plausibleBirthdate(year, month, day int) bool {
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return false
	}
	now := time.Now()
	if year < 1900 || year > now.Year() {
		return false
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if t.Year() != year || t.Month() != time.Month(month) || t.Day() != day {
		return false // e.g. 30 February normalized to early March
	}
	return !t.After(now)
}

// validBCE validates a Belgian enterprise/VAT number (10 digits starting 0 or 1, plus an
// optional "be" country prefix) by its mod-97 checksum on the first 8 digits.
func validBCE(norm string) (string, bool) {
	n := norm
	if len(n) >= 2 && n[:2] == "be" {
		n = n[2:]
	}
	if len(n) != 10 || !allDigits(n) || (n[0] != '0' && n[0] != '1') {
		return "", false
	}
	base, _ := strconv.ParseInt(n[:8], 10, 64)
	check, _ := strconv.ParseInt(n[8:], 10, 64)
	if 97-(base%97) == check {
		return n, true
	}
	return "", false
}

// validIBAN validates any-country IBAN by the ISO 7064 mod-97-10 check: move the first four
// chars to the end, map letters a→10 … z→35 (two digits each), the whole number mod 97 == 1.
func validIBAN(norm string) (string, bool) {
	if len(norm) < 15 || len(norm) > 34 {
		return "", false
	}
	if !isLower(norm[0]) || !isLower(norm[1]) || norm[2] < '0' || norm[2] > '9' || norm[3] < '0' || norm[3] > '9' {
		return "", false
	}
	rem := 0
	rearranged := norm[4:] + norm[:4]
	for i := 0; i < len(rearranged); i++ {
		c := rearranged[i]
		switch {
		case c >= '0' && c <= '9':
			rem = (rem*10 + int(c-'0')) % 97
		case c >= 'a' && c <= 'z':
			rem = (rem*100 + int(c-'a') + 10) % 97
		default:
			return "", false
		}
	}
	if rem == 1 {
		return norm, true
	}
	return "", false
}

func isLower(b byte) bool { return b >= 'a' && b <= 'z' }
