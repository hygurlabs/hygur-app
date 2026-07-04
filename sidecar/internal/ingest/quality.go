package ingest

import (
	"math"
	"strings"
	"unicode/utf8"
)

// LowConfidenceThreshold is the extraction-quality score below which an
// extraction is flagged low_confidence: the engine must not trust a monetary
// value (or any fact) mined from it, and an async re-extraction/OCR pass should
// revisit it. Chosen so clean prose scores well above it and spaced-glyph
// garbage scores near zero.
const LowConfidenceThreshold = 0.5

// TextQuality is a per-extraction quality signal. It is the "net" the ingestion
// diagnostic called for: a cheap, deterministic anti-garbage heuristic so the
// engine can KNOW an extraction is untrustworthy instead of trusting it blindly
// (the TARA « Contractor Agreement » failure mode: ledongthuc emitted
// character-per-character garbage `d a i l y  r a t e`, poisoning the Tier1
// amount regex into `0 EUR` with no signal that the value was wrong).
type TextQuality struct {
	// Score is a 0..1 extraction-confidence estimate (higher = cleaner).
	Score float64
	// Garbled reports the spaced-glyph shape (a high ratio of single-character
	// tokens / a near-1 average token length) — the ledongthuc failure mode.
	Garbled bool
	// LowConfidence is the actionable flag: Garbled or Score < threshold.
	LowConfidence bool
	// OneCharRatio is the fraction of whitespace-separated tokens that are a
	// single rune. AvgTokenLen is the mean token length in runes. TokenCount is
	// the number of tokens. Exposed for observability/tuning.
	OneCharRatio float64
	AvgTokenLen  float64
	TokenCount   int
}

// AssessTextQuality scores extracted text for garbage. It is language-agnostic
// and depends only on token shape, so it never needs the source document.
//
// The spaced-glyph artifact tokenizes as almost-all single characters
// (`d a i l y` → "d","a","i","l","y"), so a high one-character-token ratio and a
// tiny average token length are a reliable garble signature. Clean prose has an
// average token length around 4-6 and few single-char tokens.
func AssessTextQuality(text string) TextQuality {
	fields := strings.Fields(text)
	n := len(fields)
	if n == 0 {
		return TextQuality{Score: 0, Garbled: false, LowConfidence: true}
	}

	oneChar := 0
	totalRunes := 0
	for _, f := range fields {
		rc := utf8.RuneCountInString(f)
		if rc == 1 {
			oneChar++
		}
		totalRunes += rc
	}
	oneRatio := float64(oneChar) / float64(n)
	avgLen := float64(totalRunes) / float64(n)

	// Garble requires enough tokens to be statistically meaningful — a 3-word
	// title is not "garbled" just because it is short.
	garbled := n >= 20 && (oneRatio > 0.4 || avgLen < 2.0)

	// Score blends the single-char penalty with a token-length reward, clamped to
	// 0..1. Clean prose (avgLen≈5, oneRatio≈0.05) → ~0.95; spaced-glyph garbage
	// (avgLen≈1, oneRatio≈1.0) → ~0.
	lenFactor := math.Min(1, avgLen/4.0)
	score := math.Max(0, math.Min(1, (1-oneRatio)*lenFactor))

	return TextQuality{
		Score:         math.Round(score*100) / 100,
		Garbled:       garbled,
		LowConfidence: garbled || score < LowConfidenceThreshold,
		OneCharRatio:  oneRatio,
		AvgTokenLen:   avgLen,
		TokenCount:    n,
	}
}
