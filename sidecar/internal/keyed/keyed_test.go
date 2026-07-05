package keyed

import (
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/store"
)

// The plate is recognized as a keyed-entity anchor; unrelated hyphenated / numeric tokens are NOT
// (the shape is the calibration). All fixtures are fictional.
func TestRecognizeKeys_Plate(t *testing.T) {
	keys := RecognizeKeys("mon véhicule GT-139-RR est une Tesla")
	if len(keys) != 1 {
		t.Fatalf("want 1 key, got %d (%+v)", len(keys), keys)
	}
	k := keys[0]
	if k.Kind != "vehicle" || k.KeyType != "plate" {
		t.Errorf("kind/type = %q/%q, want vehicle/plate", k.Kind, k.KeyType)
	}
	if k.Norm != contradict.NormKey("GT-139-RR") {
		t.Errorf("norm = %q, want %q", k.Norm, contradict.NormKey("GT-139-RR"))
	}
	if k.Norm != "gt 139 rr" {
		t.Errorf("norm = %q, want gt 139 rr", k.Norm)
	}
}

func TestRecognizeKeys_NonPlates(t *testing.T) {
	// An order reference (no hyphen), a date (all-digit groups), a national-number-shaped run, and a
	// bare word must NOT be recognized as plates.
	for _, s := range []string{
		"order ref RN124646486 confirmed",
		"meeting on 2023-06-15 at noon",
		"numéro 23.02.23-347.71 national",
		"the covid-19 update",
		"Tesla Model Y leasing KBC",
	} {
		if keys := RecognizeKeys(s); len(keys) != 0 {
			t.Errorf("%q recognized keys %+v, want none", s, keys)
		}
	}
}

// A claim whose ENTITY is the plate anchors its (attribute, value) to the plate key; a claim naming a
// DIFFERENT keyed entity (or none) never anchors to this plate — distinct entities stay declined.
func TestAttrNodesFromClaims_Anchoring(t *testing.T) {
	claims := []contradict.Claim{
		{Entity: "GT-139-RR", Attribute: "modèle", Value: "Tesla Model X 2023", Polarity: "affirm", Quote: "GT-139-RR : Tesla Model X 2023"},
		// Model Y — a DIFFERENT vehicle (company order ref), no plate → must NOT anchor to GT-139-RR.
		{Entity: "Tesla Model Y", Attribute: "modèle", Value: "Tesla Model Y", Polarity: "affirm", Quote: "commande RN124646486 Tesla Model Y"},
		// A negation is skipped.
		{Entity: "GT-139-RR", Attribute: "statut", Value: "vendu", Polarity: "negate", Quote: "GT-139-RR n'est pas vendu"},
	}
	nodes := AttrNodesFromClaims(claims)
	if len(nodes) != 1 {
		t.Fatalf("want 1 anchored node, got %d (%+v)", len(nodes), nodes)
	}
	n := nodes[0]
	if n.KeyNorm != "gt 139 rr" {
		t.Errorf("key_norm = %q, want gt 139 rr", n.KeyNorm)
	}
	if n.Value != contradict.NormKey("Tesla Model X 2023") || n.ValueRaw != "Tesla Model X 2023" {
		t.Errorf("value = %q/%q, want the Model X value", n.Value, n.ValueRaw)
	}
}

// INVERSE REGISTRATION ANCHOR: the real-world claim shape « Tesla Model X 2023 (registration number)
// GT-139-RR » (model = entity, plate = value) must anchor « GT-139-RR → modèle = Tesla Model X 2023 ».
func TestAttrNodesFromClaims_InverseRegistration(t *testing.T) {
	claims := []contradict.Claim{
		{Entity: "Tesla Model X 2023", Attribute: "registration number", Value: "GT-139-RR", Polarity: "affirm",
			Quote: "Tesla Model X 2023, immatriculation GT-139-RR"},
	}
	nodes := AttrNodesFromClaims(claims)
	var got *store.AttrNode
	for i := range nodes {
		if nodes[i].KeyNorm == "gt 139 rr" && nodes[i].Attribute == "modele" {
			got = &nodes[i]
		}
	}
	if got == nil {
		t.Fatalf("inverse anchor missing: want gt 139 rr → modele, got %+v", nodes)
	}
	if got.ValueRaw != "Tesla Model X 2023" {
		t.Errorf("inverse model value = %q, want Tesla Model X 2023", got.ValueRaw)
	}
}

// The inverse anchor must NOT fire for a QUOTE (company Model Y « immatriculé au nom de la société »
// inside a cotation) — decline-on-quote still wins, so the company model never lands via a price offer.
func TestAttrNodesFromClaims_InverseRegistrationQuoteDeclines(t *testing.T) {
	claims := []contradict.Claim{
		{Entity: "Tesla Model Y", Attribute: "immatriculation", Value: "AA-111-BB", Polarity: "affirm",
			Quote: "cotation voiture société : Tesla Model Y immatriculé AA-111-BB au nom de la société"},
	}
	if nodes := AttrNodesFromClaims(claims); len(nodes) != 0 {
		t.Fatalf("inverse anchor must respect decline-on-quote, got %+v", nodes)
	}
}

// A claim whose entity is a generic word ("véhicule") but whose verbatim QUOTE contains the plate
// anchors by proximity within the asserted span.
func TestAttrNodesFromClaims_QuoteProximity(t *testing.T) {
	claims := []contradict.Claim{
		{Entity: "véhicule", Attribute: "modèle", Value: "Tesla Model X", Polarity: "affirm", Quote: "le véhicule GT-139-RR est une Tesla Model X"},
	}
	nodes := AttrNodesFromClaims(claims)
	if len(nodes) != 1 || nodes[0].KeyNorm != "gt 139 rr" {
		t.Fatalf("quote-proximity anchoring failed: %+v", nodes)
	}
}

// DECLINE-ON-QUOTE (fail-closed): a « cotation »/omnium PRICE OFFER for the company Model Y is NOT an
// active policy and must NEVER anchor as a determined « assurance » — even though the claim carries a
// hard key (the company plate). The founder has no CBC policy; a proposal is not a fact. (PII-safe:
// plates fictional.)
func TestAttrNodesFromClaims_DeclineOnQuote(t *testing.T) {
	claims := []contradict.Claim{
		// A QUOTE: the omnium is proposed, not held. Carries a plate key, yet must be declined.
		{Entity: "AA-111-BB", Attribute: "assurance", Value: "full omnium CBC", Polarity: "affirm",
			Quote: "cotation voiture société AA-111-BB : full omnium CBC 1200 EUR/an"},
		// A DEVIS for a loyer: prospective financing offer, not an active lease payment.
		{Entity: "AA-111-BB", Attribute: "loyer", Value: "690 EUR/mois", Polarity: "affirm",
			Quote: "devis renting KBC AA-111-BB loyer estimé 690 EUR/mois"},
	}
	if nodes := AttrNodesFromClaims(claims); len(nodes) != 0 {
		t.Fatalf("quote/offer claims must decline (anchor nothing), got %+v", nodes)
	}
}

// DECLINE-ON-AMBIGUOUS (fail-closed): an attribute tied only to « votre Tesla » — no plate, no VIN, no
// owner key — is not anchorable to EITHER vehicle and must produce no node. Never guess which vehicle.
func TestAttrNodesFromClaims_DeclineOnAmbiguous(t *testing.T) {
	claims := []contradict.Claim{
		{Entity: "votre Tesla", Attribute: "assurance", Value: "full omnium", Polarity: "affirm",
			Quote: "l'assurance de votre Tesla est en full omnium"},
	}
	if nodes := AttrNodesFromClaims(claims); len(nodes) != 0 {
		t.Fatalf("keyless « votre Tesla » attribute must decline, got %+v", nodes)
	}
}

// CLUSTER NO-LEAK: the company vehicle's loyer/insurance claims (anchored to ITS OWN key, or keyless)
// never fill the personal GT-139-RR's cluster. Only the plate-named model anchors to GT-139-RR.
func TestAttrNodesFromClaims_ClusterStaysDistinct(t *testing.T) {
	claims := []contradict.Claim{
		{Entity: "GT-139-RR", Attribute: "modèle", Value: "Tesla Model X 2023", Polarity: "affirm",
			Quote: "GT-139-RR : Tesla Model X 2023"},
		// Company Model Y loyer — anchored to the company's OWN plate, an established lease (not a quote).
		{Entity: "AA-111-BB", Attribute: "loyer", Value: "690 EUR/mois", Polarity: "affirm",
			Quote: "contrat renting AA-111-BB loyer 690 EUR/mois"},
	}
	nodes := AttrNodesFromClaims(claims)
	for _, n := range nodes {
		if n.KeyNorm == "gt 139 rr" && n.Attribute != contradict.NormKey("modèle") {
			t.Errorf("company attribute %q leaked into GT-139-RR", n.Attribute)
		}
		if n.KeyNorm == "gt 139 rr" && strings.Contains(n.Value, "690") {
			t.Errorf("company loyer leaked into GT-139-RR: %+v", n)
		}
	}
	// The company loyer anchored to its own key, distinctly.
	var sawCompanyLoyer bool
	for _, n := range nodes {
		if n.KeyNorm == "aa 111 bb" && n.Attribute == contradict.NormKey("loyer") {
			sawCompanyLoyer = true
		}
	}
	if !sawCompanyLoyer {
		t.Errorf("company loyer should anchor to its own key aa 111 bb: %+v", nodes)
	}
}

// Two documents that AGREE resolve to the value (corroborated); a plate with no attribute declines
// (empty). A same-key disagreement resolves latest-wins, or declines when unorderable.
func TestResolveAttributes(t *testing.T) {
	old := time.Now().Add(-72 * time.Hour)
	newer := time.Now().Add(-24 * time.Hour)

	// Agreement across two docs.
	agree := []store.AttrNode{
		{ContentID: "e1", KeyNorm: "gt 139 rr", Attribute: "modele", AttrRaw: "modèle", Value: "tesla model x 2023", ValueRaw: "Tesla Model X 2023", DocDate: old},
		{ContentID: "e2", KeyNorm: "gt 139 rr", Attribute: "modele", AttrRaw: "modèle", Value: "tesla model x 2023", ValueRaw: "Tesla Model X 2023", DocDate: newer},
	}
	got := ResolveAttributes(agree)
	if len(got) != 1 || got[0].Value != "Tesla Model X 2023" || got[0].State != "corroborated" || got[0].Corroboration != 2 {
		t.Fatalf("agreement resolution = %+v", got)
	}

	// Disagreement ordered by document date → latest wins (superseded).
	disagree := []store.AttrNode{
		{ContentID: "e1", KeyNorm: "gt 139 rr", Attribute: "modele", Value: "model 3", ValueRaw: "Model 3", DocDate: old},
		{ContentID: "e2", KeyNorm: "gt 139 rr", Attribute: "modele", Value: "tesla model x", ValueRaw: "Tesla Model X", DocDate: newer},
	}
	got = ResolveAttributes(disagree)
	if len(got) != 1 || got[0].Value != "Tesla Model X" || got[0].State != "superseded" {
		t.Fatalf("temporal resolution = %+v", got)
	}

	// Disagreement that cannot be ordered (no dates) → declined (dropped, fail-closed).
	unorderable := []store.AttrNode{
		{ContentID: "e1", KeyNorm: "gt 139 rr", Attribute: "modele", Value: "model 3", ValueRaw: "Model 3"},
		{ContentID: "e2", KeyNorm: "gt 139 rr", Attribute: "modele", Value: "tesla model x", ValueRaw: "Tesla Model X"},
	}
	if got = ResolveAttributes(unorderable); len(got) != 0 {
		t.Fatalf("unorderable disagreement should decline, got %+v", got)
	}
}
