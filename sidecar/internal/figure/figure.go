// Package figure is the GENERIC, DETERMINISTIC extractor + resolver for LABELLED MONETARY
// figures (FIGURES_TRUTH_PLAN family A / F1). Where labelfact binds an identifier-grade token to
// the label written next to it, figure binds a MONETARY AMOUNT to its label AND to the CONTEXT
// that makes the amount true: a unit (€/EUR), a period (a quarter/month/year → a normalized key),
// and a direction (payable / refund / advance / due — WHICH figure a shared label denotes). A bare
// amount is wrong; its context is part of its truth (G2).
//
// The mechanism is generic — label + context PATTERNS, never a VAT `if`:
//   - the LABEL is normalized against a small SEED table (the domain vocabulary pack, F2 grows it);
//     VAT/TVA/BTW is the only F1 seed, exactly as labelfact seeds DUNS/SIRET — DATA, not code.
//   - UNIT, PERIOD and DIRECTION are generic financial pattern tables shared by every label.
//
// It is precision-first and FAILS CLOSED: an amount is emitted only when a SEEDED financial label
// governs it (adjacent, with a separator) and a monetary unit is present. On any ambiguity it
// extracts nothing — better to miss than to mint a wrong figure. No LLM, no embeddings.
//
// Each extracted figure becomes an ENGRAM NODE (value+unit) with typed CONTEXT EDGES
// {entity, period, direction, source} in the store.figure_nodes table — reusing the entity graph,
// not a parallel truth store. Resolution (resolve.go) is a deterministic TRAVERSAL over those edges.
package figure

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Direction canonical tokens — WHICH figure a shared label denotes. Generic financial directions,
// not VAT-specific: any labelled amount carries one. "" means undetermined (resolution may decline).
const (
	DirPayable = "payable" // to pay / net à payer / solde dû
	DirRefund  = "refund"  // remboursé / à récupérer / crédit / en votre faveur
	DirAdvance = "advance" // acompte / provision
	DirDue     = "due"     // montant dû (generic; kept distinct from an explicit "to pay" call-out)
)

// Figure is one extracted labelled figure: the NODE (value+unit) plus its CONTEXT EDGES
// (label, period, direction, and — for a quantity/dosage — medication + frequency) and byte offsets
// in the source (so the ingest proximity pass can attribute it to the nearest entity). The SAME
// mould serves a monetary amount and a medical dosage: only the UNIT and the qualifying context
// differ, and the unit is resolved from a DATA table (unitTable), never a per-type branch.
type Figure struct {
	Label     string // normalized figure label ("vat" for money; "dose" for a dosage/quantity)
	Value     string // canonical numeric, dot-decimal, no grouping (e.g. "7421.85", "500")
	Raw       string // the amount as written (e.g. "7 421,85", "500")
	Unit      string // normalized unit from unitTable ("EUR", "mg", "mcg", "ml", "IU", "%")
	Period    string // normalized period key ("2026-Q1", "2026-03", "2026") or ""
	Direction string // one of Dir* or "" (money)
	// Medication is the qualifying CONTEXT of a dosage figure (the drug the dose belongs to) —
	// the analogue of a rate's client: it is what a shared "dose" label denotes. Folded, "" for money.
	Medication string
	// Frequency is the dosage cadence normalized to "N×/day" (or "" when none / not a dosage).
	Frequency string
	Start     int
	End       int
}

// foldText lowercases s and strips accents (NFD, dropping combining marks) so "remboursé" and
// "rembourse" compare equal. Mirrors labelfact.foldText / identity folding.
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

// labelAliases maps a folded label phrase to its canonical figure label. SEED = the domain
// vocabulary pack (F2 grows it from accounting/fiscal standards). F1 seeds only the VAT family.
// This is the ONLY place a domain term appears, as DATA — the extraction MECHANISM below is generic.
var labelAliases = map[string]string{
	"tva":   "vat",
	"vat":   "vat",
	"btw":   "vat",
	"t v a": "vat",
	// dosage/quantity: the generic figure kind whose qualifier is the MEDICATION (C7). A dosage doc
	// need not contain the word "dose" (the unit fires extraction); these seeds let a QUERY name it.
	"dose":       "dose",
	"doses":      "dose",
	"dosage":     "dose",
	"posology":   "dose",
	"posologie":  "dose",
	"posologies": "dose",
}

// NormalizeFigureLabel maps a raw label (a written phrase or a query's `label` argument) to its
// canonical figure label. Folds + space-normalizes, consults the seed table, and returns "" when
// nothing seeded matches — so an unknown label FAILS CLOSED (extracts nothing / declines) rather
// than minting a garbage figure type. Idempotent on canonical labels.
func NormalizeFigureLabel(raw string) string {
	p := strings.Join(strings.FieldsFunc(foldText(raw), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	}), " ")
	if p == "" {
		return ""
	}
	if c, ok := labelAliases[p]; ok {
		return c
	}
	// Also try each single token (so "declaration tva" or "ma tva" resolves via the "tva" token).
	for _, tok := range strings.Fields(p) {
		if c, ok := labelAliases[tok]; ok {
			return c
		}
	}
	return ""
}

// directionCues maps a folded cue substring to a canonical direction. Order matters: the FIRST
// cue found in the window wins, and refund/advance cues precede the broad payable/due ones so
// "à récupérer" is not shadowed by a nearby "payer". Generic financial directions (EN + FR).
var directionCues = []struct {
	cue string
	dir string
}{
	{"rembours", DirRefund},    // remboursé, remboursement
	{"a recuperer", DirRefund}, // à récupérer
	{"recuperer", DirRefund},   // récupérer
	{"en votre faveur", DirRefund},
	{"credit", DirRefund}, // crédit (TVA en crédit)
	{"refund", DirRefund},
	{"restituer", DirRefund},
	{"acompte", DirAdvance},
	{"provision", DirAdvance},
	{"advance", DirAdvance},
	{"a payer", DirPayable}, // à payer
	{"net a payer", DirPayable},
	{"to pay", DirPayable},
	{"payable", DirPayable},
	{"solde du", DirDue}, // solde dû
	{"montant du", DirDue},
	{"a devoir", DirDue},
	{"due", DirDue},
}

// NormalizeDirection maps a raw direction phrase (from a query's `direction` argument, or a text
// window) to a canonical Dir*. Returns "" when nothing matches (the caller decides whether an
// unknown/empty direction is ambiguous → decline).
func NormalizeDirection(raw string) string {
	f := foldText(raw)
	for _, dc := range directionCues {
		if strings.Contains(f, dc.cue) {
			return dc.dir
		}
	}
	return ""
}

// Unit KINDS group the unit table by dimension so the extractor fires the right pass without a
// per-unit branch: currency drives the monetary (label-governed) pass; the QUANTITY kinds
// (mass/volume/activity) drive the dosage pass. A kind is DATA on each row — a new unit is a new
// row, never new code.
const (
	kindCurrency = "currency"
	kindMass     = "mass"     // mg, g, mcg
	kindVolume   = "volume"   // ml
	kindActivity = "activity" // IU / UI
	kindRatio    = "ratio"    // %
)

type unitDef struct {
	canon string // canonical unit symbol ("EUR", "mg", "mcg", "ml", "IU", "%")
	kind  string // one of kind*
}

// unitTable is THE unit vocabulary — DATA, not code. It replaces the hardcoded euro: every unit the
// engine understands is a row here (folded surface form → canonical + kind). Currency is one group;
// the medical/quantity units (mg, g, mcg/µg, ml, IU/UI, %) are more rows. Extending the engine to a
// new unit is adding a row — no new `if`, no new type list. Both the monetary and the dosage passes
// consult it, so a figure is uniformly "value + unit (+ context)".
var unitTable = map[string]unitDef{
	// currency
	"€": {"EUR", kindCurrency}, "eur": {"EUR", kindCurrency}, "euro": {"EUR", kindCurrency}, "euros": {"EUR", kindCurrency},
	// mass (dosage)
	"mg": {"mg", kindMass}, "milligram": {"mg", kindMass}, "milligrams": {"mg", kindMass}, "milligramme": {"mg", kindMass}, "milligrammes": {"mg", kindMass},
	"g": {"g", kindMass}, "gram": {"g", kindMass}, "grams": {"g", kindMass}, "gramme": {"g", kindMass}, "grammes": {"g", kindMass},
	"mcg": {"mcg", kindMass}, "µg": {"mcg", kindMass}, "ug": {"mcg", kindMass}, "microgram": {"mcg", kindMass}, "micrograms": {"mcg", kindMass}, "microgramme": {"mcg", kindMass}, "microgrammes": {"mcg", kindMass},
	// volume (dosage)
	"ml": {"ml", kindVolume}, "milliliter": {"ml", kindVolume}, "milliliters": {"ml", kindVolume}, "millilitre": {"ml", kindVolume}, "millilitres": {"ml", kindVolume},
	// activity (dosage)
	"iu": {"IU", kindActivity}, "ui": {"IU", kindActivity},
	// ratio
	"%": {"%", kindRatio}, "percent": {"%", kindRatio}, "pourcent": {"%", kindRatio},
}

// canonCurrency returns the canonical currency for a matched currency token via the unit table
// (so "€"/"EUR"/"euros" all resolve to "EUR" from DATA, not a hardcoded literal). "" if not currency.
func canonCurrency(token string) string {
	if u, ok := unitTable[foldText(strings.TrimSpace(token))]; ok && u.kind == kindCurrency {
		return u.canon
	}
	return ""
}

var (
	// moneyRe matches a monetary amount tied to a euro unit, on EITHER side: "7 421,85 €",
	// "€7,421.85", "1234.50 EUR", "EUR 1 000". The amount grammar admits any-space/dot/comma
	// grouping (\p{Zs} covers the non-breaking spaces Belgian statements use) and an optional 1–2
	// digit decimal; parseAmount disambiguates the decimal separator. Generic (currency + amount
	// adjacency) — no label, no VAT.
	moneyRe = regexp.MustCompile(`(?i)(?:(€|eur|euros?)\s*([0-9][0-9.,\p{Zs}]*[0-9]|[0-9])|([0-9][0-9.,\p{Zs}]*[0-9]|[0-9])\s*(€|eur|euros?))`)

	// periodQuarterRe: "Q1 2026", "T1/2026", "1er trimestre 2026", "trimestre 1 2026".
	periodQTRe    = regexp.MustCompile(`(?i)\b[qt]\s*([1-4])\s*[ /\-.]?\s*(20\d{2})\b`)
	periodYQTRe   = regexp.MustCompile(`(?i)\b(20\d{2})\s*[ /\-.]?\s*[qt]\s*([1-4])\b`)
	periodTrimRe  = regexp.MustCompile(`(?i)\b([1-4])\s*(?:er|e|eme|ème|ère|ere)?\s*trimestre\s*(?:de\s*|du\s*)?(20\d{2})\b`)
	periodTrim2Re = regexp.MustCompile(`(?i)\btrimestre\s*([1-4])\s*(?:de\s*|du\s*)?(20\d{2})\b`)
	// periodYearRe: "année 2026", "exercice 2026", "AF 2026".
	periodYearRe = regexp.MustCompile(`(?i)\b(?:annee|année|exercice|af)\s*[:\-]?\s*(20\d{2})\b`)
	// periodMonthRe: month name + year (FR + EN), → "YYYY-MM".
	periodMonthRe = regexp.MustCompile(`(?i)\b(janvier|fevrier|février|mars|avril|mai|juin|juillet|aout|août|septembre|octobre|novembre|decembre|décembre|january|february|march|april|may|june|july|august|september|october|november|december)\s+(20\d{2})\b`)
)

var monthIndex = map[string]string{
	"janvier": "01", "january": "01",
	"fevrier": "02", "février": "02", "february": "02",
	"mars": "03", "march": "03",
	"avril": "04", "april": "04",
	"mai": "05", "may": "05",
	"juin": "06", "june": "06",
	"juillet": "07", "july": "07",
	"aout": "08", "août": "08", "august": "08",
	"septembre": "09", "september": "09",
	"octobre": "10", "october": "10",
	"novembre": "11", "november": "11",
	"decembre": "12", "décembre": "12", "december": "12",
}

// findPeriod returns the FIRST normalized period key found in s, and whether one was found.
// Quarter → "YYYY-Qn"; month → "YYYY-MM"; year → "YYYY". Precision order: quarter/month before
// a bare year (a quarter line usually also names the year).
func findPeriod(s string) (string, bool) {
	if m := periodQTRe.FindStringSubmatch(s); m != nil {
		return m[2] + "-Q" + m[1], true
	}
	if m := periodYQTRe.FindStringSubmatch(s); m != nil {
		return m[1] + "-Q" + m[2], true
	}
	if m := periodTrimRe.FindStringSubmatch(s); m != nil {
		return m[2] + "-Q" + m[1], true
	}
	if m := periodTrim2Re.FindStringSubmatch(s); m != nil {
		return m[2] + "-Q" + m[1], true
	}
	if m := periodMonthRe.FindStringSubmatch(s); m != nil {
		if mi, ok := monthIndex[strings.ToLower(m[1])]; ok {
			return m[2] + "-" + mi, true
		}
	}
	if m := periodYearRe.FindStringSubmatch(s); m != nil {
		return m[1], true
	}
	return "", false
}

// PeriodRank returns a monotonically-increasing integer for a normalized period key so
// resolution can order figures by their period edge and pick the LATEST. Quarters and months map
// into the same year*100 space (Q1→1..Q4→4 vs month 01..12 never collide meaningfully within one
// series); a bare year sorts before its own sub-periods. Unparseable keys rank 0.
func PeriodRank(key string) int {
	if key == "" {
		return 0
	}
	if i := strings.Index(key, "-Q"); i >= 0 {
		y, _ := strconv.Atoi(key[:i])
		q, _ := strconv.Atoi(key[i+2:])
		return y*100 + q
	}
	if i := strings.Index(key, "-"); i >= 0 {
		y, _ := strconv.Atoi(key[:i])
		mo, _ := strconv.Atoi(key[i+1:])
		return y*100 + mo
	}
	y, _ := strconv.Atoi(key)
	return y * 100
}

// parseAmount canonicalizes a raw amount string ("7 421,85", "7,421.85", "1000") to a dot-decimal
// with no grouping ("7421.85"). It treats the LAST separator as the decimal point IFF it is
// followed by exactly 1–2 digits; every other separator is grouping and is dropped. Returns
// ("", false) when no digits survive.
func parseAmount(raw string) (string, bool) {
	// Keep only digits and separators.
	var b strings.Builder
	for _, r := range raw {
		if (r >= '0' && r <= '9') || r == '.' || r == ',' || unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		return "", false
	}
	// Locate the decimal separator: the last '.' or ',' followed by 1–2 trailing digits.
	decPos := -1
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' || s[i] == ',' {
			frac := s[i+1:]
			if len(frac) >= 1 && len(frac) <= 2 && allDigits(frac) {
				decPos = i
			}
			break
		}
	}
	intPart, fracPart := s, ""
	if decPos >= 0 {
		intPart, fracPart = s[:decPos], s[decPos+1:]
	}
	intPart = stripNonDigits(intPart)
	if intPart == "" && fracPart == "" {
		return "", false
	}
	if intPart == "" {
		intPart = "0"
	}
	if fracPart == "" {
		return intPart, true
	}
	return intPart + "." + fracPart, true
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

func stripNonDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// amount is one monetary amount located in text, with its canonical value, unit and offsets.
type amount struct {
	value      string
	raw        string
	unit       string // canonical currency from the unit table ("EUR")
	start, end int
}

// AmountValue is a FIGURE-grade value located in text: its canonical numeric form (dot-decimal,
// no grouping — "7421.85"), the raw substring as written ("7 421,85 €"), and byte offsets. It is
// the figure analogue of labelfact.Value, exposed so the provenance firewall detects monetary
// answer-values with the SAME detector the extractor binds.
type AmountValue struct {
	Value string
	Raw   string
	Start int
	End   int
}

// AmountValues returns every euro-denominated monetary amount in text (non-overlapping). The
// currency-unit adjacency the detector requires (€/EUR next to the digits) is the built-in
// CALIBRATION that keeps bare prose numbers OUT: a count ("3 invoices"), a percentage ("20%"), a
// year ("2018") and a date ("2026-07-03") carry no currency, so none is returned. Exposed so the
// output guard classifies FIGURE-grade values (monetary amounts) by the same value-SHAPE test the
// figure extractor uses — no label routing, no type list.
func AmountValues(text string) []AmountValue {
	as := findAmounts(text)
	out := make([]AmountValue, len(as))
	for i, a := range as {
		out[i] = AmountValue{Value: a.value, Raw: a.raw, Start: a.start, End: a.end}
	}
	return out
}

// NormalizeAmount canonicalizes a raw amount string ("7 421,85 €", "850", "1,234.50") to the same
// dot-decimal, no-grouping form the extractor stores ("7421.85", "850", "1234.50"), and whether it
// parsed. UNLIKE AmountValues it does NOT require a currency unit — it is used to fold a TRUSTED
// determined value (a figure claim) into the guard's membership set however it was written, so a
// unit-bearing amount in the answer still matches. Returns ("", false) when no digits survive.
func NormalizeAmount(raw string) (string, bool) { return parseAmount(raw) }

// findAmounts returns the euro-denominated monetary amounts in text (non-overlapping).
func findAmounts(text string) []amount {
	var out []amount
	for _, m := range moneyRe.FindAllStringSubmatchIndex(text, -1) {
		// group 1/2 = (unit,amount) when unit leads; group 3/4 = (amount,unit) when unit trails.
		var as, ae int
		var unitTok string
		if m[4] >= 0 { // submatch 2 (amount), submatch 1 (leading unit)
			as, ae = m[4], m[5]
			if m[2] >= 0 {
				unitTok = text[m[2]:m[3]]
			}
		} else if m[6] >= 0 { // submatch 3 (amount), submatch 4 (trailing unit)
			as, ae = m[6], m[7]
			if m[8] >= 0 {
				unitTok = text[m[8]:m[9]]
			}
		} else {
			continue
		}
		raw := text[as:ae]
		v, ok := parseAmount(raw)
		if !ok {
			continue
		}
		unit := canonCurrency(unitTok)
		if unit == "" {
			unit = "EUR" // currency-adjacency matched but token unresolved (defensive)
		}
		out = append(out, amount{value: v, raw: strings.TrimSpace(raw), unit: unit, start: as, end: ae})
	}
	return out
}

// labelKeywordRe matches the surface forms of a seeded financial label (accent/case-insensitive),
// each word-bounded. Generic mechanism: the alias table drives which words qualify — add a seed to
// grow it, no code change. F1 = the VAT family.
var labelKeywordRe = regexp.MustCompile(`(?i)\b(tva|vat|btw|t\.v\.a\.)\b`)

const (
	// labelWindow bounds how far before an amount a governing label may sit (chars, same segment).
	labelWindow = 120
	// ctxWindow bounds the direction/period lookaround around a labelled amount (chars).
	ctxWindow = 160
)

// Extract returns the labelled monetary figures in text, deterministically. For each monetary
// amount it looks BACK within labelWindow for a seeded financial label on the same segment (no
// blank line between); if found, it emits a figure node with the amount as the value, the unit,
// and the direction + period read from a window AROUND the label→amount span (context edges). An
// amount with no seeded label in reach is skipped (fail-closed: no bare amounts). A document-level
// period fallback fills the period when the amount's own window carries none but the document names
// exactly one period.
func Extract(text string) []Figure {
	var out []Figure
	amounts := findAmounts(text)
	labels := labelKeywordRe.FindAllStringIndex(text, -1)
	if len(amounts) == 0 || len(labels) == 0 {
		// No labelled monetary figure — but a dosage/quantity figure may still be present.
		return append(out, extractDoses(text)...)
	}
	docPeriod, docHasPeriod := findPeriod(text)

	for _, a := range amounts {
		// Nearest seeded label whose end is at most labelWindow chars before the amount, on the
		// same segment (no blank line between the label and the amount).
		var canon string
		var labelStart, labelEnd int
		found := false
		for _, l := range labels {
			if l[1] > a.start { // label must precede the amount
				continue
			}
			if a.start-l[1] > labelWindow {
				continue
			}
			if hasBlankLine(text[l[1]:a.start]) {
				continue
			}
			c := NormalizeFigureLabel(text[l[0]:l[1]])
			if c == "" {
				continue
			}
			// keep the LAST (closest) qualifying label
			canon, labelStart, labelEnd, found = c, l[0], l[1], true
		}
		if !found {
			continue
		}

		// Context window: from a little before the label to a little after the amount.
		ws := labelStart - ctxWindow
		if ws < 0 {
			ws = 0
		}
		we := a.end + ctxWindow
		if we > len(text) {
			we = len(text)
		}
		window := text[ws:we]
		// Direction: prefer the tighter label→amount span, then the wider window.
		dir := NormalizeDirection(text[labelStart:a.end])
		if dir == "" {
			dir = NormalizeDirection(window)
		}
		// Period: prefer the window; fall back to the single document period.
		period, hasP := findPeriod(window)
		if !hasP && docHasPeriod {
			period = docPeriod
		}

		out = append(out, Figure{
			Label:     canon,
			Value:     a.value,
			Raw:       a.raw,
			Unit:      a.unit, // from the unit table, not a hardcoded literal
			Period:    period,
			Direction: dir,
			Start:     a.start,
			End:       a.end,
		})
		_ = labelEnd
	}
	// Dosage/quantity figures: same mould (value + unit + context), the medication playing the
	// label-qualifier role and the frequency an extra context edge.
	out = append(out, extractDoses(text)...)
	return out
}

// ---------------------------------------------------------------------------------------------
// Dosage / quantity-with-unit figures (C7). A dosage is the SAME figure NODE as a monetary amount —
// value + unit — with two extra generic context edges: the MEDICATION it belongs to (the qualifier,
// like a rate's client) and the FREQUENCY (its cadence). It fires ONLY on a quantity UNIT from the
// unit table (mass/volume/activity), never on a bare number, and only when a medication name governs
// it — so weights ("72 kg", kg is not a dosage unit), blood pressure ("120/80", no unit) and a heart
// rate ("HR 68", no unit) are NEVER read as a dose (fail-closed, precision-first).
// ---------------------------------------------------------------------------------------------

var (
	// quantityRe matches a number immediately followed by a DOSAGE unit token (unit trails the value,
	// as dosages are always written). Multi-char tokens precede "g" so "mg"/"mcg" are never shortened;
	// the trailing \b keeps "kg" (weight) and "mgX" out. Built to mirror the amount grammar (any-space
	// /dot/comma grouping, optional decimal). The kind is validated against unitTable after matching.
	quantityRe = regexp.MustCompile(`(?i)([0-9][0-9.,\p{Zs}]*[0-9]|[0-9])\s*(µg|mcg|micrograms?|microgrammes?|milligrams?|milligrammes?|mg|millilitres?|milliliters?|ml|grams?|grammes?|iu|ui|g)\b`)

	// frequencyRe forms — a cadence expressed as N times per day (EN + FR + "×"/"x" shorthand).
	freqNPerDayRe = regexp.MustCompile(`(?i)(\d+)\s*[x×]\s*/?\s*(?:per\s*)?(?:day|d\b|jour|j\b)`)
	freqNTimesRe  = regexp.MustCompile(`(?i)(\d+)\s*(?:times?|fois)\s*(?:a|per|par)?\s*(?:day|jour)`)
	// medicationRe locates a medication NAME: a Capitalized word (proper drug name), ≥3 letters.
	medicationRe = regexp.MustCompile(`\p{Lu}[\p{L}\-]{2,}`)
)

// latinFreq maps the common posology abbreviations to a per-day count.
var latinFreq = map[string]int{"qd": 1, "od": 1, "bid": 2, "bd": 2, "tid": 3, "tds": 3, "qid": 4, "qds": 4}

const (
	medWindow  = 64 // how far before a dose a governing medication name may sit (chars, same segment)
	freqWindow = 48 // how far around a dose a frequency cue is read (chars)
)

// quantity is one value+unit dosage token located in text.
type quantity struct {
	value, raw, unit string
	start, end       int
}

// findQuantities returns every dosage-unit quantity in text (mass/volume/activity kinds only — a
// currency or ratio unit is not a dosage). Non-overlapping.
func findQuantities(text string) []quantity {
	var out []quantity
	for _, m := range quantityRe.FindAllStringSubmatchIndex(text, -1) {
		as, ae := m[2], m[3]
		us, ue := m[4], m[5]
		v, ok := parseAmount(text[as:ae])
		if !ok {
			continue
		}
		u, ok := unitTable[foldText(text[us:ue])]
		if !ok || (u.kind != kindMass && u.kind != kindVolume && u.kind != kindActivity) {
			continue
		}
		out = append(out, quantity{value: v, raw: strings.TrimSpace(text[as:ae]), unit: u.canon, start: as, end: ue})
	}
	return out
}

// medicationBefore returns the nearest Capitalized medication name ending within medWindow chars
// before pos on the SAME segment (no blank line between), or "" (fail-closed → no dose emitted).
func medicationBefore(text string, pos int) string {
	ws := pos - medWindow
	if ws < 0 {
		ws = 0
	}
	seg := text[ws:pos]
	if hasBlankLine(seg) {
		if i := lastBlankLineEnd(seg); i >= 0 {
			seg = seg[i:]
		}
	}
	locs := medicationRe.FindAllString(seg, -1)
	if len(locs) == 0 {
		return ""
	}
	return locs[len(locs)-1]
}

// lastBlankLineEnd returns the index just after the last paragraph break in s, or -1.
func lastBlankLineEnd(s string) int {
	end, nlAt := -1, -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n':
			if nlAt >= 0 {
				end = i + 1
			}
			nlAt = i
		case ' ', '\t', '\r':
		default:
			nlAt = -1
		}
	}
	return end
}

// normalizeFrequency reads a dosage cadence from a window and returns it canonically as "N×/day"
// (or "" when none is present). Handles "3×/day", "3x/j", "3 times a day", and bid/tid/qid.
func normalizeFrequency(window string) string {
	if m := freqNPerDayRe.FindStringSubmatch(window); m != nil {
		return m[1] + "×/day"
	}
	if m := freqNTimesRe.FindStringSubmatch(window); m != nil {
		return m[1] + "×/day"
	}
	f := foldText(window)
	toks := strings.FieldsFunc(f, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	tokSet := map[string]bool{}
	for _, t := range toks {
		tokSet[t] = true
	}
	for _, abbr := range []string{"qd", "od", "bid", "bd", "tid", "tds", "qid", "qds"} {
		if tokSet[abbr] {
			return strconv.Itoa(latinFreq[abbr]) + "×/day"
		}
	}
	// Worded cadence ("once daily", "twice a day", "trois fois par jour" handled above by freqNTimesRe).
	if tokSet["day"] || tokSet["daily"] || tokSet["jour"] || tokSet["die"] {
		for word, n := range map[string]int{"once": 1, "twice": 2, "thrice": 3} {
			if tokSet[word] {
				return strconv.Itoa(n) + "×/day"
			}
		}
	}
	return ""
}

// extractDoses returns the dosage figures in text: each dosage-unit quantity governed by a nearby
// medication name, with the frequency read from a window around it. A quantity with no medication in
// reach is dropped (fail-closed). Deterministic.
func extractDoses(text string) []Figure {
	qs := findQuantities(text)
	if len(qs) == 0 {
		return nil
	}
	var out []Figure
	for _, q := range qs {
		med := medicationBefore(text, q.start)
		if med == "" {
			continue // no governing medication → not a resolvable dose
		}
		fs := q.end - freqWindow
		if fs < 0 {
			fs = 0
		}
		fe := q.end + freqWindow
		if fe > len(text) {
			fe = len(text)
		}
		out = append(out, Figure{
			Label:      "dose",
			Value:      q.value,
			Raw:        q.raw,
			Unit:       q.unit,
			Medication: foldText(med),
			Frequency:  normalizeFrequency(text[fs:fe]),
			Start:      q.start,
			End:        q.end,
		})
	}
	return out
}

// QuantityValue is a dosage-grade value located in text: canonical numeric, raw substring, unit and
// offsets. The figure analogue of AmountValue for the NON-currency dosage units, exposed so the
// provenance firewall detects dose answer-values with the SAME detector the extractor binds.
type QuantityValue struct {
	Value string
	Raw   string
	Unit  string
	Start int
	End   int
}

// QuantityValues returns every DOSAGE-grade quantity in text (mass/volume/activity units). Like
// AmountValues, the unit adjacency is the built-in calibration that keeps bare prose numbers OUT: a
// count ("3 invoices"), a percentage ("20%"), a weight ("72 kg", kg not a dosage unit) and a bare
// number ("HR 68") carry no dosage unit, so none is returned. Exposed so the output guard classifies
// dosage answer-values by the same value-SHAPE test the extractor uses.
func QuantityValues(text string) []QuantityValue {
	qs := findQuantities(text)
	out := make([]QuantityValue, len(qs))
	for i, q := range qs {
		out[i] = QuantityValue{Value: q.value, Raw: text[q.start:q.end], Unit: q.unit, Start: q.start, End: q.end}
	}
	return out
}

// hasBlankLine reports whether s contains a paragraph break (a blank line). Mirrors
// labelfact.hasBlankLine — a label and amount split by one are in different segments.
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
		default:
			nl = -1
		}
	}
	return false
}
