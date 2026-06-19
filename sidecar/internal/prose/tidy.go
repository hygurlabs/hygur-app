// Package prose holds the deterministic, model-independent cleanup pass applied
// to Hygur's user-facing prose outputs (mail drafts, chronicle entries, standing
// positions, follow-up report). It is the Couche B of the writing-quality work:
// where the voice block (llm.ProseVoiceGuidance) only *asks* the model to behave,
// Tidy *guarantees* a baseline regardless of which model ran — strip a meta
// preamble the model was told not to write, and apply French typography. It is
// deliberately conservative: it never rewrites meaning, only punctuation/spacing
// and a leading boilerplate line. The user re-reads every draft anyway.
package prose

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/abadojack/whatlanggo"
)

// enabled is a process-wide kill-switch (HYGUR_PROSE_TIDY), default on, set once
// at startup. When off, Tidy is an identity pass.
var enabled = true

// SetEnabled wires the config kill-switch. Called once from main; not safe to
// toggle concurrently with Tidy (it isn't, in practice — set at boot).
func SetEnabled(v bool) { enabled = v }

// Enabled reports the current kill-switch state.
func Enabled() bool { return enabled }

const (
	nbsp     = " " // no-break space — before ':'
	thinNbsp = " " // narrow no-break space — before ';' '!' '?' and inside guillemets
)

// Tidy cleans one prose output. lang is "fr"/"french", "en"/"english", or ""
// (auto-detect). French typography only runs when the text is French; for any
// other language only the (language-safe) preamble strip applies. Returns the
// input unchanged when the kill-switch is off or the text is blank.
func Tidy(text, lang string) string {
	if !enabled || strings.TrimSpace(text) == "" {
		return text
	}
	return tidy(text, lang)
}

// tidy is the pure transform (no kill-switch), so golden tests are deterministic.
func tidy(text, lang string) string {
	text = stripPreamble(text)
	if isFrench(lang, text) {
		text = frenchTypography(text)
	}
	return text
}

func isFrench(lang, text string) bool {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "fr", "french", "fra", "français", "francais":
		return true
	case "en", "english", "eng":
		return false
	default:
		return detectFrench(text)
	}
}

// preambleRe matches a single leading boilerplate lead-in line that ends with a
// colon and is followed by the real content — exactly the "Here is the reply:"
// / "Voici votre brouillon :" pattern small models re-add despite the prompt.
// Anchored, case-insensitive, single line; the trailing colon at line end is the
// discriminator that keeps real sentences (e.g. "Here is the situation: we owe…")
// untouched.
var preambleRe = regexp.MustCompile(`(?i)^[ \t]*(?:here(?:'s| is| are)|sure|certainly|of course|i(?:'d| would) be happy to|below is|voici|voilà|voila|bien sûr|bien sur|avec plaisir|bien entendu)[^\n:]*:[ \t\x{00A0}\x{202F}]*\n+`)

func stripPreamble(text string) string {
	loc := preambleRe.FindStringIndex(text)
	if loc == nil {
		return text
	}
	if loc[1]-loc[0] > 60 {
		return text // too long to be a boilerplate lead-in — likely a real sentence
	}
	rest := text[loc[1]:]
	if strings.TrimSpace(rest) == "" {
		return text // nothing of substance after — the "preamble" was the content
	}
	return rest
}

var (
	apostropheRe = regexp.MustCompile(`'`)
	dquotePairRe = regexp.MustCompile(`"([^"\n]*)"`)
	openGuillRe  = regexp.MustCompile(`«[ \t\x{00A0}\x{202F}]*`)
	closeGuillRe = regexp.MustCompile(`[ \t\x{00A0}\x{202F}]*»`)
	// before ';' '!' '?': require a real preceding char and not another punctuation,
	// so "?!" stays "?!" and we don't act inside placeholders.
	beforeStrongRe = regexp.MustCompile(`([^\s;:!?«»\x{00A0}\x{202F}])[ \t\x{00A0}\x{202F}]*([;!?])`)
	// before ':': only when a letter precedes and whitespace follows (sentence
	// colon), so "14:30" and "http(s)://" are left alone (URLs are masked anyway).
	beforeColonRe   = regexp.MustCompile(`(\p{L})[ \t\x{00A0}\x{202F}]*:([ \t\x{00A0}\x{202F}\n]|$)`)
	doubleSpaceRe   = regexp.MustCompile(`  +`)
	fencedCodeRe    = regexp.MustCompile("(?s)```.*?```")
	inlineCodeRe    = regexp.MustCompile("`[^`\n]*`")
	urlRe           = regexp.MustCompile(`https?://\S+`)
	protectPatterns = []*regexp.Regexp{fencedCodeRe, inlineCodeRe, urlRe}
)

// frenchTypography applies French punctuation spacing and curly marks, leaving
// fenced/inline code and URLs untouched (masked out, transformed, restored).
func frenchTypography(text string) string {
	masked, stash := protect(text)

	masked = apostropheRe.ReplaceAllString(masked, "’")
	masked = dquotePairRe.ReplaceAllString(masked, "«"+thinNbsp+"$1"+thinNbsp+"»")
	masked = openGuillRe.ReplaceAllString(masked, "«"+thinNbsp)
	masked = closeGuillRe.ReplaceAllString(masked, thinNbsp+"»")
	masked = beforeStrongRe.ReplaceAllString(masked, "$1"+thinNbsp+"$2")
	masked = beforeColonRe.ReplaceAllString(masked, "$1"+nbsp+":$2")
	masked = doubleSpaceRe.ReplaceAllString(masked, " ")

	return restore(masked, stash)
}

// protect replaces protected spans with `\x00N\x00` sentinels (null-delimited, so
// no typography rule can match them and no real text collides).
func protect(text string) (string, []string) {
	var stash []string
	for _, re := range protectPatterns {
		text = re.ReplaceAllStringFunc(text, func(m string) string {
			ph := "\x00" + strconv.Itoa(len(stash)) + "\x00"
			stash = append(stash, m)
			return ph
		})
	}
	return text, stash
}

func restore(text string, stash []string) string {
	for i := len(stash) - 1; i >= 0; i-- {
		text = strings.Replace(text, "\x00"+strconv.Itoa(i)+"\x00", stash[i], 1)
	}
	return text
}

// detectFrench delegates the FR/EN gate to a trigram language detector
// (whatlanggo) rather than a hand-rolled word list: more robust on real prose
// and trivially extensible if more languages are ever targeted. Fail-safe by
// construction — anything not detected as French skips the French typography,
// which is the only behaviour that could otherwise corrupt non-French text.
func detectFrench(text string) bool {
	return whatlanggo.Detect(text).Lang == whatlanggo.Fra
}
