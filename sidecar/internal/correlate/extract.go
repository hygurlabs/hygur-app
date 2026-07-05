package correlate

import (
	"regexp"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/keyed"
	"github.com/hygur/sidecar/internal/recognize"
)

// Doc is a minimal ingested document view — exactly what the mini-DB / knowledge_items row provides.
type Doc struct {
	ID    string
	Title string // mail subject (carries the AG cert plate)
	Body  string // normalized/display text
	Orgs  []string
	Date  time.Time
}

// vinRe matches a 17-char VIN (ISO 3779: no I/O/Q). The both-a-letter-and-a-digit guard below keeps
// a 17-char all-alpha/all-digit run from being mistaken for a VIN.
var vinRe = regexp.MustCompile(`\b[A-HJ-NPR-Z0-9]{17}\b`)

// emailRe matches an email address (the broker/spouse hard key).
var emailRe = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)

// priceRe matches a European-formatted monetary amount followed by €/EUR/euros — a real figure from
// the document (never invented). Captures the amount; requires the currency marker so a bare number
// (a date, a count) is not read as a price.
var priceRe = regexp.MustCompile(`(?i)(\d{1,3}(?:[ .]\d{3})*(?:,\d{2})?)\s*(?:€|eur\b|euros\b)`)

// ObservationsFromDoc turns one document into the entity OBSERVATIONS it evidences, reusing the
// deployed deterministic extractors (keyed.InsuranceNodes for the plate + assureur/courtier/PJ/modèle
// under the courtier≠assureur registry; keyed.RecognizeKeys for strict-shape plates; a VIN + IBAN +
// enterprise recognizer for the other hard keys; an email recognizer for contact keys). It SEGMENTS
// a doc into entities by key TYPE — vehicle keys (plate, VIN) group into ONE vehicle observation
// carrying the insurance facts; each email becomes its OWN contact observation (so the broker and the
// spouse self-merge across documents by their shared address, not by name). This segmentation is the
// one residual heuristic; the CORE merge (Correlate) is fully generic over whatever observations it
// is handed.
func ObservationsFromDoc(d Doc) []Observation {
	var out []Observation
	text := d.Title + "\n" + d.Body

	// --- Vehicle observation: plate/VIN keys + insurance attributes ---------------------------------
	var vehKeys []KeyRef
	var vehAttrs []Attr
	plateSeen := map[string]bool{}

	// Insurance anchor: plate (from subject/labelled body) + assureur/courtier/PJ/modèle, role-typed.
	for _, n := range keyed.InsuranceNodes(d.Title, d.Body, d.Orgs) {
		if n.KeyType == "plate" && !plateSeen[n.KeyNorm] {
			plateSeen[n.KeyNorm] = true
			vehKeys = append(vehKeys, KeyRef{Type: "plate", Value: n.KeyNorm})
		}
		vehAttrs = append(vehAttrs, attrFromNodeReal(n.Attribute, n.AttrRaw, n.Value, n.ValueRaw))
	}
	// Strict-shape plates anywhere in the text (belt-and-braces; deduped against the above).
	for _, k := range keyed.RecognizeKeys(text) {
		if k.KeyType == "plate" && !plateSeen[k.Norm] {
			plateSeen[k.Norm] = true
			vehKeys = append(vehKeys, KeyRef{Type: "plate", Value: k.Norm})
		}
	}
	// VIN — a vehicle key. Grouped with the plate: a plate and a VIN in the SAME doc are one car.
	for _, v := range vinRe.FindAllString(strings.ToUpper(text), -1) {
		if hasLetterAndDigit(v) {
			vehKeys = appendKey(vehKeys, KeyRef{Type: "vin", Value: strings.ToLower(v)})
		}
	}
	// Price — surfaced only as a fact when present; the traversal declines when absent or ambiguous.
	for _, m := range priceRe.FindAllStringSubmatch(text, -1) {
		raw := strings.TrimSpace(m[0])
		val := strings.ReplaceAll(strings.TrimSpace(m[1]), " ", "")
		vehAttrs = append(vehAttrs, Attr{Name: "prix", Role: RoleNone, Value: val, Raw: raw})
	}
	if len(vehKeys) > 0 {
		out = append(out, Observation{DocID: d.ID, Kind: "vehicle", Hard: vehKeys, Attrs: vehAttrs, Date: d.Date})
	}

	// --- Contact observations: one per distinct email (broker/spouse self-merge across docs) --------
	emailSeen := map[string]bool{}
	for _, e := range emailRe.FindAllString(text, -1) {
		e = strings.ToLower(e)
		if emailSeen[e] || isNoiseEmail(e) {
			continue
		}
		emailSeen[e] = true
		out = append(out, Observation{
			DocID: d.ID, Kind: "contact",
			Hard: []KeyRef{{Type: "email", Value: e}},
			Soft: nameNear(d.Body, e),
			Date: d.Date,
		})
	}

	// --- Identity keys (IBAN / enterprise / national number) as their own anchored observations -----
	for _, t := range recognize.Recognize(text) {
		out = append(out, Observation{
			DocID: d.ID, Kind: "identity",
			Hard: []KeyRef{{Type: t.Type, Value: t.Value}},
			Date: d.Date,
		})
	}
	return out
}

// attrFromNodeReal maps a keyed insurance AttrNode (by its exported fields) to a role-typed correlate
// Attr. The role is what makes the courtier≠assureur law structural: a Lefevre node arrives as
// attribute "courtier" and is tagged RoleCourtier, so the vehicle traversal can never read it as the
// insurer.
func attrFromNodeReal(attribute, attrRaw, value, valueRaw string) Attr {
	role := RoleNone
	name := attribute
	switch attribute {
	case "assureur":
		role = RoleAssureur
	case "courtier":
		role = RoleCourtier
	case "protection juridique":
		role = RolePJ
	}
	raw := valueRaw
	if raw == "" {
		raw = value
	}
	return Attr{Name: name, Role: role, Value: value, Raw: raw}
}

func appendKey(keys []KeyRef, k KeyRef) []KeyRef {
	for _, e := range keys {
		if e.Type == k.Type && e.Value == k.Value {
			return keys
		}
	}
	return append(keys, k)
}

func hasLetterAndDigit(s string) bool {
	hasL, hasD := false, false
	for _, r := range s {
		switch {
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
			hasL = true
		case r >= '0' && r <= '9':
			hasD = true
		}
	}
	return hasL && hasD
}

// isNoiseEmail drops addresses that are not entity anchors: no-reply/system senders that would merge
// unrelated docs by a shared robot address. Bounded, conservative.
func isNoiseEmail(e string) bool {
	for _, n := range []string{"no-reply", "noreply", "no_reply", "donotreply", "mailer-daemon", "postmaster"} {
		if strings.Contains(e, n) {
			return true
		}
	}
	return false
}

// nameNear returns a proper-name soft label sitting immediately before the email in "Name <email>"
// form, if any — corroboration only (never a merge key). Best-effort, bounded to a short window.
func nameNear(body, email string) []string {
	idx := strings.Index(strings.ToLower(body), email)
	if idx <= 0 {
		return nil
	}
	win := body[max0(idx-60):idx]
	if lt := strings.LastIndex(win, "<"); lt >= 0 {
		win = win[:lt]
	}
	win = strings.Trim(strings.TrimSpace(win), "'\"")
	fields := strings.Fields(win)
	// keep the trailing 1-3 capitalized tokens (a name), drop connective words
	var name []string
	for i := len(fields) - 1; i >= 0 && len(name) < 3; i-- {
		f := strings.Trim(fields[i], "'\":,")
		if f == "" || !startsUpper(f) {
			break
		}
		name = append([]string{f}, name...)
	}
	if len(name) == 0 {
		return nil
	}
	return []string{strings.Join(name, " ")}
}

func startsUpper(s string) bool {
	r := []rune(s)
	return len(r) > 0 && r[0] >= 'A' && r[0] <= 'Z'
}
func max0(x int) int {
	if x < 0 {
		return 0
	}
	return x
}
