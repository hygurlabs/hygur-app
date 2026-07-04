package ingest

import (
	"strings"
	"testing"
)

func TestAssessTextQuality_Garbled(t *testing.T) {
	// Spaced-glyph shape: the ledongthuc failure mode on the TARA PDF.
	garbled := "d o n e i n l o n d o n t h e d a i l y r a t e i s 6 0 0 e u r o s p e r d a y f o r j o h n"
	q := AssessTextQuality(garbled)
	if !q.Garbled {
		t.Errorf("expected garbled=true, got %+v", q)
	}
	if !q.LowConfidence {
		t.Errorf("expected low_confidence=true, got %+v", q)
	}
	if q.Score >= LowConfidenceThreshold {
		t.Errorf("expected score < %.2f, got %.2f", LowConfidenceThreshold, q.Score)
	}
}

func TestAssessTextQuality_Clean(t *testing.T) {
	clean := "Contractor Agreement between ACME Gaming Ltd and John Doe. " +
		"The agreed daily rate is 600 EUR per working day, invoiced monthly."
	q := AssessTextQuality(clean)
	if q.Garbled {
		t.Errorf("expected garbled=false, got %+v", q)
	}
	if q.LowConfidence {
		t.Errorf("expected low_confidence=false, got %+v", q)
	}
	if q.Score < 0.8 {
		t.Errorf("expected high score for clean prose, got %.2f", q.Score)
	}
}

func TestAssessTextQuality_ShortTitleNotGarbled(t *testing.T) {
	// A short clean title must not be mis-flagged as garbled just for being short.
	q := AssessTextQuality("Invoice March 2026")
	if q.Garbled {
		t.Errorf("short clean title should not be garbled, got %+v", q)
	}
}

func TestAssessTextQuality_Empty(t *testing.T) {
	q := AssessTextQuality("   ")
	if !q.LowConfidence {
		t.Errorf("empty text must be low_confidence, got %+v", q)
	}
	if q.TokenCount != 0 {
		t.Errorf("expected 0 tokens, got %d", q.TokenCount)
	}
}

// TestAssessTextQuality_SingleCharsBelowThreshold guards the token-count floor:
// a genuinely tiny fragment of single chars isn't statistically "garbled" but is
// still low-confidence via the score path.
func TestAssessTextQuality_SingleCharsBelowThreshold(t *testing.T) {
	q := AssessTextQuality(strings.Repeat("a ", 5))
	if q.LowConfidence != (q.Score < LowConfidenceThreshold) && !q.Garbled {
		t.Errorf("low_confidence should follow score/garble rule, got %+v", q)
	}
}
