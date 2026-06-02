package retrieval

import (
	"strings"
	"time"
	"unicode"
)

// Scoring modes for the final blend of semantic similarity and recency.
const (
	ScoringModeAdditive       = "additive"
	ScoringModeMultiplicative = "multiplicative"
)

// DefaultCurrentStateFilterDays is the default lookback window applied when the
// query is detected as a "current state" question (amounts due, balances, etc.).
const DefaultCurrentStateFilterDays = 90

// currentStateMarkers signal that the user is asking about a present-time fact
// — what the balance IS, what the amount IS DUE, what the latest invoice IS.
// Detected substrings (case-insensitive). The list deliberately mixes English
// and French because the chat is bilingual.
var currentStateMarkers = []string{
	// English
	"latest", "current", "last ", "recent", "this month", "this quarter",
	"due", "owe", "outstanding", "balance",
	// French
	"montant", "solde", "échéance", "rappel", "dernier", "dernière",
	"actuel", "actuelle", "à payer", "dû", "due", "redevable",
}

// IsCurrentStateQuery returns true when the query reads as a question about a
// present-time fact. Used to gate the 90-day pre-filter and to bias the
// recency weight in additive scoring.
//
// False positives are tolerable (the worst case is the pre-filter falling back
// to full history when 0 results match). False negatives are also tolerable —
// the additive scoring without temporal marker still rewards recency.
func IsCurrentStateQuery(query string) bool {
	q := strings.ToLower(query)
	for _, m := range currentStateMarkers {
		if strings.Contains(q, m) {
			return true
		}
	}
	return false
}

// monthTokens are month names (English + French, full + common abbreviations)
// used by QueryHasExplicitPeriod to detect a subject-period reference.
var monthTokens = map[string]struct{}{
	"january": {}, "february": {}, "march": {}, "april": {}, "may": {}, "june": {},
	"july": {}, "august": {}, "september": {}, "october": {}, "november": {}, "december": {},
	"jan": {}, "feb": {}, "mar": {}, "apr": {}, "jun": {}, "jul": {}, "aug": {},
	"sep": {}, "sept": {}, "oct": {}, "nov": {}, "dec": {},
	"janvier": {}, "février": {}, "fevrier": {}, "mars": {}, "avril": {}, "mai": {},
	"juin": {}, "juillet": {}, "août": {}, "aout": {}, "septembre": {}, "octobre": {},
	"novembre": {}, "décembre": {}, "decembre": {},
}

// QueryHasExplicitPeriod reports whether the query names a concrete year
// (2000-2099) or month. Such a token signals the user is asking about a SUBJECT
// period ("recharges 2026", "facture avril") — which usually differs from the
// document's RECEIVED date. When true, callers skip the received-date hard
// pre-filter and soften recency weighting so older-but-relevant docs (a March
// invoice received in March, asked about in June) stay retrievable.
func QueryHasExplicitPeriod(query string) bool {
	for _, f := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if isYearToken(f) {
			return true
		}
		if _, ok := monthTokens[f]; ok {
			return true
		}
	}
	return false
}

func isYearToken(f string) bool {
	if len(f) != 4 {
		return false
	}
	for _, r := range f {
		if r < '0' || r > '9' {
			return false
		}
	}
	return f >= "2000" && f <= "2099"
}

// recencyScore maps a document age into [0, 1]. Today → 1.0, infinity → 0.
//
// recency(d) = 1 / (1 + days_since)
//
// 0d:   1.000     30d: 0.032     90d: 0.011     365d: 0.003
//
// The curve is sharply front-loaded: most of the weight is in the first few
// days. This is what we want — "what is my VAT amount" should strongly prefer
// last week's email over last quarter's, and treat anything older as roughly
// equal background noise.
func recencyScore(date, now time.Time) float64 {
	if date.IsZero() {
		return 0
	}
	days := now.Sub(date).Hours() / 24
	if days < 0 {
		days = 0
	}
	return 1.0 / (1.0 + days)
}

// detectQueryEntityType returns a short label ("iban", "amount", "communication")
// when the query unambiguously targets one of the Tier 1 structured entities,
// or "" otherwise. Used to boost items that carry the matching extracted_*
// metadata field. False positives are limited because matching requires
// concrete keywords, not topic words.
func detectQueryEntityType(query string) string {
	q := strings.ToLower(query)
	// IBAN / account number
	switch {
	case strings.Contains(q, "iban"),
		strings.Contains(q, "account number"),
		strings.Contains(q, "numéro de compte"),
		strings.Contains(q, "compte bancaire"):
		return "iban"
	}
	// Amount / montant
	switch {
	case strings.Contains(q, "amount"),
		strings.Contains(q, "montant"),
		strings.Contains(q, "balance"),
		strings.Contains(q, "solde"),
		strings.Contains(q, "how much"),
		strings.Contains(q, "combien"):
		return "amount"
	}
	// Structured communication / payment reference
	switch {
	case strings.Contains(q, "communication"),
		strings.Contains(q, "reference"),
		strings.Contains(q, "référence"),
		strings.Contains(q, "structured comm"):
		return "communication"
	}
	return ""
}

// metadataHasEntity reports whether the given metadata map carries an extracted
// entity of the requested type. The keys are written by Tier 1 in
// internal/mail/extract.go.
func metadataHasEntity(metadata map[string]any, entityType string) bool {
	if metadata == nil {
		return false
	}
	var key string
	switch entityType {
	case "iban":
		key = "extracted_iban"
	case "amount":
		key = "extracted_amounts"
	case "communication":
		key = "extracted_structured_comm"
	default:
		return false
	}
	v, ok := metadata[key]
	if !ok {
		return false
	}
	// Stored as []string (or []any after JSON unmarshal); non-nil + non-empty wins.
	switch s := v.(type) {
	case []string:
		return len(s) > 0
	case []any:
		return len(s) > 0
	}
	return false
}

// ApplyAdditiveScore blends semantic similarity and recency linearly:
//
//	final = sem*(1-Wr) + recency*Wr
//
// where Wr=0.5 when a temporal marker is detected (the user explicitly asked
// about recency) and Wr=0.3 otherwise (recency as a soft tiebreaker).
//
// This is fundamentally different from multiplicative freshness: a very recent
// doc with mediocre cosine can now beat an older doc with perfect cosine, which
// is the desired behavior for "current state" questions where the user wants
// today's number, not last year's most relevant document.
func ApplyAdditiveScore(semScore float64, date, now time.Time, hasTemporalMarker bool) float64 {
	wr := 0.3
	if hasTemporalMarker {
		wr = 0.5
	}
	rec := recencyScore(date, now)
	return semScore*(1-wr) + rec*wr
}
