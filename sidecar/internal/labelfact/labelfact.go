// Package labelfact is the GENERIC, DETERMINISTIC label→value extractor (Plan B). Where
// recognize types a value by its CHECKSUM (family A: national number, enterprise/VAT, IBAN —
// self-verifying), labelfact types a value by the LABEL written next to it (family B: DUNS,
// SIRET, EIN, order/reference numbers…). This ends the per-type whack-a-mole: any labelled
// identifier is captured under its own normalized label, so a "DUNS" is stored as id_duns and
// retrieved as id_duns — never coerced into a wrong enterprise_number.
//
// It is precision-first and FAILS CLOSED: a value is bound to a label only when the pairing is
// syntactically unambiguous (a label immediately precedes exactly one identifier-grade value,
// with a separator), or — for the whole document — when there is exactly ONE value and ONE
// label. On any ambiguity (several values under one label, several distinct labels, a value
// with no label) it extracts NOTHING. Better to miss than to mislabel. No LLM, no embeddings.
//
// Output is []recognize.Typed with Type = the normalized label, so label-facts flow through the
// SAME identifier-graph machinery (entity_mentions id_<label>, entity_identifier_link proximity)
// as checksum identifiers — no fourth store. Checksum-type labels are deliberately NOT emitted
// here: recognize owns and VALIDATES those (skipping them keeps family A pure and stops an
// invalid "numéro national: 000…" from polluting the checksum type).
package labelfact

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/hygur/sidecar/internal/identifier"
	"github.com/hygur/sidecar/internal/recognize"
)

// maxGap bounds the label→value gap: a label binds only a value that sits right after it (across a
// separator and at most a couple of connector words), never one merely nearby in prose.
const maxGap = 48

// foldText lowercases s and strips accents (NFD, dropping combining marks) so "Numéro" and
// "numero" compare equal. Mirrors the folding used elsewhere (tools.foldText, identity).
func foldText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// aliasTable maps a folded, space-normalized label phrase to its canonical id_type key. It
// dealiases (a) checksum-type synonyms onto recognize's canonical type names — so a VAT/TVA/BCE
// query reaches the checksum family and a "national number" label is recognized as family A and
// skipped — and (b) family-B synonyms onto a stable key (D-U-N-S/Dun & Bradstreet → duns). It is
// a SEED: growable, and any label not listed still normalizes to a sensible key via cue-stripping.
var aliasTable = map[string]string{
	// Family A — checksum types (dealiased so lookups hit the validated node; skipped on extract).
	"national number":     recognize.TypeNationalNumber,
	"numero national":     recognize.TypeNationalNumber,
	"niss":                recognize.TypeNationalNumber,
	"rijksregisternummer": recognize.TypeNationalNumber,
	"enterprise number":   recognize.TypeEnterprise,
	"numero d entreprise": recognize.TypeEnterprise,
	"numero dentreprise":  recognize.TypeEnterprise,
	"numero entreprise":   recognize.TypeEnterprise,
	"numero de tva":       recognize.TypeEnterprise,
	"vat":                 recognize.TypeEnterprise,
	"tva":                 recognize.TypeEnterprise,
	"btw":                 recognize.TypeEnterprise,
	"bce":                 recognize.TypeEnterprise,
	"kbo":                 recognize.TypeEnterprise,
	"iban":                recognize.TypeIBAN,
	// Family B — label-only seeds.
	"duns":           "duns",
	"d u n s":        "duns",
	"dun bradstreet": "duns",
	"siret":          "siret",
	"siren":          "siren",
	"ein":            "ein",
	"tin":            "tin",
	"lei":            "lei",
	"gln":            "gln",
	"rna":            "rna",
	"urssaf":         "urssaf",
}

// cueWords are the label-cue tokens ("… number") that name an identifier without being the
// identifier's own label; stripped from the phrase so "order number" → order, "D-U-N-S number"
// → duns. In BOTH positions (leading/trailing) as long as a real label token remains.
var cueWords = map[string]bool{
	"number": true, "numero": true, "num": true, "no": true, "nr": true, "n": true,
	"reference": true, "references": true, "ref": true, "id": true, "code": true,
}

// stopWords are function words (articles, possessives, determiners, conjunctions, prepositions,
// auxiliaries) that may sit in front of a cue ("the reference", "and for number") but carry no
// label identity; dropped, and used as a boundary when scanning the words in front of a cue, so a
// prose run never becomes a bogus id_the / id_and_for. EN + FR.
var stopWords = map[string]bool{
	// EN
	"the": true, "a": true, "an": true, "this": true, "that": true, "these": true, "those": true,
	"your": true, "my": true, "our": true, "his": true, "her": true, "their": true, "its": true,
	"is": true, "was": true, "are": true, "were": true, "be": true, "been": true, "has": true,
	"have": true, "had": true, "and": true, "or": true, "for": true, "as": true, "with": true,
	"from": true, "to": true, "of": true, "on": true, "in": true, "at": true, "by": true,
	"but": true, "not": true, "any": true, "above": true, "below": true, "same": true, "new": true,
	// FR
	"le": true, "la": true, "les": true, "l": true, "un": true, "une": true, "de": true,
	"des": true, "du": true, "ce": true, "cette": true, "ces": true, "mon": true, "ma": true,
	"mes": true, "votre": true, "notre": true, "leur": true, "son": true, "sa": true, "ses": true,
	"et": true, "ou": true, "avec": true, "au": true, "aux": true, "sur": true, "par": true,
	"dans": true, "pour": true, "sous": true, "vers": true, "sans": true, "car": true, "donc": true,
	"est": true, "sont": true, "ont": true,
}

// gapWords are the only word-tokens allowed to sit BETWEEN a label and its value (cue words plus
// a couple of connectors) — anything else is a content word, which means the value is not this
// label's value. Keeps "D-U-N-S Number is enclosed: 37…" adjacent while rejecting "USA:
// population 12345678…".
var gapWords = map[string]bool{
	"is": true, "enclosed": true, "of": true,
}

func init() {
	for w := range cueWords {
		gapWords[w] = true
	}
}

// normPhrase folds s, splits it into words on whitespace and underscore (the word separators),
// then within each word drops the remaining non-alphanumerics — hyphens, dots, apostrophes —
// gluing an acronym written "d-u-n-s"/"d.u.n.s" into "duns". Words rejoin with a single space. So
// "D-U-N-S Number" → "duns number" and "national_number" → "national number" (underscore splits).
func normPhrase(s string) string {
	var toks []string
	fields := strings.FieldsFunc(foldText(s), func(r rune) bool {
		return unicode.IsSpace(r) || r == '_'
	})
	for _, w := range fields {
		var b strings.Builder
		for _, r := range w {
			if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') {
				b.WriteRune(r)
			}
		}
		if b.Len() > 0 {
			toks = append(toks, b.String())
		}
	}
	return strings.Join(toks, " ")
}

// NormalizeLabel maps a raw label (a written phrase, or a lookup's `type` argument) to its
// canonical id_type key. It: (1) space-normalizes and glues acronyms; (2) consults the alias
// table on the full phrase (so "national number"/"vat" dealias before any word is dropped);
// (3) strips leading/trailing cue + stop words; (4) re-consults the alias table; (5) falls back
// to the surviving tokens joined by '_'. Idempotent on canonical keys ("national_number" →
// "national_number", "duns" → "duns"). Returns "" when nothing usable remains.
func NormalizeLabel(raw string) string {
	p := normPhrase(raw)
	if p == "" {
		return ""
	}
	if c, ok := aliasTable[p]; ok {
		return c
	}
	toks := strings.Fields(p)
	// Strip leading cue/stop words.
	for len(toks) > 1 && (cueWords[toks[0]] || stopWords[toks[0]]) {
		toks = toks[1:]
	}
	// Strip trailing cue/stop words.
	for len(toks) > 1 && (cueWords[toks[len(toks)-1]] || stopWords[toks[len(toks)-1]]) {
		toks = toks[:len(toks)-1]
	}
	// A lone surviving cue/stop word is not a label (e.g. bare "number", "the").
	if len(toks) == 1 && (cueWords[toks[0]] || stopWords[toks[0]]) {
		return ""
	}
	stripped := strings.Join(toks, " ")
	if c, ok := aliasTable[stripped]; ok {
		return c
	}
	return strings.Join(toks, "_")
}

// Value/label detection regexes, all run on the ORIGINAL text so byte offsets stay valid for the
// proximity attribution downstream.
var (
	numRunRe = regexp.MustCompile(`[0-9][0-9 .\-/:]*[0-9]|[0-9]`)
	ibanRe   = regexp.MustCompile(`(?i)\b[a-z]{2}[0-9]{2}[0-9a-z]{10,30}\b`)
	alnumRe  = regexp.MustCompile(`\b[0-9A-Za-z]{8,20}\b`)

	// aliasCueRe matches known label keywords / cue words (accent- and case-insensitive), each
	// word-bounded so a cue never fires inside a longer word ("code" in "barcode").
	aliasCueRe = regexp.MustCompile(`(?i)n°|#|\b(?:num[eé]ro(?:\s+national|\s+de\s+tva|\s+d['’ ]?\s*entreprise)?|national\s+number|r[eé]f[eé]rence|references?|duns|d-u-n-s|dun\s*(?:&|and|et)?\s*bradstreet|vat|tva|btw|bce|kbo|iban|siret|siren|ein|niss|rijksregisternummer|no|code|number|nr|id)\b`)
	// acronymRe matches an UPPERCASE acronym (plain or hyphenated: EIN, SIRET, D-U-N-S), the
	// generic mechanism that makes a brand-new labelled identifier work with no code change.
	acronymRe = regexp.MustCompile(`\b[A-Z](?:-?[A-Z0-9]){1,6}\b`)
)

// value is one identifier-grade token found in the text, with its canonical norm and offsets.
type value struct {
	norm       string
	raw        string
	start, end int
}

// Value is an identifier-grade token located in text: its canonical (identifier.Normalize'd)
// form, the raw substring as written, and its byte offsets in the source.
type Value struct {
	Norm  string
	Raw   string
	Start int
	End   int
}

// IdentifierGradeValues returns every identifier-grade value in text, using the SAME value-grade
// notion the extractor binds to labels (see findValues/valueGrade): a ≥9-digit run, an IBAN shape,
// or a mixed alphanumeric code ≥8 chars with ≥4 digits — and deliberately NOT short monetary
// amounts, 8-digit dates, counts, percentages or years. Exposed so a caller (the chat output
// guard) reuses the exact deterministic detector instead of re-deriving a value-shape test. This
// is value-SHAPE detection only — no label routing, no type list.
func IdentifierGradeValues(text string) []Value {
	vs := findValues(text)
	out := make([]Value, len(vs))
	for i, v := range vs {
		out[i] = Value{Norm: v.norm, Raw: v.raw, Start: v.start, End: v.end}
	}
	return out
}

// findValues returns the identifier-grade values in text (≥9 digits, an IBAN shape, or a mixed
// alphanumeric code ≥8 chars with ≥4 digits), non-overlapping, longest-at-position preferred. It
// excludes short amounts, 8-digit dates (YYYYMMDD) and plain words.
func findValues(text string) []value {
	var cands []value
	add := func(s, e int) {
		raw := text[s:e]
		n := identifier.Normalize(raw)
		if valueGrade(n) {
			cands = append(cands, value{n, raw, s, e})
		}
	}
	for _, m := range numRunRe.FindAllStringIndex(text, -1) {
		add(m[0], m[1])
	}
	for _, m := range ibanRe.FindAllStringIndex(text, -1) {
		add(m[0], m[1])
	}
	for _, m := range alnumRe.FindAllStringIndex(text, -1) {
		add(m[0], m[1])
	}
	// Non-overlapping, preferring the longest span at each position.
	sortValues(cands)
	var out []value
	for _, v := range cands {
		if len(out) > 0 && v.start < out[len(out)-1].end {
			continue
		}
		out = append(out, v)
	}
	return out
}

// valueGrade reports whether an identifier.Normalize'd token is identifier-grade.
func valueGrade(n string) bool {
	d, letter := 0, false
	for _, r := range n {
		if r >= '0' && r <= '9' {
			d++
		} else {
			letter = true
		}
	}
	switch {
	case !letter && d >= identifier.MinIdentifierDigits: // pure numeric ≥9 (dodges 8-digit dates)
		return true
	case letter && len(n) >= 15 && len(n) <= 34 && looksIBAN(n): // IBAN-shaped
		return true
	case letter && len(n) >= 8 && len(n) <= 20 && d >= 4: // mixed alphanumeric code
		return true
	}
	return false
}

func looksIBAN(n string) bool {
	if len(n) < 4 {
		return false
	}
	return isLower(n[0]) && isLower(n[1]) && n[2] >= '0' && n[2] <= '9' && n[3] >= '0' && n[3] <= '9'
}

func isLower(b byte) bool { return b >= 'a' && b <= 'z' }

// anchor is a label occurrence: its canonical id_type and byte offsets. `end` is where its value
// is expected to begin; `start` bounds the previous label's span. `strong` marks a label backed by
// a known keyword/alias or an explicit cue word ("… number") — as opposed to a bare uppercase
// acronym, which is trusted only under strict adjacency (a footer's "CA"/"MS"/"TM" must not count
// as a document label).
type anchor struct {
	canon      string
	start, end int
	strong     bool
}

// findAnchors returns the qualifying label occurrences (alias/cue keywords + uppercase acronyms)
// in text, each resolved to its canonical id_type. A cue anchor ("… number") pulls in up to two
// preceding label words ("order number" → order); an alias/acronym anchor stands alone.
func findAnchors(text string) []anchor {
	var out []anchor
	for _, m := range aliasCueRe.FindAllStringIndex(text, -1) {
		phrase := text[m[0]:m[1]]
		low := normPhrase(phrase)
		var canon string
		if isBareCue(low) {
			// A cue word on its own — the label root is the preceding word(s).
			canon = NormalizeLabel(strings.Join(precedingLabelWords(text, m[0]), " ") + " " + phrase)
		} else {
			canon = NormalizeLabel(phrase)
		}
		if canon != "" {
			out = append(out, anchor{canon, m[0], m[1], true}) // keyword/alias/cue → strong
		}
	}
	for _, m := range acronymRe.FindAllStringIndex(text, -1) {
		// A bare uppercase acronym is a label ONLY if it is a KNOWN identifier (alias-table entry:
		// DUNS, SIRET, EIN, GLN…). An UNKNOWN caps token is dropped entirely — in real documents it
		// is overwhelmingly a proper noun the case can't distinguish from a label (a surname
		// "DUBOIS", a city "PARIS", a currency "EUR"), which would mint garbage id_dubois/id_paris.
		// New acronym identifiers are onboarded by adding a seed alias, not by trusting ALL-CAPS.
		if _, known := aliasTable[normPhrase(text[m[0]:m[1]])]; !known {
			continue
		}
		if canon := NormalizeLabel(text[m[0]:m[1]]); canon != "" {
			out = append(out, anchor{canon, m[0], m[1], true})
		}
	}
	sortAnchors(out)
	return out
}

// isBareCue reports whether a space-normalized phrase is only cue/stop words (no label root),
// so its label must come from the preceding words.
func isBareCue(low string) bool {
	fields := strings.Fields(low)
	if len(fields) == 0 {
		return true
	}
	for _, f := range fields {
		if !cueWords[f] && !stopWords[f] {
			return false
		}
	}
	return true
}

// precedingLabelWords returns up to the two alphabetic words immediately before anchorStart on
// the same line (the label root in front of a cue word).
func precedingLabelWords(text string, anchorStart int) []string {
	lo := anchorStart - 40
	if lo < 0 {
		lo = 0
	}
	prefix := text[lo:anchorStart]
	if nl := strings.LastIndexByte(prefix, '\n'); nl >= 0 {
		prefix = prefix[nl+1:]
	}
	fields := strings.Fields(prefix)
	var out []string
	for i := len(fields) - 1; i >= 0 && len(out) < 2; i-- {
		w := fields[i]
		// Stop at a determiner/stopword ("this number"/"and for number" are generic prose, not a
		// label) or a single-letter token ("model x number") — only genuine multi-letter content
		// words in front of the cue are label roots. On a boundary, the collected words (if any)
		// form the label; if none, the cue forms no label and the anchor is rejected.
		if !isLabelWord(w) || stopWords[strings.ToLower(w)] || len([]rune(w)) < 2 {
			break
		}
		out = append([]string{w}, out...)
	}
	return out
}

// isLabelWord reports whether w is a label word: at least one letter, and only letters or the
// intra-acronym connectors '-' '.' (so "D-U-N-S" and "Order" qualify, "123" and "no." do not).
func isLabelWord(w string) bool {
	letter := false
	for _, r := range w {
		switch {
		case unicode.IsLetter(r):
			letter = true
		case r == '-' || r == '.':
		default:
			return false
		}
	}
	return letter
}

// Extract returns the label-bound identifier facts in text, deterministically, as
// []recognize.Typed with Type = the normalized (family-B) label. Binding rules — precision-first,
// fail-closed:
//
//   - ADJACENT: a label binds a value only when its forward span (up to the next label, a blank
//     line, or maxSpan) contains EXACTLY ONE value, and only separator/cue words lie between them.
//     0 or ≥2 values in the span → the label binds nothing (ambiguous).
//   - A value bound by TWO different labels is ambiguous → dropped.
//   - DOCUMENT-LEVEL (fallback, when nothing bound adjacently): only when the whole document has
//     EXACTLY ONE value and EXACTLY ONE distinct label (covers a label in the subject + value in
//     the body).
//
// Checksum-type labels (national_number/enterprise_number/iban and their synonyms) are never
// emitted: recognize owns and validates them.
func Extract(text string) []recognize.Typed {
	values := findValues(text)
	if len(values) == 0 {
		return nil
	}
	anchors := findAnchors(text)

	// Adjacent binds: canon per value (via a map so a value claimed by two distinct canons drops).
	type bind struct {
		canon    string
		conflict bool
	}
	bound := map[int]*bind{} // value index → its bind
	checksumClaimed := false // a value adjacently owned by a checksum-type label (blocks doc-level)
	for _, a := range anchors {
		// The label binds the FIRST value after it — "LABEL <sep> VALUE".
		vi := -1
		for i, v := range values {
			if v.start >= a.end {
				vi = i
				break
			}
		}
		if vi < 0 {
			continue // no value after the label
		}
		if !gapOK(text[a.end:values[vi].start]) {
			continue // not a "LABEL <sep> VALUE" binding (content word between, no separator, or too far)
		}
		// Bare-list guard: "LABEL: 111 222" — a second value immediately follows the first with only
		// value separators between them (no words), so which one is the value is ambiguous → decline.
		if vi+1 < len(values) && onlyValueSep(text[values[vi].end:values[vi+1].start]) {
			continue
		}
		if recognize.IsChecksumType(a.canon) {
			checksumClaimed = true
			continue // checksum family owns/validates these
		}
		if b := bound[vi]; b == nil {
			bound[vi] = &bind{canon: a.canon}
		} else if b.canon != a.canon {
			b.conflict = true // same value, two different labels → ambiguous
		}
	}

	var out []recognize.Typed
	for vi, b := range bound {
		if b.conflict {
			continue
		}
		v := values[vi]
		out = append(out, recognize.Typed{Type: b.canon, Value: v.norm, Raw: v.raw, Start: v.start, End: v.end})
	}
	if len(out) > 0 {
		return out
	}

	// Document-level fallback (covers the Apple mail: label in the subject / same sentence, value
	// separated by an entity — "The D-U-N-S Number for 0X0800 is 373258378"): bind only when the
	// doc has exactly ONE identifier-grade value and exactly ONE distinct STRONG label. Weak
	// (bare-acronym) labels are ignored — a footer's "CA"/"MS"/"TM" must not defeat the bind or
	// masquerade as the label.
	if len(values) != 1 || checksumClaimed {
		return nil
	}
	distinct := map[string]bool{}
	for _, a := range anchors {
		if a.strong {
			distinct[a.canon] = true
		}
	}
	if len(distinct) != 1 {
		return nil
	}
	var canon string
	for c := range distinct {
		canon = c
	}
	if recognize.IsChecksumType(canon) {
		return nil
	}
	v := values[0]
	return []recognize.Typed{{Type: canon, Value: v.norm, Raw: v.raw, Start: v.start, End: v.end}}
}

// onlyValueSep reports whether s (the text between two adjacent values) contains no letter — only
// whitespace / value separators — meaning the two values form a bare list under one label
// ("111 222", "111, 222"), which is ambiguous. A words-bearing gap ("111 and amount 222") is not.
func onlyValueSep(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// gapOK reports whether the gap between a label and its value marks a genuine "LABEL <sep> VALUE"
// binding: within maxGap bytes, containing only cue/connector words, AND carrying an explicit
// separator (':' '=' '#' '°' or a newline). Requiring a separator is the precision guard that
// stops a value merely NEAR a word in prose ("model 123456789") from binding — a real labelled
// fact writes "Model: 123456789". (The document-level fallback, which does not call gapOK, still
// covers the separator-less subject/body case.)
func gapOK(gap string) bool {
	if len(gap) > maxGap || hasBlankLine(gap) {
		return false // too far, or a paragraph break separates them → not the same segment
	}
	for _, w := range strings.FieldsFunc(foldText(gap), func(r rune) bool {
		return !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'z'))
	}) {
		if _, ok := gapWords[w]; !ok {
			return false
		}
	}
	for _, r := range gap {
		if r == ':' || r == '=' || r == '#' || r == '°' || r == '\n' {
			return true
		}
	}
	return false
}

// hasBlankLine reports whether s contains a blank line (\n, optional spaces/tabs/\r, \n) — a
// paragraph break. A label and value split by one are in different segments, not adjacent.
func hasBlankLine(s string) bool {
	nl := -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n':
			if nl >= 0 {
				return true
			}
			nl = i
		case ' ', '\t', '\r':
			// stay in a run only if we've already seen a newline
		default:
			nl = -1
		}
	}
	return false
}

// sortValues / sortAnchors: small insertion-free sorts kept here to avoid pulling sort into the
// hot path unnecessarily; slices are tiny (labels/values per doc).
func sortValues(vs []value) {
	for i := 1; i < len(vs); i++ {
		for j := i; j > 0 && less(vs[j], vs[j-1]); j-- {
			vs[j], vs[j-1] = vs[j-1], vs[j]
		}
	}
}

// less orders by start ascending, then longer span first (so the longest at a position wins).
func less(a, b value) bool {
	if a.start != b.start {
		return a.start < b.start
	}
	return (a.end - a.start) > (b.end - b.start)
}

func sortAnchors(as []anchor) {
	for i := 1; i < len(as); i++ {
		for j := i; j > 0 && as[j].end < as[j-1].end; j-- {
			as[j], as[j-1] = as[j-1], as[j]
		}
	}
}
