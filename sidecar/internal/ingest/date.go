package ingest

import (
	"time"
)

// dateFormats lists common formats found in document metadata.
// Ordered from most specific to least specific.
var dateFormats = []string{
	time.RFC3339,
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05.000Z",
	"2006-01-02T15:04:05-07:00",
	"2006-01-02 15:04:05",
	"2006-01-02",
	"02/01/2006",
	"01/02/2006",
	// PDF /CreationDate format: D:YYYYMMDDHHmmSSOHH'mm'
	"D:20060102150405-07'00'",
	"D:20060102150405+07'00'",
	"D:20060102150405Z",
	"D:20060102150405",
	"D:20060102",
}

// parseDate tries to parse a string as a date using known formats.
// Returns the parsed time in UTC, or zero on failure.
func parseDate(s string) time.Time {
	for _, format := range dateFormats {
		if t, err := time.Parse(format, s); err == nil && !t.IsZero() {
			return t.UTC()
		}
	}
	return time.Time{}
}

// CanonicalDate extracts the best content date from document metadata.
//
// Priority:
//  1. metadata["canonical_date"] — already set, pass-through
//  2. metadata["date"] / metadata["doc_date"] — frontmatter or parser-extracted date
//  3. metadata["created"] / metadata["published"] — alt frontmatter keys
//  4. metadata["file_mtime"] — OS mtime passed by the ingestor
//  5. fallback — caller-provided default (usually time.Now() or item.CreatedAt)
//
// The returned time is always in UTC.
func CanonicalDate(meta Metadata, fallback time.Time) time.Time {
	if meta == nil {
		return fallback.UTC()
	}

	for _, key := range []string{"canonical_date", "date", "doc_date", "created", "published", "file_mtime"} {
		val, ok := meta[key]
		if !ok {
			continue
		}
		switch v := val.(type) {
		case time.Time:
			if !v.IsZero() {
				return v.UTC()
			}
		case string:
			if t := parseDate(v); !t.IsZero() {
				return t
			}
		}
	}

	return fallback.UTC()
}
