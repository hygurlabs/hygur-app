package ingest

import (
	"regexp"
	"strings"
	"unicode"
)

// multiSpaceRegex matches two or more consecutive whitespace characters.
var multiSpaceRegex = regexp.MustCompile(`\s{2,}`)

// NormalizeText normalizes text for full-text search.
// It performs the following transformations:
//   - Converts to lowercase
//   - Trims leading and trailing whitespace
//   - Collapses multiple consecutive whitespace characters into a single space
//   - Removes control characters (except newlines which become spaces)
func NormalizeText(s string) string {
	if s == "" {
		return ""
	}

	// Replace control characters with spaces (preserve newlines as spaces)
	var builder strings.Builder
	builder.Grow(len(s))

	for _, r := range s {
		if unicode.IsControl(r) {
			if r == '\n' || r == '\r' || r == '\t' {
				builder.WriteRune(' ')
			}
			// Skip other control characters
			continue
		}
		builder.WriteRune(r)
	}

	// Convert to lowercase
	text := strings.ToLower(builder.String())

	// Collapse multiple spaces
	text = multiSpaceRegex.ReplaceAllString(text, " ")

	// Trim leading and trailing whitespace
	text = strings.TrimSpace(text)

	return text
}
