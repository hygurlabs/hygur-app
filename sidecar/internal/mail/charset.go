package mail

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

// DecodeCharset converts a mail part's bytes to a valid UTF-8 string using the
// declared charset. Without it, an ISO-8859-1 / Windows-1252 body (common in
// French/Belgian business mail) leaks raw 8-bit bytes — e.g. "é" (0xE9) — that
// are invalid UTF-8 and surface as the replacement character "" in the UI.
//
// Behaviour:
//   - utf-8 / us-ascii / empty charset: kept as-is when already valid UTF-8;
//     otherwise (a mislabeled 8-bit body) recovered via Windows-1252, a Latin-1
//     superset that maps every byte and so never fails.
//   - a known charset (iso-8859-1, windows-1252, …): decoded via that encoding.
//   - an unknown charset: bytes returned untouched.
func DecodeCharset(b []byte, charset string) string {
	cs := strings.ToLower(strings.TrimSpace(charset))
	switch cs {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		if utf8.Valid(b) {
			return string(b)
		}
		if out, _, err := transform.Bytes(charmap.Windows1252.NewDecoder(), b); err == nil {
			return string(out)
		}
		return string(b)
	}
	enc, err := htmlindex.Get(cs)
	if err != nil || enc == nil {
		return string(b) // unknown charset — leave the bytes untouched
	}
	out, _, err := transform.Bytes(enc.NewDecoder(), b)
	if err != nil {
		return string(b)
	}
	return string(out)
}
