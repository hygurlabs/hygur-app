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
	// Merged reports the merged-words shape (abnormally long average token length
	// with near-zero inter-word whitespace) — the space-losing extractor failure
	// mode (`dailyrate600`), the mirror image of the spaced-glyph mode.
	Merged bool
	// OneCharRatio is the fraction of whitespace-separated tokens that are a
	// single rune. AvgTokenLen is the mean token length in runes. WhitespaceRatio
	// is the fraction of characters that are whitespace. TokenCount is the number
	// of tokens. Exposed for observability/tuning.
	OneCharRatio    float64
	AvgTokenLen     float64
	WhitespaceRatio float64
	TokenCount      int
}

// AssessTextQuality scores extracted text for garbage. It is language-agnostic
// and depends only on token shape, so it never needs the source document.
//
// Two symmetric extractor failures produce untrustworthy text, and BOTH must
// score low so the keep-better guard prefers clean poppler text over either:
//
//   - SPACED-GLYPH (`d a i l y` → "d","a","i","l","y"): a high one-character-token
//     ratio and a tiny average token length. Clean prose has few single-char
//     tokens and an average token length around 4-6.
//   - MERGED-WORDS (`dailyrate600` → one long run): the mirror image — whitespace
//     is lost, so tokens are abnormally LONG and the inter-word whitespace ratio
//     collapses toward zero. Before this was penalized it scored ~1.0 and OUTRANKED
//     clean poppler text (~0.95), so the guard kept the garbage. It must score low.
//
// The score is the product of three shape factors in 0..1, so a failure on any
// axis drags the score down: (1) the single-char penalty (spaced-glyph), (2) a
// token-length plausibility curve that rewards normal word length but decays for
// abnormally long merged runs, and (3) an inter-word whitespace factor that
// punishes near-zero whitespace. Clean prose scores ~0.95; both failure modes
// score well under the low-confidence threshold.
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

	// Inter-word whitespace ratio: whitespace runes over all runes. Clean prose is
	// ~0.15; merged-words text collapses toward 0 (spaces were dropped). Spaced-glyph
	// text is high (~0.5) — that mode is caught by oneRatio, not here.
	totalChars := utf8.RuneCountInString(text)
	wsRunes := 0
	for _, r := range text {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			wsRunes++
		}
	}
	wsRatio := 0.0
	if totalChars > 0 {
		wsRatio = float64(wsRunes) / float64(totalChars)
	}

	// Garble (spaced-glyph) requires enough tokens to be statistically meaningful —
	// a 3-word title is not "garbled" just because it is short.
	garbled := n >= 20 && (oneRatio > 0.4 || avgLen < 2.0)
	// Merged-words: abnormally long average token AND near-zero whitespace, with
	// enough content to be meaningful. The symmetric counterpart of garbled.
	merged := n >= 8 && avgLen > 12.0 && wsRatio < 0.06

	// Score = single-char penalty × length-plausibility curve × whitespace factor,
	// clamped to 0..1. Clean prose (avgLen≈5, oneRatio≈0.05, wsRatio≈0.15) → ~0.95;
	// spaced-glyph (avgLen≈1, oneRatio≈1.0) → ~0; merged-words (avgLen≫12,
	// wsRatio≈0.03) → well under the threshold, so clean poppler always outranks it.
	score := math.Max(0, math.Min(1, (1-oneRatio)*lenPlausibility(avgLen)*whitespaceFactor(wsRatio)))

	return TextQuality{
		Score:           math.Round(score*100) / 100,
		Garbled:         garbled,
		Merged:          merged,
		LowConfidence:   garbled || merged || score < LowConfidenceThreshold,
		OneCharRatio:    oneRatio,
		AvgTokenLen:     avgLen,
		WhitespaceRatio: wsRatio,
		TokenCount:      n,
	}
}

// lenPlausibility maps an average token length to a 0..1 plausibility weight: it
// rewards normal word length (a plateau over ~4–8 runes → 1.0), penalizes the
// spaced-glyph short end (→0 near 1 rune) and, critically, penalizes the
// merged-words long end so a space-less run cannot score as clean prose (→0 by
// ~24 runes). This is what makes clean poppler outrank merged ledongthuc output.
func lenPlausibility(avgLen float64) float64 {
	switch {
	case avgLen <= 1:
		return 0
	case avgLen < 4:
		return (avgLen - 1) / 3 // 1→0, 4→1 : spaced-glyph / very short
	case avgLen <= 8:
		return 1 // normal prose plateau
	case avgLen >= 24:
		return 0 // fully merged run
	default:
		return (24 - avgLen) / 16 // 8→1, 12→0.75, 16→0.5, 24→0 : merged-words decay
	}
}

// whitespaceFactor punishes near-zero inter-word whitespace, the tell of the
// merged-words mode. Clean prose (~0.15) and spaced-glyph (~0.5) map to 1.0; a
// space-less run (→0) is driven toward 0.
func whitespaceFactor(wsRatio float64) float64 {
	return math.Max(0, math.Min(1, wsRatio/0.08))
}
