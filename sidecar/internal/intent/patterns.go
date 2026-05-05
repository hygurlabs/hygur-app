package intent

import (
	"regexp"
	"sort"
	"strings"
)

// Mail patterns (French + English)
var mailPatterns = []string{
	// French explicit patterns
	`dans mes mails?`,
	`dans mes emails?`,
	`dans ma messagerie`,
	`dans mes courriels?`,
	`dans ma boite mail`,
	`dans ma boite de reception`,
	`dans mes messages`,
	// English explicit patterns
	`in my mails?`,
	`in my emails?`,
	`in my inbox`,
	`from my inbox`,
	`from my emails?`,
	`from my mails?`,
	`email.*from`,
	`mail.*de`,
}

// mailSemanticKeywords are words that suggest transactional/email content
// even without explicit "dans mes mails" mention.
var mailSemanticKeywords = []string{
	// French transactional keywords
	`recharge`,
	`récapitulatif`,
	`facture`,
	`confirmation de commande`,
	`confirmation d.achat`,
	`livraison`,
	`expédition`,
	`ticket`,
	`réservation`,
	`booking`,
	// English transactional keywords
	`receipt`,
	`invoice`,
	`order confirmation`,
	`shipping`,
	`delivery`,
	`booking confirmation`,
}

// Knowledge patterns (French + English)
var knowledgePatterns = []string{
	// French patterns
	`dans mes notes?`,
	`dans mes documents?`,
	`dans ma base`,
	`dans mes fichiers?`,
	`dans ma documentation`,
	`dans mes connaissances`,
	// English patterns
	`in my notes?`,
	`in my documents?`,
	`in my files?`,
	`from my knowledge`,
	`from my notes?`,
	`from my documents?`,
	`in my knowledge base`,
}

// Temporal patterns for recency detection (French + English)
var temporalRecentPatterns = []string{
	// French patterns - "les dernières", "les plus récentes", etc.
	`(?:les?\s+)?(\d+)\s+derni[eè]re?s?`,
	`(?:les?\s+)?derni[eè]re?s?\s+(\d+)`,
	`(?:les?\s+)?(\d+)\s+plus\s+r[ée]cente?s?`,
	`(?:les?\s+)?plus\s+r[ée]cente?s?\s+(\d+)`,
	`r[ée]cemment`,
	`cette\s+semaine`,
	`ce\s+mois`,
	`ces\s+derniers\s+jours`,
	`derni[eè]rement`,
	// English patterns - "last N", "most recent", "latest", etc.
	`(?:the\s+)?last\s+(\d+)`,
	`(?:the\s+)?(\d+)\s+most\s+recent`,
	`most\s+recent(?:ly)?`,
	`latest`,
	`newest`,
	`this\s+week`,
	`this\s+month`,
	`recent(?:ly)?`,
}

// temporalRangeMonths maps lowercase month names (and common variants) to month number.
// Used by matchesTemporalRange in detector.go.
var temporalRangeMonths = map[string]int{
	"janvier": 1, "january": 1,
	"février": 2, "fevrier": 2, "february": 2,
	"mars": 3, "march": 3,
	"avril": 4, "april": 4,
	"mai": 5, "may": 5,
	"juin": 6, "june": 6,
	"juillet": 7, "july": 7,
	"août": 8, "aout": 8, "august": 8,
	"septembre": 9, "september": 9,
	"octobre": 10, "october": 10,
	"novembre": 11, "november": 11,
	"décembre": 12, "decembre": 12, "december": 12,
}

// Compiled regex patterns (initialized once).
var (
	mailRegex           *regexp.Regexp
	mailSemanticRegex   *regexp.Regexp
	knowledgeRegex      *regexp.Regexp
	temporalRecentRegex *regexp.Regexp
	temporalRangeRegex  *regexp.Regexp
)

func init() {
	mailRegex = compilePatterns(mailPatterns)
	mailSemanticRegex = compilePatterns(mailSemanticKeywords)
	knowledgeRegex = compilePatterns(knowledgePatterns)
	temporalRecentRegex = compilePatterns(temporalRecentPatterns)

	monthNames := make([]string, 0, len(temporalRangeMonths))
	for name := range temporalRangeMonths {
		monthNames = append(monthNames, regexp.QuoteMeta(name))
	}
	sort.Strings(monthNames) // deterministic
	temporalRangeRegex = regexp.MustCompile(`(?i)\b(` + strings.Join(monthNames, "|") + `)\b`)
}

// compilePatterns joins patterns and compiles them into a case-insensitive regex.
func compilePatterns(patterns []string) *regexp.Regexp {
	combined := `(?i)(` + strings.Join(patterns, "|") + `)`
	return regexp.MustCompile(combined)
}

// GetMailRegex returns the compiled mail detection regex.
func GetMailRegex() *regexp.Regexp {
	return mailRegex
}

// GetMailSemanticRegex returns the compiled mail semantic keyword regex.
func GetMailSemanticRegex() *regexp.Regexp {
	return mailSemanticRegex
}

// GetKnowledgeRegex returns the compiled knowledge detection regex.
func GetKnowledgeRegex() *regexp.Regexp {
	return knowledgeRegex
}

// GetTemporalRecentRegex returns the compiled temporal recency regex.
func GetTemporalRecentRegex() *regexp.Regexp {
	return temporalRecentRegex
}

// GetTemporalRangeRegex returns the compiled temporal range (month name) regex.
func GetTemporalRangeRegex() *regexp.Regexp { return temporalRangeRegex }
