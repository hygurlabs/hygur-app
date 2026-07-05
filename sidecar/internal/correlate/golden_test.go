package correlate

import (
	"strings"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// GOLDEN HARNESS. Fixtures are SYNTHETIC/REDACTED — fake plates, VINs, emails and prices reproducing
// the SHAPE of the founder's four real vehicle documents (docs/AUTO_CORRELATION_PLAN.md §3). The real
// data lives only in a local mini-DB (scratchpad, never committed); these fixtures carry NO PII. Each
// mirrors what the deployed ingest actually extracts for that doc (verified against the mini-DB):
//   - AG certificate  → plate in the mail SUBJECT, AG + Lefevre in orgs, boilerplate body, NO price.
//   - Sogessur relevé → plate + Sogessur in an OCR'd body ("relevé d'information"), NO price.
//   - Zoé (spouse)    → plate + make/model + Baloise + ARAG in the body, a real prime (102,35 €),
//                       the spouse's email and the broker's email.
//   - Model Y cotation→ a VIN + model only (prospective quote): NO plate, NO assureur → not a vehicle
//                       we can surface (honest decline), and it must NOT merge into the AG cert.
// ─────────────────────────────────────────────────────────────────────────────────────────────────

const brokerEmail = "bob.lefevre@broker.test" // shared HARD key across all three broker docs
const spouseEmail = "anne.durand@spouse.test"     // the spouse's HARD key

func goldenDocs() []Doc {
	d := func(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }
	certBody := "Cher client, Votre nouveau certificat d'assurance automobile se trouve en pièce jointe. " +
		"Transférez cet e-mail à tous les conducteurs du véhicule. AG Insurance SA. " +
		"Votre courtier Bob Lefevre <" + brokerEmail + ">."
	return []Doc{
		// Model X — Sogessur (relevé, OCR'd body carries the plate + insurer)
		{
			ID:    "doc-modelx-sogessur",
			Title: "Relevé d'information assurance auto",
			Body: "Relevé d'information — assurance automobile. Véhicule TESLA MODEL X immatriculé XN-001-AA. " +
				"Compagnie SOGESSUR. Degré bonus-malus. Intermédiaire Bob Lefevre <" + brokerEmail + ">.",
			Orgs: []string{"Sogessur", "LEFEVRE'BOB SRL"},
			Date: d("2025-05-15"),
		},
		// Model Y — AG (certificate, plate in the SUBJECT)
		{
			ID:    "doc-modely-ag-cert",
			Title: "Votre certificat d'assurance automobile - 2YAT495",
			Body:  certBody,
			Orgs:  []string{"AG Insurance SA", "LEFEVRE'BOB SRL", "Banque nationale de Belgique"},
			Date:  d("2025-05-20"),
		},
		// Moto — AG (certificate, plate in the SUBJECT)
		{
			ID:    "doc-moto-ag-cert",
			Title: "Votre certificat d'assurance automobile - MZKV639",
			Body:  certBody,
			Orgs:  []string{"AG Insurance SA", "LEFEVRE", "Globex Telecom"},
			Date:  d("2025-06-01"),
		},
		// Zoé — Baloise + ARAG (spouse's car; plate + model + price + both emails in the body)
		{
			ID:    "doc-zoe-baloise",
			Title: "Carte d'assurance provisoire - DURAND Alice - ARAG - Protection Juridique",
			Body: "Bonjour Mme Durand, vous trouverez les documents pour votre véhicule RENAULT ZOE " +
				"immatriculé 2-FWZ-272. Carte d'assurance provisoire. Compagnie Baloise. Protection " +
				"juridique ARAG, prime de 102,35 €. Alice Durand <" + spouseEmail + ">. " +
				"Bob Lefevre <" + brokerEmail + ">.",
			Orgs: []string{"ARAG", "Baloise", "Globex Telecom"},
			Date: d("2024-09-23"),
		},
		// Model Y cotation — VIN + model only (prospective): must NOT become a vehicle, must NOT merge.
		{
			ID:    "doc-modely-cotation",
			Title: "Demande cotation voiture société",
			Body: "Je sollicite votre aide pour la couverture assurance d'un nouveau véhicule. " +
				"Tesla New Model Y Long Range : VIN ZZ9YGCEK7SB000041. Financement renting KBC.",
			Orgs: []string{"KBC"},
			Date: d("2025-05-10"),
		},
	}
}

func buildGraph(docs []Doc) *Graph {
	var obs []Observation
	for _, d := range docs {
		obs = append(obs, ObservationsFromDoc(d)...)
	}
	return Correlate(obs)
}

// TestGoldenVehicles is the GATE: the pipeline must surface the four vehicles, each with the correct
// INSURER (never the broker) and its price OR an honest decline — with ZERO confabulation.
func TestGoldenVehicles(t *testing.T) {
	g := buildGraph(goldenDocs())
	vs := g.Vehicles()

	// EXACTLY four vehicles — the cotation (VIN, no plate) is honestly NOT surfaced as one.
	if len(vs) != 4 {
		var got []string
		for _, v := range vs {
			got = append(got, v.Plate)
		}
		t.Fatalf("expected 4 vehicles, got %d: %v", len(vs), got)
	}

	// Expected golden result, keyed by plate norm. Insurer is the assureur; broker must be Lefevre;
	// price present only for the Zoé (the only doc carrying a real €-amount) — declined elsewhere.
	type want struct {
		insurer   string
		pj        string
		broker    string
		wantPrice bool
	}
	golden := map[string]want{
		"xn 001 aa":  {insurer: "Sogessur", broker: "Lefevre", wantPrice: false},
		"2 yat 495":  {insurer: "AG Insurance", broker: "Lefevre", wantPrice: false},
		"mzkv 639":   {insurer: "AG Insurance", broker: "Lefevre", wantPrice: false},
		"2 fwz 272":  {insurer: "Baloise", pj: "ARAG", broker: "Lefevre", wantPrice: true},
	}
	for _, v := range vs {
		w, ok := golden[v.Plate]
		if !ok {
			t.Errorf("unexpected vehicle plate %q", v.Plate)
			continue
		}
		if !strings.EqualFold(v.Insurer, w.insurer) {
			t.Errorf("[%s] insurer = %q, want %q", v.Plate, v.Insurer, w.insurer)
		}
		if w.pj != "" && !strings.EqualFold(v.PJ, w.pj) {
			t.Errorf("[%s] PJ = %q, want %q", v.Plate, v.PJ, w.pj)
		}
		if !strings.EqualFold(v.Broker, w.broker) {
			t.Errorf("[%s] broker = %q, want %q", v.Plate, v.Broker, w.broker)
		}
		// ZERO HALLUCINATION: the broker is NEVER surfaced as the insurer.
		if strings.Contains(strings.ToLower(v.Insurer), "lefevre") {
			t.Errorf("[%s] HALLUCINATION: broker surfaced as insurer (%q)", v.Plate, v.Insurer)
		}
		if strings.Contains(strings.ToLower(v.PJ), "lefevre") {
			t.Errorf("[%s] HALLUCINATION: broker surfaced as PJ (%q)", v.Plate, v.PJ)
		}
		// Price: present exactly when a real figure exists; otherwise honestly declined (never invented).
		if w.wantPrice {
			if v.Price == "" {
				t.Errorf("[%s] expected a price, got decline", v.Plate)
			}
		} else {
			if v.Price != "" {
				t.Errorf("[%s] INVENTED price %q (none in the facts)", v.Plate, v.Price)
			}
			if !contains(v.Declined, "price") {
				t.Errorf("[%s] price absent but not recorded as declined", v.Plate)
			}
		}
	}
}

// TestBrokerSelfMergesByHardKey proves the CORRELATOR is GENERIC: "Lefevre the broker is ONE entity
// seen in every certificate" is NOT hand-coded — it falls out of the union-find because the three
// certs share the broker's HARD key (email). One entity, three source docs, anchored by email.
func TestBrokerSelfMergesByHardKey(t *testing.T) {
	g := buildGraph(goldenDocs())
	broker := g.EntityByKey(KeyRef{Type: "email", Value: brokerEmail})
	if broker == nil {
		t.Fatal("broker entity not assembled from shared email key")
	}
	if len(broker.Docs) < 3 {
		t.Errorf("broker should merge across ≥3 docs, got %d: %v", len(broker.Docs), broker.Docs)
	}
}

// TestVinDoesNotMergeWithoutSharedKey proves the anchor-or-decline law at the LINK level: the
// cotation's VIN and the AG cert's plate are the same real car, but NO document ties them, so the
// correlator must NOT merge them (that would be a soft-name guess on "Model Y"). Honest separation.
func TestVinDoesNotMergeWithoutSharedKey(t *testing.T) {
	g := buildGraph(goldenDocs())
	vin := g.EntityByKey(KeyRef{Type: "vin", Value: "zz9ygcek7sb000041"})
	if vin == nil {
		t.Fatal("VIN entity missing")
	}
	if vin.HasKeyType("plate") {
		t.Errorf("VIN entity wrongly merged with a plate (no shared hard key): keys=%v", vin.Keys)
	}
	// And it must not surface as a 5th vehicle.
	for _, v := range g.Vehicles() {
		if strings.Contains(v.Plate, "xp7") {
			t.Errorf("VIN-only cotation surfaced as a vehicle: %+v", v)
		}
	}
}

// TestMergeIgnoresKind is the structural genericity guard: the merge must union by shared hard key
// REGARDLESS of the declared Kind. Two observations with DIFFERENT kinds but the SAME email are the
// same entity; two with the SAME kind but no shared key stay distinct.
func TestMergeIgnoresKind(t *testing.T) {
	obs := []Observation{
		{DocID: "a", Kind: "contact", Hard: []KeyRef{{Type: "email", Value: "x@y.test"}}},
		{DocID: "b", Kind: "person", Hard: []KeyRef{{Type: "email", Value: "x@y.test"}}}, // same key, other kind → MERGE
		{DocID: "c", Kind: "contact", Hard: []KeyRef{{Type: "email", Value: "z@y.test"}}}, // same kind, other key → SEPARATE
	}
	g := Correlate(obs)
	if len(g.Entities) != 2 {
		t.Fatalf("expected 2 entities (merge by key, not kind), got %d", len(g.Entities))
	}
	merged := g.EntityByKey(KeyRef{Type: "email", Value: "x@y.test"})
	if merged == nil || len(merged.Docs) != 2 {
		t.Fatalf("email-shared observations of different kinds did not merge: %+v", merged)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
