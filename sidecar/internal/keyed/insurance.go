package keyed

// Vehicle INSURANCE anchor — the courtier-vs-assureur model (docs/VEHICLE_INSURANCE_FIX_PLAN.md).
//
// A vehicle's insurance is NOT one flat "insurance" attribute: it is
//
//	{ courtier (broker, common to every vehicle), ASSUREUR (per-vehicle underwriter), protection juridique }.
//
// The founder's broker "Lefevre" (`'LEFEVRE'BOB SRL`, boblefevreassurances.be) is a COURTIER,
// never the insurer — surfacing it as the assureur is the exact bug this file forecloses. The real
// underwriters are AG Insurance (Model Y + moto), Baloise + ARAG (the wife's Zoé), Sogessur (Model X).
//
// The anchor is DETERMINISTIC (no LLM) and runs on EVERY insurance certificate / contract / card,
// including the AG certificate mails whose sender is a no-reply (so no semantic claims are ever
// extracted from them — the claim path can never anchor them). It reads:
//   - the PLATE from the mail SUBJECT/TITLE ("… certificat d'assurance automobile - 2HAT495") AND from
//     labelled body spans ("véhicule RENAULT ZOE immatriculé 2-FWY272", "Pl : MBQV633"), because the
//     certificate carries the plate ONLY in the subject/title, not the boilerplate body;
//   - the ASSUREUR / COURTIER / PJ from the document's orgs + body, classified by a bounded registry
//     that keeps the broker OUT of the assureur bucket — the law: never surface the courtier as insurer.
//
// It emits attribute nodes anchored to the plate key: assureur / courtier / protection juridique, plus
// a modèle when a vehicle make+model sits next to the plate (so "l'assurance de la Zoé" traverses
// model → plate → assureur). A document with a plate but no ASSUREUR anchors nothing for insurance —
// anchor-or-decline. Established-fact gate: only certificates / contracts / insurance cards anchor; a
// bare "demande de cotation / devis / offre" (no certificate marker) is prospective and is NOT anchored.

import (
	"regexp"
	"sort"
	"strings"

	"github.com/hygur/sidecar/internal/store"
)

// insurer is one bounded registry row: a canonical display name, the role (assureur | courtier | pj),
// and the folded match tokens that identify it in an org string or the body. The role is what enforces
// the law — a token in the `courtier` role can NEVER be emitted as an assureur.
type insurer struct {
	canonical string
	role      string // "assureur" | "courtier" | "pj"
	tokens    []string
}

// insurerRegistry is the bounded courtier/assureur/PJ registry. Extensible by data (a new underwriter =
// a new row), generic in shape. The broker (Lefevre) is pinned to the COURTIER role so it is structurally
// impossible to surface it as the assureur.
var insurerRegistry = []insurer{
	{canonical: "AG Insurance", role: "assureur", tokens: []string{"ag insurance", "aginsurance"}},
	{canonical: "Baloise", role: "assureur", tokens: []string{"baloise", "baloise insurance", "baloise belgium"}},
	{canonical: "Sogessur", role: "assureur", tokens: []string{"sogessur"}},
	{canonical: "ARAG", role: "pj", tokens: []string{"arag"}},
	{canonical: "Lefevre", role: "courtier", tokens: []string{"lefevre", "boblefevreassurances", "bob lefevre"}},
}

// insuranceDocMarkers are the established-fact markers (folded) that make a document an insurance
// CERTIFICATE / CONTRACT / CARD — a real policy artefact, not a prospective quote. At least one must be
// present for any anchoring to happen. "relevé d'information / de sinistralité" is included so an
// insurer's history statement (the Sogessur relevé) anchors too.
var insuranceDocMarkers = []string{
	"certificat d assurance", "certificat d’assurance", "carte d assurance", "carte d’assurance",
	"carte verte", "attestation d assurance", "contrat auto", "contrat d assurance", "police d assurance",
	"assurance provisoire", "assurance automobile", "releve d information", "releve de sinistralite",
	"protection juridique",
}

// insurancePlateContextRe captures the plate token that follows an insurance/immatriculation label —
// the SAFE way to read a separator-less plate out of a subject/body without matching order refs. It
// matches after " - " at the end of a certificate title, or after immatricul/plaque/"Pl :" markers.
var insurancePlateContextRe = regexp.MustCompile(
	`(?i)(?:immatricul[eé]?\s*|plaque\s*(?:d['’]?immatriculation\s*)?:?\s*|\bpl\s*:?\s*|assurance automobile\s*-\s*|carte verte\s*-\s*)([0-9A-Za-z]{1,7}(?:[ -][0-9A-Za-z]{1,6}){0,3})`)

// vehicleMakes is a bounded set of car/moto makes used to read a "modèle" next to a plate (make + up to
// two following tokens). Generic brand names, not founder-specific — extensible by data.
var vehicleMakes = []string{
	"tesla", "renault", "peugeot", "citroen", "citroën", "volkswagen", "audi", "bmw", "mercedes",
	"toyota", "opel", "ford", "volvo", "kia", "hyundai", "nissan", "fiat", "zero", "harley", "yamaha",
	"honda", "kawasaki", "ktm", "vespa", "piaggio",
}

var wordRe = regexp.MustCompile(`[0-9A-Za-zé]+`)

// vehicleTypes are distinctive vehicle-CLASS words (folded) that, when a policy names one next to a
// plate, anchor a "description" so a class-word question ("l'assureur de ma moto") can traverse type →
// plate → assureur. Deliberately NOT the generic "voiture/car" (every car matches, so the model → plate
// guard would just decline) — only classes that pick out a distinct vehicle in a household. The retrieval
// side still fails closed if a class word matches more than one plate.
var vehicleTypes = []string{"moto", "motos", "motorcycle", "scooter", "cyclomoteur", "camionnette", "utilitaire"}

// plateNorm canonicalizes a plate token to the SAME space-separated form the keyed layer already uses
// for query/claim plates (contradict.NormKey → "gt 139 rr"), REPAIRING separator-less or partially
// separated subject plates by inserting a boundary at every letter↔digit transition. So the certificate
// subject "2HAT495", the body "2-FWY272" and the query "2-HAT-495" all canonicalize to a matching key —
// without touching RecognizeKeys (whose strict shape the rest of the system depends on).
//
//	"2HAT495"   → "2 hat 495"   (== NormKey("2-HAT-495"))
//	"2-FWY272"  → "2 fwy 272"   (== NormKey("2-FWY-272"))
//	"GT-139-RR" → "gt 139 rr"
//
// The moto "MBQV633" → "mbqv 633" (its letter run has no internal boundary); that vehicle resolves by
// type/model, not by a plate typed into the query, so the exact grouping is immaterial to matching.
func plateNorm(raw string) string {
	var b strings.Builder
	var prevClass byte // 'd' digit, 'a' letter, 0 = none/separator
	for _, r := range strings.ToLower(raw) {
		var class byte
		switch {
		case r >= '0' && r <= '9':
			class = 'd'
		case r >= 'a' && r <= 'z':
			class = 'a'
		default:
			b.WriteByte(' ')
			prevClass = 0
			continue
		}
		if prevClass != 0 && prevClass != class {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
		prevClass = class
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// isInsuranceDoc reports whether the title/body carry an established insurance-policy marker.
func isInsuranceDoc(foldedTitle, foldedBody string) bool {
	hay := foldedTitle + " \n " + foldedBody
	for _, m := range insuranceDocMarkers {
		if strings.Contains(hay, m) {
			return true
		}
	}
	return false
}

// fold lowercases and normalizes apostrophes so registry/marker matching is punctuation-stable.
func fold(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("’", " ", "'", " ", " ", " ", "-", " ").Replace(s)
	return s
}

// extractPlateNorms returns the distinct canonical plate keys named in an insurance document, read from
// the labelled subject/body spans only (never a free scan — that would catch order refs / the company
// name 0x0800). Each candidate is confirmed by validPlate (5–8 alnum, both a letter and a digit).
func extractPlateNorms(title, body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range insurancePlateContextRe.FindAllStringSubmatch(title+"\n"+body, -1) {
		cand := strings.TrimSpace(m[1])
		if !validPlate(cand) {
			continue
		}
		n := plateNorm(cand)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// classifyInsurers scans the document's orgs + body against the registry and returns the distinct
// canonical names found per role. The broker can only ever land in `courtiers`.
func classifyInsurers(orgs []string, foldedBody string) (assureurs, pjs, courtiers []string) {
	hay := foldedBody
	for _, o := range orgs {
		hay += " \n " + fold(o)
	}
	seen := map[string]bool{}
	for _, ins := range insurerRegistry {
		hit := false
		for _, tok := range ins.tokens {
			if strings.Contains(hay, tok) {
				hit = true
				break
			}
		}
		if !hit || seen[ins.canonical] {
			continue
		}
		seen[ins.canonical] = true
		switch ins.role {
		case "assureur":
			assureurs = append(assureurs, ins.canonical)
		case "pj":
			pjs = append(pjs, ins.canonical)
		case "courtier":
			courtiers = append(courtiers, ins.canonical)
		}
	}
	sort.Strings(assureurs)
	sort.Strings(pjs)
	sort.Strings(courtiers)
	return assureurs, pjs, courtiers
}

// extractModele reads a "make + up to two tokens" vehicle model out of the body when a known make is
// present (e.g. "RENAULT ZOE" → "renault zoe"). Returns "" when no make is found. Used only to power
// the model → plate traversal; never overrides a determined modèle from the claim path.
func extractModele(body string) (norm, raw string) {
	tokens := wordRe.FindAllString(body, -1)
	lower := make([]string, len(tokens))
	for i, t := range tokens {
		lower[i] = strings.ToLower(t)
	}
	for i, lw := range lower {
		for _, mk := range vehicleMakes {
			if lw == mk {
				end := i + 1
				for end < len(tokens) && end <= i+2 {
					// stop at obviously non-model tokens (digits-only, immatricul…)
					nt := lower[end]
					if nt == "immatricule" || nt == "immatriculé" || strings.HasPrefix(nt, "immatric") {
						break
					}
					end++
				}
				parts := tokens[i:end]
				rawM := strings.Join(parts, " ")
				return plateModeleNorm(rawM), rawM
			}
		}
	}
	return "", ""
}

// plateModeleNorm folds a model string to the value grouping key (lowercase alnum words).
func plateModeleNorm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// InsuranceNodes anchors a vehicle's ASSUREUR / COURTIER / protection juridique (and, opportunistically,
// its modèle) to its PLATE key from an insurance certificate / contract / card. Deterministic, no LLM,
// safe to run on every document (it self-gates on the insurance markers + labelled plate). The law is
// intrinsic: Lefevre is registry-pinned to the courtier role, and a plate with no assureur anchors no
// insurer at all (honest decline). title = mail subject, body = display/normalized text, orgs =
// metadata.extracted_orgs.
func InsuranceNodes(title, body string, orgs []string) []store.AttrNode {
	ft, fb := fold(title), fold(body)
	if !isInsuranceDoc(ft, fb) {
		return nil
	}
	plates := extractPlateNorms(title, body)
	if len(plates) == 0 {
		return nil
	}
	assureurs, pjs, courtiers := classifyInsurers(orgs, fb)
	// Anchor-or-decline: without a real ASSUREUR (or PJ underwriter) there is nothing honest to anchor —
	// a courtier alone is never surfaced as the insurer.
	if len(assureurs) == 0 && len(pjs) == 0 {
		return nil
	}
	modNorm, modRaw := extractModele(body)
	descNorm, descRaw := extractVehicleType(fb)
	var out []store.AttrNode
	for _, p := range plates {
		for _, a := range assureurs {
			out = append(out, store.AttrNode{
				KeyNorm: p, KeyType: "plate", Kind: "vehicle",
				Attribute: "assureur", AttrRaw: "assureur",
				Value: plateModeleNorm(a), ValueRaw: a, Prox: 1.0,
			})
		}
		for _, pj := range pjs {
			out = append(out, store.AttrNode{
				KeyNorm: p, KeyType: "plate", Kind: "vehicle",
				Attribute: "protection juridique", AttrRaw: "protection juridique",
				Value: plateModeleNorm(pj), ValueRaw: pj, Prox: 1.0,
			})
		}
		for _, c := range courtiers {
			out = append(out, store.AttrNode{
				KeyNorm: p, KeyType: "plate", Kind: "vehicle",
				Attribute: "courtier", AttrRaw: "courtier",
				Value: plateModeleNorm(c), ValueRaw: c, Prox: 1.0,
			})
		}
		if modNorm != "" {
			out = append(out, store.AttrNode{
				KeyNorm: p, KeyType: "plate", Kind: "vehicle",
				Attribute: "modele", AttrRaw: "modèle",
				Value: modNorm, ValueRaw: modRaw, Prox: 1.0,
			})
		}
		if descNorm != "" {
			out = append(out, store.AttrNode{
				KeyNorm: p, KeyType: "plate", Kind: "vehicle",
				Attribute: "description", AttrRaw: "type",
				Value: descNorm, ValueRaw: descRaw, Prox: 1.0,
			})
		}
	}
	return out
}

// extractVehicleType returns the first distinctive vehicle-class word present in the folded body (e.g.
// "moto"), used to anchor a "description" for the type → plate traversal. "" when none is present.
func extractVehicleType(foldedBody string) (norm, raw string) {
	for _, t := range vehicleTypes {
		if strings.Contains(foldedBody, t) {
			// canonicalize a couple of variants to their base class
			base := t
			if t == "motos" || t == "motorcycle" {
				base = "moto"
			}
			return base, base
		}
	}
	return "", ""
}
