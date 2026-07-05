// Package keyed is the GENERIC keyed-entity anchor (docs/GENERALIZATION_PLAN.md): the owner-anchor
// (identifier → person, eb7d089/448af00) generalized from the PERSON to ANY entity that carries a
// hard KEY. A keyed entity — a vehicle by its PLATE, a bike by its serial, a cat by its ISO chip, a
// phone by its IMEI — is a NODE anchored by that key; its attributes (model, year…) are DETERMINED
// facts anchored to the key. The spike proved linking must go through the DURE key, never through
// name/embedding similarity (which conflates a Model X / Model Y / Model 3), so this package anchors
// on the key and NEVER on a name.
//
// The mechanism is GENERIC — a bounded registry of key FORMATS (data, not code) drives recognition;
// the attribute extraction + resolution are format-agnostic. The vehicle/plate is the first (and,
// tonight, only) instance: adding bike/cat/phone is adding a format row, not new logic.
package keyed

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/store"
)

// Key is one recognized keyed-entity anchor: its family (Kind/KeyType), its canonical Norm (the
// anchor — aligned with the entity-index NormKey so it matches claim entities and query subjects),
// the Raw span as written, and its byte offsets.
type Key struct {
	Kind    string // "vehicle"
	KeyType string // "plate"
	Norm    string // canonical anchor ("gt 139 rr")
	Raw     string // as written ("GT-139-RR")
	Start   int
	End     int
}

// format is one recognizable key FORMAT — the bounded, extensible registry (GENERALIZATION_PLAN §1).
// Each family (plate, serial, chip, imei…) is a row: a matcher regex + a validity predicate. No new
// code per family — just a new row. Tonight only the plate family is registered.
type format struct {
	kind    string
	keyType string
	re      *regexp.Regexp
	valid   func(raw string) bool
}

// plateRe matches a license-plate-shaped token: hyphen-separated groups mixing letters and digits,
// in one of a bounded set of national shapes (Belgian and the GT-139-RR style). Word-bounded and
// case-insensitive. The shape itself is the calibration that keeps unrelated tokens out: an order
// reference (no hyphen), a VAT/IBAN (no letter-then-digit plate shape), a date (all-digit groups)
// and a national number (all-digit) do NOT match — so a plate is anchored, those are not.
var plateRe = regexp.MustCompile(`(?i)\b(?:[a-z]{1,3}-[0-9]{2,4}-[a-z0-9]{1,3}|[0-9]{1,3}-[a-z]{2,3}-[0-9]{2,4}|[a-z]{2,3}-[0-9]{3,4})\b`)

// formats is the key-format registry. Bounded + generic: bike serial / cat chip / phone IMEI are
// future rows here, each with its own matcher — the recognizer, anchoring and resolution below are
// unchanged. Plate is the sole instance tonight (the vehicle slice).
var formats = []format{
	{kind: "vehicle", keyType: "plate", re: plateRe, valid: validPlate},
}

// validPlate confirms a plate candidate: after removing separators it is 5–8 alphanumerics carrying
// BOTH a letter and a digit (a plate is never all-letters or all-digits). This is the guard that
// separates a plate from a coincidental hyphenated token.
func validPlate(raw string) bool {
	var alnum strings.Builder
	hasLetter, hasDigit := false, false
	for _, r := range strings.ToLower(raw) {
		switch {
		case r >= 'a' && r <= 'z':
			hasLetter = true
			alnum.WriteRune(r)
		case r >= '0' && r <= '9':
			hasDigit = true
			alnum.WriteRune(r)
		}
	}
	n := alnum.Len()
	return hasLetter && hasDigit && n >= 5 && n <= 8
}

// RecognizeKeys returns every keyed-entity anchor found in text, deterministically (no LLM). Overlaps
// are resolved by keeping the first match per format; results are ordered by position.
func RecognizeKeys(text string) []Key {
	var out []Key
	for _, f := range formats {
		for _, m := range f.re.FindAllStringIndex(text, -1) {
			raw := text[m[0]:m[1]]
			if f.valid != nil && !f.valid(raw) {
				continue
			}
			out = append(out, Key{
				Kind: f.kind, KeyType: f.keyType,
				Norm: contradict.NormKey(raw), Raw: strings.ToUpper(raw),
				Start: m[0], End: m[1],
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// KeysInQuery returns the distinct keyed-entity anchors NAMED in a query (deduplicated by Norm), so
// the determined-facts layer can resolve "the model of my vehicle GT-139-RR" straight to the
// plate-anchored node — no reliance on the fuzzy n-gram subject detector.
func KeysInQuery(query string) []Key {
	seen := map[string]bool{}
	var out []Key
	for _, k := range RecognizeKeys(query) {
		if k.Norm == "" || seen[k.Norm] {
			continue
		}
		seen[k.Norm] = true
		out = append(out, k)
	}
	return out
}

// AttrNodesFromClaims anchors each claim to a KEY it names — the generic keyed-entity attribute
// extractor. A claim becomes a keyed attribute of a key when that key appears in the claim's ENTITY
// (the claim is literally ABOUT the key — the strongest anchor) or, failing that, in its verbatim
// QUOTE (the attribute value sits next to the key in a tight span — proximity anchoring). The KEY is
// the anchor: a claim naming a DIFFERENT key, or no key at all, never fills THIS key's attribute —
// "distinct entities stay declined", generalized from person-identity to entity-identity. Negations
// and empty attribute/value claims are skipped. Deterministic, no LLM.
func AttrNodesFromClaims(claims []contradict.Claim) []store.AttrNode {
	var out []store.AttrNode
	for _, c := range claims {
		if c.Polarity == "negate" {
			continue
		}
		attr := contradict.NormKey(c.Attribute)
		val := contradict.NormKey(c.Value)
		if attr == "" || val == "" {
			continue
		}
		// PROSPECTIVE-CONTEXT DECLINE (fail-closed): a QUOTE/OFFER/PROJECTION is not an established
		// fact. A « cotation » / « devis » / « offre de prix » for a Model Y omnium is a PRICE OFFER,
		// not an active policy — anchoring it as a determined attribute is exactly the conflation the
		// founder rejects (« I have NO insurance at CBC »). Such a claim is dropped: anchor-or-decline,
		// never assert a proposal as truth. Applies before key anchoring so no prospective value can
		// fill any key's cluster.
		if isProspective(c.Attribute + " " + c.Value + " " + c.Quote + " " + c.Entity) {
			continue
		}
		// INVERSE REGISTRATION ANCHOR: the natural phrasing of a vehicle fact makes the MODEL the entity
		// and the PLATE the value — « Tesla Model X 2023 (registration number) GT-139-RR ». The direct
		// anchor below would miss the model (the plate is not in the entity/quote as a subject). So when
		// the attribute is a registration/immatriculation attribute and the VALUE carries the plate, we
		// invert: the plate's « modèle » is the entity. This is what puts « GT-139-RR → Model X » into the
		// determined layer so the chat stops re-deriving the vehicle's attributes from conflating RAG. It
		// is tightly gated (registration attribute + a real, non-key entity) so it never over-anchors.
		if isRegistrationAttr(attr) {
			// Strip a leading generic vehicle-class noun so « véhicule Tesla Model X 2023 » and « Tesla
			// Model X 2023 » (the SAME model, one carrying an extraction-noise prefix) anchor to the SAME
			// value — otherwise two documents that AGREE read as a disagreement and the attribute is
			// falsely marked « superseded », which makes the chat hedge the model as obsolete.
			ent := stripVehicleClassPrefix(strings.TrimSpace(c.Entity))
			if ent != "" && len(RecognizeKeys(c.Entity)) == 0 {
				for _, k := range RecognizeKeys(c.Value) {
					out = append(out, store.AttrNode{
						KeyNorm: k.Norm, KeyType: k.KeyType, Kind: k.Kind,
						Attribute: "modele", AttrRaw: "modèle",
						Value: contradict.NormKey(ent), ValueRaw: ent, Prox: 1.0,
					})
				}
			}
		}
		// Anchor on the KEY named in the claim's own entity first (precise); fall back to the key
		// named in its verbatim quote (proximity within the asserted span). A claim naming NO key —
		// e.g. an attribute tied only to « votre Tesla » with no plate/VIN/owner — anchors to nothing
		// and is declined here (never guessed onto a vehicle).
		keys := RecognizeKeys(c.Entity)
		if len(keys) == 0 {
			keys = RecognizeKeys(c.Quote)
		}
		for _, k := range keys {
			out = append(out, store.AttrNode{
				KeyNorm:   k.Norm,
				KeyType:   k.KeyType,
				Kind:      k.Kind,
				Attribute: attr,
				AttrRaw:   strings.TrimSpace(c.Attribute),
				Value:     val,
				ValueRaw:  strings.TrimSpace(c.Value),
				Prox:      1.0,
			})
		}
	}
	return out
}

// vehicleClassPrefixes are leading generic vehicle nouns (FR+EN) that carry NO model information. A
// claim entity « véhicule Tesla Model X 2023 » names the same model as « Tesla Model X 2023 »; the
// prefix is stripped before anchoring so two agreeing documents are not read as a false disagreement.
var vehicleClassPrefixes = []string{"véhicule", "vehicule", "vehicle", "voiture", "automobile", "auto", "car"}

// stripVehicleClassPrefix removes any leading run of generic vehicle-class nouns from s (case-folded
// prefix, space-delimited), preserving the original casing of the remainder. Bounded and idempotent.
func stripVehicleClassPrefix(s string) string {
	t := strings.TrimSpace(s)
	for {
		low := strings.ToLower(t)
		stripped := false
		for _, p := range vehicleClassPrefixes {
			if strings.HasPrefix(low, p+" ") {
				t = strings.TrimSpace(t[len(p):])
				stripped = true
				break
			}
		}
		if !stripped {
			return t
		}
	}
}

// registrationAttrMarkers are the folded attribute tokens that denote « this thing's registration /
// plate is … » — the claim shape whose VALUE is the plate and whose ENTITY is the vehicle. Bounded
// FR+EN. Used only to trigger the inverse anchor (plate → modèle = entity).
var registrationAttrMarkers = []string{"registration", "immatricul", "plaque", "plate", "licence"}

// isRegistrationAttr reports whether a normalized attribute denotes a vehicle's registration/plate.
func isRegistrationAttr(attr string) bool {
	for _, m := range registrationAttrMarkers {
		if strings.Contains(attr, m) {
			return true
		}
	}
	return false
}

// prospectiveMarkers are the case-folded tokens that frame a claim as a QUOTE / OFFER / PROJECTION
// rather than an established fact. Bounded and language-aware (FR + EN) for the vehicle-finance domain
// that motivated it (a Model Y omnium « cotation »), and generic enough to guard any attribute: a
// price offer, a draft, an estimate or a projection is prospective, so its value is NOT a determined
// truth. Fail-closed by design — the founder wants « anchor-or-decline, never guess a proposal ».
var prospectiveMarkers = []string{
	"cotation", "devis", "offre de prix", "offre de", "proposition", "simulation",
	"estimation", "projet d", "projet de", "prévisionnel", "previsionnel", "à titre indicatif",
	"quote", "quotation", "offer", "estimate", "draft", "proposal", "projected", "forecast",
}

// isProspective reports whether a span reads as a quote/offer/projection (see prospectiveMarkers).
// Case-insensitive substring test on the folded span. Kept deliberately conservative: it only fires
// on explicit prospective vocabulary, so it declines quotes without swallowing ordinary facts.
func isProspective(span string) bool {
	s := strings.ToLower(span)
	for _, m := range prospectiveMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// ResolvedAttr is one DETERMINED attribute of a keyed entity: the surviving value, how many distinct
// documents corroborate it, whether it superseded an older reading, and its sources.
type ResolvedAttr struct {
	Attribute     string   // display attribute ("modèle")
	Value         string   // display value ("Tesla Model X 2023")
	State         string   // "corroborated" | "superseded"
	Corroboration int      // distinct source documents for the surviving value
	Sources       []string // content_ids carrying the surviving value
}

// ResolveAttributes turns a key's raw attribute nodes into its DETERMINED attributes, deterministically
// and FAIL-CLOSED. Nodes are grouped by attribute; within an attribute the values are reconciled by
// TEMPORAL SUPERSESSION (the same rule figure.ResolveTemporal applies): if the documents AGREE on one
// value it stands (corroborated); if they DISAGREE the value from the LATEST document date wins
// (superseded); if a disagreement cannot be ordered (no dates, or a tie at the latest date holding
// several values) the attribute is DROPPED — never guessed, never averaged. Deterministic ordering:
// corroboration desc, then attribute.
func ResolveAttributes(nodes []store.AttrNode) []ResolvedAttr {
	if len(nodes) == 0 {
		return nil
	}
	byAttr := map[string][]store.AttrNode{}
	order := []string{}
	for _, n := range nodes {
		if _, ok := byAttr[n.Attribute]; !ok {
			order = append(order, n.Attribute)
		}
		byAttr[n.Attribute] = append(byAttr[n.Attribute], n)
	}
	var out []ResolvedAttr
	for _, attr := range order {
		grp := byAttr[attr]
		pick, superseded, ok := resolveTemporal(grp)
		if !ok {
			continue // contested + unorderable → decline this attribute (fail-closed)
		}
		srcs := map[string]bool{}
		var attrRaw string
		for _, n := range grp {
			if n.Value == pick.Value {
				if n.ContentID != "" {
					srcs[n.ContentID] = true
				}
				if attrRaw == "" && n.AttrRaw != "" {
					attrRaw = n.AttrRaw
				}
			}
		}
		if attrRaw == "" {
			attrRaw = attr
		}
		sources := make([]string, 0, len(srcs))
		for s := range srcs {
			sources = append(sources, s)
		}
		sort.Strings(sources)
		state := "corroborated"
		if superseded {
			state = "superseded"
		}
		out = append(out, ResolvedAttr{
			Attribute:     attrRaw,
			Value:         pick.ValueRaw,
			State:         state,
			Corroboration: len(sources),
			Sources:       sources,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Corroboration != out[j].Corroboration {
			return out[i].Corroboration > out[j].Corroboration
		}
		return out[i].Attribute < out[j].Attribute
	})
	return out
}

// resolveTemporal picks the surviving value for one attribute's nodes: agreement stands; a
// disagreement is resolved to the LATEST document date (superseded=true); an unorderable disagreement
// returns ok=false (decline). Mirrors figure.ResolveTemporal, kept local so the keyed layer stays
// decoupled from the figure store type.
func resolveTemporal(nodes []store.AttrNode) (pick store.AttrNode, superseded bool, ok bool) {
	if len(nodes) == 0 {
		return store.AttrNode{}, false, false
	}
	if distinctValues(nodes) == 1 {
		return nodes[0], false, true
	}
	var latest time.Time
	for _, n := range nodes {
		if n.DocDate.After(latest) {
			latest = n.DocDate
		}
	}
	if latest.IsZero() {
		return store.AttrNode{}, false, false // no dates to order by → decline
	}
	var top []store.AttrNode
	for _, n := range nodes {
		if n.DocDate.Equal(latest) {
			top = append(top, n)
		}
	}
	if distinctValues(top) != 1 {
		return store.AttrNode{}, false, false // latest date is itself contradictory → decline
	}
	return top[0], true, true
}

func distinctValues(nodes []store.AttrNode) int {
	m := map[string]bool{}
	for _, n := range nodes {
		m[n.Value] = true
	}
	return len(m)
}
