package identifier

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"12.34.56-789.01":  "12345678901",
		"12.34.56:789-01":  "12345678901", // colon separator — no list to miss it
		"BE0123.456.789":   "be0123456789",
		"12 34 56 789 01":  "12345678901",
		"99-00-1234/001":   "99001234001",
		"(04) 99-88-77-66": "0499887766",
		"Plain Text":       "plaintext",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractQuery(t *testing.T) {
	// Identifier queries → canonical key + true.
	ids := map[string]string{
		"12345678901":         "12345678901",
		"12.34.56-789.01":     "12345678901",
		"invoice 12345678901": "12345678901",
		"99-00-1234/001":      "99001234001",
		"0499887766":          "0499887766",
		"BE0123.456.789":      "be0123456789",
	}
	for q, want := range ids {
		got, ok := ExtractQuery(q)
		if !ok || got != want {
			t.Errorf("ExtractQuery(%q) = (%q,%v), want (%q,true)", q, got, ok, want)
		}
	}

	// Prose / short-code / DATE queries → not an identifier (stay on the semantic path).
	// A normalized 8-digit date must not hijack a semantic query.
	for _, q := range []string{
		"numéro national", "0x0800 SRL", "facture de mars", "quelle est la TVA", "2024",
		"mails du 2024-03-15", "réunion du 15032024", "20240315",
	} {
		if got, ok := ExtractQuery(q); ok {
			t.Errorf("ExtractQuery(%q) = (%q,true), want (_,false)", q, got)
		}
	}
}
