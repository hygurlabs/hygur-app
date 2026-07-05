package keyed

import (
	"testing"

	"github.com/hygur/sidecar/internal/contradict"
)

// plateNorm must canonicalize a separator-less / partially-separated subject plate to the SAME key the
// query/claim path produces via contradict.NormKey — otherwise the certificate's plate never joins the
// vehicle typed in the question. Fictional plates.
func TestPlateNorm_MatchesNormKey(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"1ABC234", "1 abc 234"},
		{"1-ABC-234", "1 abc 234"},
		{"1-ABC234", "1 abc 234"},
		{"GT139RR", "gt 139 rr"},
		{"GT-139-RR", "gt 139 rr"},
	}
	for _, c := range cases {
		if got := plateNorm(c.raw); got != c.want {
			t.Errorf("plateNorm(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
	// The hyphenated forms must equal NormKey exactly (the existing convention).
	if plateNorm("GT-139-RR") != contradict.NormKey("GT-139-RR") {
		t.Errorf("plateNorm != NormKey for hyphenated plate")
	}
}

// An AG-style certificate (no-reply, no claims) anchors assureur=AG + courtier=Lefevre to the subject
// plate — and NEVER surfaces the broker as the assureur. Fictional plate, real doc shape.
func TestInsuranceNodes_CertAnchorsAssureurNotBroker(t *testing.T) {
	title := "Votre certificat d'assurance automobile - 1ABC234"
	body := "Votre certificat d'assurance automobile. Votre assureur CRCFR@aginsurance.be www.aginsurance.be " +
		"Votre courtier 'LEFEVRE'BOB SRL bob.lefevre@globex.example. AG Insurance SA - Bruxelles."
	orgs := []string{"AG Insurance SA", "LEFEVRE'BOB SRL", "Banque nationale de Belgique"}

	nodes := InsuranceNodes(title, body, orgs)
	got := map[string]string{}
	for _, n := range nodes {
		if n.KeyNorm != "1 abc 234" || n.KeyType != "plate" || n.Kind != "vehicle" {
			t.Fatalf("bad key on node: %+v", n)
		}
		got[n.Attribute] = n.ValueRaw
	}
	if got["assureur"] != "AG Insurance" {
		t.Errorf("assureur = %q, want AG Insurance", got["assureur"])
	}
	if got["courtier"] != "Lefevre" {
		t.Errorf("courtier = %q, want Lefevre", got["courtier"])
	}
	// The law: Lefevre is NEVER the assureur.
	for _, n := range nodes {
		if n.Attribute == "assureur" && n.ValueRaw == "Lefevre" {
			t.Fatalf("broker surfaced as assureur — law violated")
		}
	}
}

// The Zoé shape: one self-contained document ties model + plate + Baloise (AUTO) + ARAG (PJ). All
// anchor to the plate, and the modèle is captured so a model → plate traversal can reach the assureur.
func TestInsuranceNodes_ZoeBaloiseAndPJ(t *testing.T) {
	body := "Vous trouverez les documents pour votre véhicule RENAULT ZOE immatriculé 1-XYZ234. " +
		"Analyse des besoins au contrat AUTO Baloise. Contrat ARAG – Protection Juridique."
	orgs := []string{"Baloise", "ARAG", "Globex Telecom"}

	nodes := InsuranceNodes("Fwd: Carte d'assurance provisoire - Protection Juridique", body, orgs)
	got := map[string]string{}
	for _, n := range nodes {
		if n.KeyNorm != "1 xyz 234" {
			t.Fatalf("plate key = %q, want 1 xyz 234 (%+v)", n.KeyNorm, n)
		}
		got[n.Attribute] = n.ValueRaw
	}
	if got["assureur"] != "Baloise" {
		t.Errorf("assureur = %q, want Baloise", got["assureur"])
	}
	if got["protection juridique"] != "ARAG" {
		t.Errorf("PJ = %q, want ARAG", got["protection juridique"])
	}
	if got["modele"] == "" || got["modele"] != "RENAULT ZOE" {
		t.Errorf("modele = %q, want RENAULT ZOE", got["modele"])
	}
}

// Anchor-or-decline: a prospective quote (no certificate marker) and a broker-only document both anchor
// NOTHING — the insurer must never be guessed from an offer, and the courtier alone is never surfaced.
func TestInsuranceNodes_DeclineQuoteAndBrokerOnly(t *testing.T) {
	// Pure quote — no certificate/contract marker.
	if n := InsuranceNodes("Demande de cotation voiture société", "Bonjour, voici notre offre de cotation. Plaque 1-ABC-234.", []string{"AG Insurance"}); len(n) != 0 {
		t.Errorf("prospective cotation anchored %d nodes, want 0", len(n))
	}
	// Certificate present but only a courtier named (no assureur) → decline.
	if n := InsuranceNodes("Votre certificat d'assurance automobile - 1ABC234", "Votre courtier 'LEFEVRE'BOB SRL.", []string{"LEFEVRE'BOB SRL"}); len(n) != 0 {
		t.Errorf("broker-only cert anchored %d nodes, want 0 (no assureur)", len(n))
	}
}
