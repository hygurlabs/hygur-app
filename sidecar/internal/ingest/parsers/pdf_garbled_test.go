package parsers

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/hygur/sidecar/internal/ingest"
)

// helveticaWidths are the AFM advance widths (units/1000) for the glyphs used by
// the synthetic fixtures — enough to place each glyph at its natural position so
// pdftotext reconstructs words cleanly.
var helveticaWidths = map[rune]int{
	' ': 278, ':': 278, '/': 278, '.': 278, ',': 278,
	'0': 556, '1': 556, '2': 556, '6': 556,
	'A': 667, 'C': 722, 'D': 722, 'E': 667, 'F': 611, 'G': 778, 'I': 278, 'J': 500,
	'L': 556, 'M': 833, 'N': 722, 'O': 778, 'R': 722, 'T': 611, 'U': 722, 'Y': 667,
	'a': 556, 'b': 556, 'c': 500, 'd': 556, 'e': 556, 'f': 278, 'g': 556, 'h': 556,
	'i': 222, 'j': 222, 'l': 222, 'm': 833, 'n': 556, 'o': 556, 'p': 556, 'r': 333,
	's': 500, 't': 278, 'u': 556, 'y': 500,
}

// fixtureLines are REDACTED/synthetic contract lines — fake parties (ACME GAMING
// LTD / John Doe) and dates, NOT the founder's real TARA contract, which is
// never committed. The daily rate 600 EUR mirrors the real fact under test.
var fixtureLines = []struct {
	text string
	y    int
}{
	{"CONTRACTOR AGREEMENT", 720},
	{"For the attention of John Doe", 700},
	{"Company: ACME GAMING LTD", 680},
	{"Daily rate: 600 EUR", 660},
	{"Desired start date: 01/02/2026", 640},
}

func escapePDFText(c rune) string {
	if c == '(' || c == ')' || c == '\\' {
		return "\\" + string(c)
	}
	return string(c)
}

// buildPDF assembles a minimal single-page PDF from a content stream.
func buildPDF(content string) []byte {
	stream := []byte(content)
	objs := [][]byte{
		[]byte("<< /Type /Catalog /Pages 2 0 R >>"),
		[]byte("<< /Type /Pages /Kids [3 0 R] /Count 1 >>"),
		[]byte("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>"),
		[]byte(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)),
		[]byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"),
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n", i+1)
		b.Write(o)
		b.WriteString("\nendobj\n")
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n", len(objs)+1)
	b.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF", len(objs)+1, xref)
	return b.Bytes()
}

// syntheticSpacedGlyphPDF reproduces the ledongthuc garble mode: each glyph is
// drawn in its OWN text object (BT … Tm … Tj … ET), exactly like the real Deel
// contract (Type0/CIDFontType2, one glyph per BT). ledongthuc emits a space
// between every text object → "d a i l y  r a t e"; pdftotext reconstructs words
// from the glyph geometry.
func syntheticSpacedGlyphPDF() []byte {
	var c strings.Builder
	for _, ln := range fixtureLines {
		x := 72.0
		for _, ch := range ln.text {
			fmt.Fprintf(&c, "BT /F1 12 Tf 1 0 0 1 %.2f %d Tm (%s) Tj ET\n", x, ln.y, escapePDFText(ch))
			w := helveticaWidths[ch]
			if w == 0 {
				w = 556
			}
			x += float64(w) * 12.0 / 1000.0
		}
	}
	return buildPDF(c.String())
}

// syntheticCleanPDF draws the same text as ordinary runs (one BT per line) — a
// well-behaved text PDF both extractors handle. Guards against regressions.
func syntheticCleanPDF() []byte {
	var c strings.Builder
	c.WriteString("BT /F1 12 Tf\n")
	for _, ln := range fixtureLines {
		fmt.Fprintf(&c, "1 0 0 1 72 %d Tm (%s) Tj\n", ln.y, ln.text)
	}
	c.WriteString("ET\n")
	return buildPDF(c.String())
}

func popplerAvailable() bool {
	_, err := exec.LookPath("pdftotext")
	return err == nil
}

// TestLedongthucGarblesSpacedGlyphs pins the OLD failure mode: the pure-Go
// extractor produces character-per-character garbage on the spaced-glyph PDF,
// and the quality heuristic flags it low_confidence.
func TestLedongthucGarblesSpacedGlyphs(t *testing.T) {
	data := syntheticSpacedGlyphPDF()
	text, _, _, err := extractViaLedongthuc(context.Background(), data)
	if err != nil {
		t.Fatalf("ledongthuc extraction failed: %v", err)
	}
	q := ingest.AssessTextQuality(text)
	if !q.Garbled {
		t.Errorf("expected ledongthuc output to be flagged garbled; got quality %+v\ntext=%q", q, text)
	}
	if !q.LowConfidence {
		t.Errorf("expected low_confidence for garbled ledongthuc output; got %+v", q)
	}
	if q.OneCharRatio < 0.9 {
		t.Errorf("expected near-total single-char tokens (spaced glyphs); got ratio %.3f", q.OneCharRatio)
	}
}

// TestParsePrefersPdftotextOnGarbled is the core before/after gate: on the exact
// same bytes that garble under ledongthuc, Parse (pdftotext primary) returns
// CLEAN text with the rate/parties intact and a HIGH confidence signal.
func TestParsePrefersPdftotextOnGarbled(t *testing.T) {
	if !popplerAvailable() {
		t.Skip("pdftotext (poppler) not installed; skipping the clean-extraction gate")
	}
	data := syntheticSpacedGlyphPDF()
	text, meta, err := NewPDFParserTextOnly().Parse(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if got := meta["extract_method"]; got != "pdftotext" {
		t.Errorf("expected extract_method=pdftotext, got %v", got)
	}
	if low, _ := meta["extract_low_confidence"].(bool); low {
		t.Errorf("expected high confidence on clean pdftotext output; meta=%v", meta)
	}
	q := ingest.AssessTextQuality(text)
	if q.Garbled {
		t.Errorf("pdftotext output should not be garbled; got %+v\ntext=%q", q, text)
	}
	// The load-bearing facts must survive cleanly.
	for _, want := range []string{"Daily rate: 600 EUR", "ACME GAMING LTD", "John Doe"} {
		if !strings.Contains(text, want) {
			t.Errorf("clean extraction missing %q\ngot: %q", want, text)
		}
	}
}

// TestParseCleanPDFNoRegression ensures a well-behaved text PDF extracts cleanly
// with high confidence (whichever extractor wins), so the change does not
// regress the common case.
func TestParseCleanPDFNoRegression(t *testing.T) {
	data := syntheticCleanPDF()
	text, meta, err := NewPDFParserTextOnly().Parse(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if low, _ := meta["extract_low_confidence"].(bool); low {
		t.Errorf("clean PDF should be high confidence; meta=%v text=%q", meta, text)
	}
	if !strings.Contains(text, "600 EUR") {
		t.Errorf("expected clean extraction to contain the rate; got %q", text)
	}
}
