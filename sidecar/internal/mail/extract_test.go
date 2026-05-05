package mail

import (
	"testing"
)

func TestDetectHighPriority_AccountingKeyword(t *testing.T) {
	subject := "Déclaration TVA 1er trimestre 2026"
	body := "Veuillez trouver les instructions de paiement..."
	highPriority, hits := detectHighPriority(subject, body, "noreply@example.com")
	if !highPriority {
		t.Fatalf("expected high_priority=true, got false")
	}
	if len(hits) == 0 {
		t.Errorf("expected at least one keyword hit")
	}
	foundTVA, foundPaiement := false, false
	for _, h := range hits {
		if h == "tva" {
			foundTVA = true
		}
		if h == "paiement" {
			foundPaiement = true
		}
	}
	if !foundTVA {
		t.Errorf("expected 'tva' in hits, got %v", hits)
	}
	if !foundPaiement {
		t.Errorf("expected 'paiement' in hits, got %v", hits)
	}
}

func TestDetectHighPriority_AccountingDomain(t *testing.T) {
	subject := "Newsletter mensuelle"
	body := "Découvrez nos nouveautés ce mois."
	highPriority, hits := detectHighPriority(subject, body, "info@example.test")
	if !highPriority {
		t.Fatalf("expected high_priority=true for accounting domain, got false")
	}
	foundDomain := false
	for _, h := range hits {
		if h == "domain:example.test" {
			foundDomain = true
		}
	}
	if !foundDomain {
		t.Errorf("expected domain match in hits, got %v", hits)
	}
}

func TestDetectHighPriority_Newsletter_NotPriority(t *testing.T) {
	subject := "Découvrez nos nouveaux produits"
	body := "Cliquez ici pour voir les promotions de la semaine."
	highPriority, hits := detectHighPriority(subject, body, "marketing@example.com")
	if highPriority {
		t.Errorf("expected high_priority=false for newsletter, got true with hits %v", hits)
	}
}

func TestDetectHighPriority_PartialWordNoMatch(t *testing.T) {
	// "tvasports" or "vati" shouldn't trigger TVA/VAT matches.
	subject := "TVASports updates"
	body := "vatican news this week"
	highPriority, hits := detectHighPriority(subject, body, "news@example.com")
	if highPriority {
		t.Errorf("expected high_priority=false (substring not word match), got true with hits %v", hits)
	}
}

func TestEnrichMetadataWithTier1_TVAEmail(t *testing.T) {
	subject := "Déclaration TVA - 0x0800 - 1er trimestre 2026 [FID0000000]"
	body := `Cher Client,
Montant : 7 421,85 €
IBAN : BE68 5390 0754 7034
Communication : +++090/9337/55493+++
Fiduciaire de la Cense & Associés`
	from := "compta@example.test"

	metadata := map[string]any{}
	enrichMetadataWithTier1(metadata, subject, body, from)

	if _, ok := metadata["extracted_iban"]; !ok {
		t.Errorf("expected extracted_iban in metadata")
	}
	if _, ok := metadata["extracted_amounts"]; !ok {
		t.Errorf("expected extracted_amounts in metadata")
	}
	if _, ok := metadata["extracted_structured_comm"]; !ok {
		t.Errorf("expected extracted_structured_comm in metadata")
	}
	hp, ok := metadata["high_priority"].(bool)
	if !ok || !hp {
		t.Errorf("expected high_priority=true, got %v", metadata["high_priority"])
	}
	if _, ok := metadata["accounting_keywords"]; !ok {
		t.Errorf("expected accounting_keywords in metadata")
	}
}

func TestEnrichMetadataWithTier1_NewsletterNoPriorityFlag(t *testing.T) {
	subject := "Newsletter hebdomadaire"
	body := "Découvrez nos articles ce mois-ci."
	from := "newsletter@example.com"

	metadata := map[string]any{}
	enrichMetadataWithTier1(metadata, subject, body, from)

	if _, ok := metadata["high_priority"]; ok {
		t.Errorf("expected no high_priority key for newsletter, got %v", metadata["high_priority"])
	}
	if _, ok := metadata["extracted_iban"]; ok {
		t.Errorf("expected no extracted_iban for newsletter")
	}
}

func TestContainsWord(t *testing.T) {
	tests := []struct {
		text, kw string
		want     bool
	}{
		{"the tva is due", "tva", true},
		{"tvasports today", "tva", false},
		{"watch tva.", "tva", true},
		{"facture impayée", "facture", true},
		{"facturation interne", "facture", false},
		{"", "tva", false},
		{"tva", "tva", true},
	}
	for _, tc := range tests {
		got := containsWord(tc.text, tc.kw)
		if got != tc.want {
			t.Errorf("containsWord(%q, %q) = %v, want %v", tc.text, tc.kw, got, tc.want)
		}
	}
}
