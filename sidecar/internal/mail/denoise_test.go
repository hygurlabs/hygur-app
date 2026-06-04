package mail

import (
	"strings"
	"testing"
)

// Zero-width / formatting chars denoiseMailBody must strip, built from code
// points so the source stays pure ASCII (a literal U+FEFF is an illegal BOM):
// ZWSP, ZWNJ, ZWJ, word-joiner, BOM/ZWNBSP, soft hyphen.
var zeroWidthRunes = []rune{0x200B, 0x200C, 0x200D, 0x2060, 0xFEFF, 0x00AD}

// denoiseMailBody must strip zero-width/formatting chars and leaked CSS blocks
// while preserving real prose, including non-CSS braces.
func TestDenoiseMailBody_StripsNoiseKeepsContent(t *testing.T) {
	noise := string([]rune{0x200C, 0x200B})
	in := "Bonjour" + noise + "Monsieur,\n" +
		"body { font-family: NotoSans; padding-left: 8px; }\n" +
		"Nous vous remercions. Votre facture de 12,50 EUR est prete."

	out := denoiseMailBody(in)

	if strings.ContainsAny(out, string(zeroWidthRunes)) {
		t.Errorf("zero-width characters survived: %q", out)
	}
	if strings.Contains(out, "font-family") || strings.ContainsAny(out, "{}") {
		t.Errorf("leaked CSS survived: %q", out)
	}
	for _, want := range []string{"Bonjour", "Monsieur", "remercions", "12,50", "prete"} {
		if !strings.Contains(out, want) {
			t.Errorf("real content %q was eaten: %q", want, out)
		}
	}
}

// Braces without CSS declaration syntax (no prop: value;) are prose - keep them.
func TestDenoiseMailBody_PreservesNonCSSBraces(t *testing.T) {
	in := "Le tarif {voir annexe} reste valable."
	if out := denoiseMailBody(in); !strings.Contains(out, "{voir annexe}") {
		t.Errorf("non-CSS braces were removed: %q", out)
	}
}
