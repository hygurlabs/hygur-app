package mail

import (
	"strings"

	"github.com/hygur/sidecar/internal/extract"
)

// accountingKeywords flag emails as financially material.
// Detection is case-insensitive on subject + body. Word-boundary checks avoid
// false positives like "facturation interne" inside a sales newsletter.
var accountingKeywords = []string{
	"tva", "vat", "btw",
	"facture", "factuur", "invoice",
	"virement", "paiement", "payment",
	"déclaration", "precompte", "précompte",
	"échéance", "echeance",
	"contrat", "juridique", "légal", "legal",
	"paie", "payroll", "salaire",
	"due",
}

// accountingDomains is a hardcoded list of senders known to send accounting
// or HR-related correspondence. Extending this list is a config follow-up.
var accountingDomains = []string{
	"securex.be",
	"partena.be",
	"acerta.be",
	"sdworx.be",
}

// detectHighPriority returns true if the email is likely accounting / legal / HR.
// It triggers on either an accounting keyword in subject+body OR a known
// accounting domain in the sender address.
func detectHighPriority(subject, body, fromAddr string) (bool, []string) {
	hits := make([]string, 0, 4)
	corpus := strings.ToLower(subject + " " + body)
	for _, kw := range accountingKeywords {
		if containsWord(corpus, kw) {
			hits = append(hits, kw)
		}
	}

	from := strings.ToLower(fromAddr)
	for _, d := range accountingDomains {
		if strings.Contains(from, "@"+d) || strings.HasSuffix(from, "."+d) || strings.HasSuffix(from, "@"+d) {
			hits = append(hits, "domain:"+d)
		}
	}

	return len(hits) > 0, hits
}

// containsWord checks whether the lowercased text contains the lowercased
// keyword as a whole word (alphanumeric or accented letter on both sides
// would disqualify). Cheap substring + boundary check; avoids the cost of
// compiling a regex per keyword.
func containsWord(text, kw string) bool {
	for i := 0; i <= len(text)-len(kw); i++ {
		if text[i:i+len(kw)] != kw {
			continue
		}
		if i > 0 && isWordByte(text[i-1]) {
			continue
		}
		end := i + len(kw)
		if end < len(text) && isWordByte(text[end]) {
			continue
		}
		return true
	}
	return false
}

// isWordByte returns true for ASCII alphanumeric and underscore. UTF-8
// continuation bytes (0x80-0xBF) are also treated as "word-ish" so that
// accented characters in French don't create spurious word boundaries.
func isWordByte(b byte) bool {
	switch {
	case b >= '0' && b <= '9':
		return true
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b == '_':
		return true
	case b >= 0x80:
		return true
	}
	return false
}

// enrichMetadataWithTier1 runs Tier 1 extraction on the email body and layers
// mail-specific high_priority detection (accounting keywords + sender domain)
// on top. Mutates metadata in place. Returns the raw Tier1Entities and the
// high-priority flag so callers (e.g. the indexer) can decide whether to
// emit downstream events without re-running the regex.
func enrichMetadataWithTier1(metadata map[string]any, subject, body, fromAddr string) (extract.Tier1Entities, bool) {
	tier1 := extract.EnrichMetadataWithTier1(metadata, body)

	highPriority, hits := detectHighPriority(subject, body, fromAddr)
	if highPriority {
		metadata["high_priority"] = true
		metadata["accounting_keywords"] = hits
	}
	return tier1, highPriority
}
