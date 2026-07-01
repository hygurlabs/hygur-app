package contradict

import "testing"

func TestClosedNegative(t *testing.T) {
	mk := func(attr, val, at string) Claim {
		return Claim{Attribute: attr, Value: val, AssertedAt: at}
	}

	// Terminal-negative outcome values (FR/EN, accented) → closed.
	for _, cs := range [][]Claim{
		{mk("status", "candidature refusée", "2026-06-01")},
		{mk("status", "rejected due to lack of experience", "2026-06-01")},
		{mk("status", "annulé", "2026-06-01")},
		{mk("delivery status", "renvoyée sans traitement", "2026-06-01")},
		{mk("status", "démission de manière inattendue", "2026-06-01")},
		{mk("outcome", "unsuccessful", "2026-06-01")},
		{mk("decision", "profil non retenu", "2026-06-01")},
		{mk("status", "réponse négative (Tarif trop élevé)", "2026-06-01")},
	} {
		if ok, _ := ClosedNegative(cs); !ok {
			t.Errorf("expected CLOSED for %q", cs[0].Value)
		}
	}

	// Open / positive / non-outcome → not closed (incl. the "disclosure" false-positive
	// trap and a negative word on a non-outcome attribute).
	for _, cs := range [][]Claim{
		{mk("status", "signée", "2026-06-01")},
		{mk("status", "en cours de livraison", "2026-06-01")},
		{mk("status", "full disclosure provided", "2026-06-01")},
		{mk("price", "refus catégorique", "2026-06-01")},
		{},
	} {
		if ok, _ := ClosedNegative(cs); ok {
			t.Errorf("expected NOT closed for %+v", cs)
		}
	}

	// Latest outcome wins (bi-temporal): an old rejection reopened later is not closed.
	if ok, _ := ClosedNegative([]Claim{
		mk("status", "rejected", "2026-01-01"),
		mk("status", "reopened, in progress", "2026-06-01"),
	}); ok {
		t.Error("latest status (reopened) should not be closed")
	}
	// …and a later rejection closes as of its date.
	if ok, when := ClosedNegative([]Claim{
		mk("status", "in progress", "2026-01-01"),
		mk("status", "finally declined", "2026-06-01"),
	}); !ok || when != "2026-06-01" {
		t.Errorf("latest rejection should close as of 2026-06-01, got ok=%v when=%q", ok, when)
	}
}

func TestThreadKey(t *testing.T) {
	for in, want := range map[string]string{
		"Re: TechSquare Opportunity - RESA":           "techsquare opportunity - resa",
		"RE: RE: Fwd:  TechSquare Opportunity - RESA": "techsquare opportunity - resa",
		"TR: Facture": "facture",
		"Devis 2026":  "devis 2026",
	} {
		if got := ThreadKey(in); got != want {
			t.Errorf("ThreadKey(%q) = %q, want %q", in, got, want)
		}
	}
	if ThreadKey("Re: Projet X") != ThreadKey("RE: Projet X") {
		t.Error("replies to the same subject should share a thread key")
	}
}
