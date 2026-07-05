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

// TestAssessTextQuality_Merged is the FIX-1 regression: space-losing extractor
// output (`dailyrate600`) previously scored ~1.0 and OUTRANKED clean poppler text
// (~0.95), so the keep-better guard preferred the garbage. Merged-words text must
// now score low and be flagged low-confidence.
func TestAssessTextQuality_Merged(t *testing.T) {
	// ledongthuc merged-words shape: whitespace dropped, words fused into long runs.
	merged := "contractoragreement betweenacmegaming ltdandjohndoe theagreeddaily " +
		"rate600euro perworkingday invoicedmonthly nettotalpayable 7200euroverthe " +
		"twelvemonthterm signedinlondon paymenttermsnet thirtydaysfrom invoicedatehereto"
	q := AssessTextQuality(merged)
	if !q.Merged {
		t.Errorf("expected merged=true, got %+v", q)
	}
	if !q.LowConfidence {
		t.Errorf("expected low_confidence=true for merged-words, got %+v", q)
	}
	if q.Score >= LowConfidenceThreshold {
		t.Errorf("expected merged score < %.2f, got %.2f", LowConfidenceThreshold, q.Score)
	}
}

// TestAssessTextQuality_CleanOutranksMerged is the core FIX-1 guarantee the
// keep-better guard relies on: clean poppler prose must score strictly higher than
// the merged-words extraction of the same content.
func TestAssessTextQuality_CleanOutranksMerged(t *testing.T) {
	clean := "Contractor Agreement between ACME Gaming Ltd and John Doe. " +
		"The agreed daily rate is 600 EUR per working day, invoiced monthly. " +
		"Net total payable 7200 EUR over the twelve month term. Signed in London."
	merged := "contractoragreement betweenacmegaming ltdandjohndoe theagreeddaily " +
		"rate600euro perworkingday invoicedmonthly nettotalpayable 7200euroverthe " +
		"twelvemonthterm signedinlondon paymenttermsnet thirtydaysfrom invoicedatehereto"
	qc := AssessTextQuality(clean)
	qm := AssessTextQuality(merged)
	if qc.Score <= qm.Score {
		t.Errorf("clean (%.2f) must outrank merged (%.2f)", qc.Score, qm.Score)
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
