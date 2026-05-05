// Package extract provides text-extraction primitives that are agnostic of the
// document source (email, markdown, PDF, …). Tier 1 is rule-based regex
// extraction (IBAN, monetary amounts, VAT numbers, structured communications,
// phone numbers, URLs, due dates) and runs on any normalized text body.
package extract

import (
	"regexp"
	"strings"
)

// AmountEntity is a monetary amount normalized to the form "1234.56 EUR".
type AmountEntity struct {
	Value    string `json:"value"`    // e.g. "7421.85"
	Currency string `json:"currency"` // e.g. "EUR"
	Raw      string `json:"raw"`      // original match, preserved for debugging
}

// Tier1Entities holds the rule-based entity extraction output.
// All fields are deduplicated and ordered by first appearance in the source text.
type Tier1Entities struct {
	IBANs                    []string       `json:"ibans,omitempty"`
	Amounts                  []AmountEntity `json:"amounts,omitempty"`
	StructuredCommunications []string       `json:"structured_communications,omitempty"`
	VATNumbers               []string       `json:"vat_numbers,omitempty"`
	PhoneNumbers             []string       `json:"phone_numbers,omitempty"`
	URLs                     []string       `json:"urls,omitempty"`
	DueDates                 []string       `json:"due_dates,omitempty"`
}

// Count returns the total number of entities across all categories.
func (t Tier1Entities) Count() int {
	return len(t.IBANs) + len(t.Amounts) + len(t.StructuredCommunications) +
		len(t.VATNumbers) + len(t.PhoneNumbers) + len(t.URLs) + len(t.DueDates)
}

var (
	// IBAN: 2 letters, 2 digits, then 11-30 alphanumeric chars optionally
	// separated by single spaces. Length is post-validated against ISO 13616.
	reIBAN = regexp.MustCompile(`\b[A-Z]{2}\d{2}(?:[ ]?[A-Z0-9]+)+\b`)

	// Belgian structured communication: +++NNN/NNNN/NNNNN+++.
	reStructuredComm = regexp.MustCompile(`\+{3}\d{3}/\d{4}/\d{5}\+{3}`)

	// EUR amount: digits with optional thousands separators (space, dot, comma)
	// followed by € or EUR. Captures the numeric portion and currency token.
	reAmountEUR = regexp.MustCompile(`(?i)(\d{1,3}(?:[\s.,]\d{3})*(?:[.,]\d{1,2})?|\d+[.,]\d{1,2}|\d+)\s*(€|EUR\b|euros?\b)`)

	// VAT number: country code (BE/FR/NL/DE/LU) + digits. Length validated per country.
	reVATNumber = regexp.MustCompile(`\b(BE|FR|NL|DE|LU)\s?(\d[\d ]{6,15}\d|\d{7,16})\b`)

	// Phone numbers: international or national, 7-15 digits with various separators.
	rePhone = regexp.MustCompile(`(?:\+\d{1,3}[\s.-]?)?(?:\(\d{1,4}\)[\s.-]?)?\d{2,4}[\s.-]?\d{2,4}[\s.-]?\d{2,4}(?:[\s.-]?\d{2,4})?`)

	// URLs: http(s) only. Stops at whitespace or angle brackets.
	reURL = regexp.MustCompile(`https?://[^\s<>"'\)]+`)

	// Due-date triggers (FR + EN). Captures: trigger phrase + the date itself.
	// Date forms accepted:
	//   - "25 avril 2026" / "25 April 2026"
	//   - "25/04/2026" / "25-04-2026"
	//   - "25 avril" / "April 25" (year inferred elsewhere if needed)
	// The separator between trigger and date may be a colon, dash, or
	// whitespace ("Échéance : 25/04/2026" is common in Belgian accounting).
	reDueDate = regexp.MustCompile(`(?i)(?:à payer avant le|date d'échéance|échéance(?: le)?|avant le|due (?:by|before|on)|by the|by)[\s:\-—–]+((?:\d{1,2}[\s./-]\d{1,2}[\s./-]\d{2,4})|(?:\d{1,2}\s+[A-Za-zéûôîàèùç]{3,9}(?:\s+\d{4})?))`)
)

// unicodeSpaceReplacer normalizes typographic Unicode spaces (NBSP, narrow
// NBSP, figure space) to a regular ASCII space so that \s-based regexes in
// RE2 can match them. Cheaper than constructing a Unicode-aware regex.
var unicodeSpaceReplacer = strings.NewReplacer(
	" ", " ", // NO-BREAK SPACE
	" ", " ", // NARROW NO-BREAK SPACE
	" ", " ", // FIGURE SPACE
)

// vatLengthByCountry validates the digit count after the country code.
// BE: 9 or 10 digits, FR: 11, NL: 9 + 'B' + 2 (handled separately), DE: 9, LU: 8.
var vatLengthByCountry = map[string]map[int]bool{
	"BE": {9: true, 10: true},
	"FR": {11: true},
	"DE": {9: true},
	"LU": {8: true},
	"NL": {9: true, 12: true},
}

// ExtractTier1 runs all rule-based extractors on the given text and returns
// deduplicated entities. The text should be a normalized text body.
//
// Extractors that consume long digit sequences (IBAN, structured communication,
// VAT, URL) run first, then the matched spans are blanked out before phone
// number extraction so that digits inside an IBAN aren't mistaken for a phone.
func ExtractTier1(text string) Tier1Entities {
	ibans := extractIBANs(text)
	structComm := extractStructuredCommunications(text)
	vat := extractVATNumbers(text)
	urls := extractURLs(text)
	amounts := extractAmounts(text)
	dueDates := extractDueDates(text)

	masked := maskMatches(text, reIBAN, reStructuredComm, reVATNumber, reURL)
	phones := extractPhoneNumbers(masked)

	return Tier1Entities{
		IBANs:                    ibans,
		Amounts:                  amounts,
		StructuredCommunications: structComm,
		VATNumbers:               vat,
		PhoneNumbers:             phones,
		URLs:                     urls,
		DueDates:                 dueDates,
	}
}

// extractDueDates pulls phrases like "à payer avant le 25 avril 2026" or
// "due by 25/04/2026" into a deduplicated list of raw date strings.
// Normalisation to time.Time is left to the caller (different consumers care
// about different precision).
func extractDueDates(text string) []string {
	normalized := unicodeSpaceReplacer.Replace(text)
	matches := reDueDate.FindAllStringSubmatch(normalized, -1)
	out := make([]string, 0, len(matches))
	seen := make(map[string]bool)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		date := strings.TrimSpace(m[1])
		if date == "" || seen[date] {
			continue
		}
		seen[date] = true
		out = append(out, date)
	}
	return out
}

// maskMatches replaces every match of every regex in `regexes` with spaces of
// equal length, preserving offsets. Used to prevent later extractors from
// re-matching digits already consumed by earlier extractors.
func maskMatches(text string, regexes ...*regexp.Regexp) string {
	bytes := []byte(text)
	for _, re := range regexes {
		for _, idx := range re.FindAllIndex(bytes, -1) {
			for i := idx[0]; i < idx[1]; i++ {
				bytes[i] = ' '
			}
		}
	}
	return string(bytes)
}

func extractIBANs(text string) []string {
	matches := reIBAN.FindAllString(text, -1)
	out := make([]string, 0, len(matches))
	seen := make(map[string]bool)
	for _, m := range matches {
		canonical := strings.ReplaceAll(m, " ", "")
		// Real IBANs are 15-34 chars; reject shorter/longer matches that slip past the regex.
		if len(canonical) < 15 || len(canonical) > 34 {
			continue
		}
		if !seen[canonical] {
			seen[canonical] = true
			out = append(out, canonical)
		}
	}
	return out
}

func extractAmounts(text string) []AmountEntity {
	// French/European typography uses non-ASCII spaces as thousands separators
	// (NBSP U+00A0, NARROW NBSP U+202F, FIGURE SPACE U+2007). RE2's \s only
	// covers ASCII whitespace, so normalize these to a regular space first.
	normalized := unicodeSpaceReplacer.Replace(text)
	matches := reAmountEUR.FindAllStringSubmatch(normalized, -1)
	out := make([]AmountEntity, 0, len(matches))
	seen := make(map[string]bool)
	for _, m := range matches {
		raw := strings.TrimSpace(m[0])
		numeric := normalizeAmount(m[1])
		if numeric == "" {
			continue
		}
		key := numeric + "|EUR"
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, AmountEntity{Value: numeric, Currency: "EUR", Raw: raw})
	}
	return out
}

// normalizeAmount converts European-format numbers ("7 421,85" or "7.421,85")
// and US-format ("7,421.85" or "7421.85") into a canonical "7421.85" form.
// Returns an empty string if the input cannot be parsed.
func normalizeAmount(raw string) string {
	s := strings.ReplaceAll(raw, " ", "")
	if s == "" {
		return ""
	}

	lastDot := strings.LastIndex(s, ".")
	lastComma := strings.LastIndex(s, ",")

	switch {
	case lastDot == -1 && lastComma == -1:
		// Plain digit string — no separators to normalize.
	case lastComma > lastDot:
		// Comma is the decimal separator (European). Strip dots, swap comma to dot.
		s = strings.ReplaceAll(s, ".", "")
		s = strings.Replace(s, ",", ".", 1)
	default:
		// Dot is the decimal separator (US/UK). Strip commas.
		s = strings.ReplaceAll(s, ",", "")
	}

	// Final guard: must contain only digits and at most one dot.
	if strings.Count(s, ".") > 1 {
		return ""
	}
	for _, r := range s {
		if r != '.' && (r < '0' || r > '9') {
			return ""
		}
	}
	return s
}

func extractStructuredCommunications(text string) []string {
	matches := reStructuredComm.FindAllString(text, -1)
	out := make([]string, 0, len(matches))
	seen := make(map[string]bool)
	for _, m := range matches {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

func extractVATNumbers(text string) []string {
	matches := reVATNumber.FindAllStringSubmatch(text, -1)
	out := make([]string, 0, len(matches))
	seen := make(map[string]bool)
	for _, m := range matches {
		country := m[1]
		digitsRaw := m[2]
		digits := strings.ReplaceAll(digitsRaw, " ", "")
		validLengths, known := vatLengthByCountry[country]
		if !known || !validLengths[len(digits)] {
			continue
		}
		canonical := country + digits
		if !seen[canonical] {
			seen[canonical] = true
			out = append(out, canonical)
		}
	}
	return out
}

func extractPhoneNumbers(text string) []string {
	matches := rePhone.FindAllString(text, -1)
	out := make([]string, 0, len(matches))
	seen := make(map[string]bool)
	for _, m := range matches {
		// Reject anything with fewer than 7 digits — too noisy otherwise.
		digitCount := 0
		for _, r := range m {
			if r >= '0' && r <= '9' {
				digitCount++
			}
		}
		if digitCount < 7 || digitCount > 15 {
			continue
		}
		trimmed := strings.TrimSpace(m)
		if !seen[trimmed] {
			seen[trimmed] = true
			out = append(out, trimmed)
		}
	}
	return out
}

func extractURLs(text string) []string {
	matches := reURL.FindAllString(text, -1)
	out := make([]string, 0, len(matches))
	seen := make(map[string]bool)
	for _, m := range matches {
		// Trim trailing punctuation that the regex can over-capture.
		trimmed := strings.TrimRight(m, ".,;:!?)")
		if !seen[trimmed] {
			seen[trimmed] = true
			out = append(out, trimmed)
		}
	}
	return out
}
