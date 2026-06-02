package ingest

import "testing"

func TestExtractDomainTagStripsAngleBrackets(t *testing.T) {
	cases := map[string]string{
		"user@edf.fr":            "edf.fr",
		"user@edf.fr>":           "edf.fr",   // angle-bracketed sender leaked the '>'
		"Name <user@edf.fr>":     "edf.fr",
		"a@scaleway.com> (x)":    "scaleway.com",
		"user@gmail.com":         "",         // common provider skipped
		"":                       "",
		"garbage":                "",
	}
	for in, want := range cases {
		if got := extractDomainTag(in); got != want {
			t.Errorf("extractDomainTag(%q) = %q, want %q", in, got, want)
		}
	}
}
