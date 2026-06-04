package extract

import (
	"reflect"
	"testing"
)

func TestTier1_BelgianStructuredComm(t *testing.T) {
	text := "Communication : +++090/9337/55493+++"
	got := ExtractTier1(text)
	want := []string{"+++090/9337/55493+++"}
	if !reflect.DeepEqual(got.StructuredCommunications, want) {
		t.Errorf("got %v, want %v", got.StructuredCommunications, want)
	}
}

func TestTier1_StructuredComm_Deduplication(t *testing.T) {
	text := `Référence: +++090/9337/55493+++
Veuillez utiliser la communication +++090/9337/55493+++ pour ce paiement.`
	got := ExtractTier1(text)
	if len(got.StructuredCommunications) != 1 {
		t.Errorf("expected 1 unique communication, got %d: %v", len(got.StructuredCommunications), got.StructuredCommunications)
	}
}

func TestTier1_IBAN_BE_WithSpaces(t *testing.T) {
	text := "IBAN : BE68 5390 0754 7034"
	got := ExtractTier1(text)
	want := []string{"BE68539007547034"}
	if !reflect.DeepEqual(got.IBANs, want) {
		t.Errorf("got %v, want %v", got.IBANs, want)
	}
}

func TestTier1_IBAN_BE_NoSpaces(t *testing.T) {
	text := "Mon IBAN est BE68539007547034 pour ce virement."
	got := ExtractTier1(text)
	want := []string{"BE68539007547034"}
	if !reflect.DeepEqual(got.IBANs, want) {
		t.Errorf("got %v, want %v", got.IBANs, want)
	}
}

func TestTier1_IBAN_MultipleCountries(t *testing.T) {
	text := `Belgian: BE68 5390 0754 7034
French: FR1420041010050500013M02606
German: DE89 3704 0044 0532 0130 00`
	got := ExtractTier1(text)
	if len(got.IBANs) != 3 {
		t.Fatalf("expected 3 IBANs, got %d: %v", len(got.IBANs), got.IBANs)
	}
}

func TestTier1_Amount_EuropeanFormat(t *testing.T) {
	tests := []struct {
		text    string
		wantVal string
	}{
		{"Montant : 7 421,85 €", "7421.85"},
		{"7421.85 EUR", "7421.85"},
		{"Solde de 1.234,56 €", "1234.56"},
		{"Total: 12,50 €", "12.50"},
		{"À payer 89,47 € avant échéance", "89.47"},
		{"Le montant est de 100 EUR", "100"},
	}
	for _, tc := range tests {
		t.Run(tc.text, func(t *testing.T) {
			got := ExtractTier1(tc.text)
			if len(got.Amounts) != 1 {
				t.Fatalf("expected 1 amount, got %d: %v", len(got.Amounts), got.Amounts)
			}
			if got.Amounts[0].Value != tc.wantVal {
				t.Errorf("got value %q, want %q", got.Amounts[0].Value, tc.wantVal)
			}
			if got.Amounts[0].Currency != "EUR" {
				t.Errorf("got currency %q, want EUR", got.Amounts[0].Currency)
			}
		})
	}
}

func TestTier1_Amount_NonBreakingSpace(t *testing.T) {
	// French typography emits U+00A0 (NBSP) as the thousands separator.
	// Regression: prior to Unicode-space normalization, "7 421,85" was
	// matched as "421,85" because \s in RE2 only covers ASCII whitespace.
	text := "Montant : 7 421,85 €"
	got := ExtractTier1(text)
	if len(got.Amounts) != 1 {
		t.Fatalf("expected 1 amount, got %d: %v", len(got.Amounts), got.Amounts)
	}
	if got.Amounts[0].Value != "7421.85" {
		t.Errorf("got value %q, want 7421.85 (NBSP thousands sep)", got.Amounts[0].Value)
	}
}

func TestTier1_VATNumber_BE_FR(t *testing.T) {
	text := `TVA: BE0123456789
Numéro TVA français FR12345678901
Pas de TVA luxembourgeoise`
	got := ExtractTier1(text)
	wantBE := "BE0123456789"
	wantFR := "FR12345678901"
	foundBE, foundFR := false, false
	for _, v := range got.VATNumbers {
		if v == wantBE {
			foundBE = true
		}
		if v == wantFR {
			foundFR = true
		}
	}
	if !foundBE {
		t.Errorf("missing BE VAT %q in %v", wantBE, got.VATNumbers)
	}
	if !foundFR {
		t.Errorf("missing FR VAT %q in %v", wantFR, got.VATNumbers)
	}
}

func TestTier1_VATNumber_RejectsInvalidLength(t *testing.T) {
	// FR VAT must be exactly 11 digits — reject 5 digits.
	text := "FR 123 should not match"
	got := ExtractTier1(text)
	if len(got.VATNumbers) != 0 {
		t.Errorf("expected 0 VAT numbers, got %v", got.VATNumbers)
	}
}

func TestTier1_URL(t *testing.T) {
	text := `Visit https://example.com/path?q=1 for more info.
See also http://other.example.org.`
	got := ExtractTier1(text)
	if len(got.URLs) != 2 {
		t.Fatalf("expected 2 URLs, got %d: %v", len(got.URLs), got.URLs)
	}
	if got.URLs[0] != "https://example.com/path?q=1" {
		t.Errorf("got %q, want trailing punctuation stripped", got.URLs[0])
	}
	if got.URLs[1] != "http://other.example.org" {
		t.Errorf("got %q, want trailing dot stripped", got.URLs[1])
	}
}

func TestTier1_TVAPaymentEmail_Integration(t *testing.T) {
	// Realistic TVA email body with all entity types.
	text := `Cher Client,
Sur base des documents et des informations reçus, nous avons envoyé la déclaration TVA de EXMPL.
Il en ressort que vous restez redevable de la somme de 7 421,85 €.

Instructions de paiement
service T.V.A.-Recettes Bruxelles
Montant : 7 421,85 €
A payer avant le : 25 avril 2026
IBAN : BE68 5390 0754 7034
BIC : GEBABEBB
Communication : +++090/9337/55493+++

Fiduciaire de la Cense & Associés
019/63 27 14
TVA: BE0123456789
https://example.test`

	got := ExtractTier1(text)

	if len(got.IBANs) != 1 || got.IBANs[0] != "BE68539007547034" {
		t.Errorf("IBANs: got %v, want [BE68539007547034]", got.IBANs)
	}
	if len(got.StructuredCommunications) != 1 || got.StructuredCommunications[0] != "+++090/9337/55493+++" {
		t.Errorf("StructuredCommunications: got %v", got.StructuredCommunications)
	}
	if len(got.Amounts) < 1 || got.Amounts[0].Value != "7421.85" {
		t.Errorf("Amounts: got %v, want first value 7421.85", got.Amounts)
	}
	foundVAT := false
	for _, v := range got.VATNumbers {
		if v == "BE0123456789" {
			foundVAT = true
			break
		}
	}
	if !foundVAT {
		t.Errorf("VATNumbers: missing BE0123456789 in %v", got.VATNumbers)
	}
	if len(got.URLs) < 1 {
		t.Errorf("URLs: got %v, want at least 1", got.URLs)
	}
}

func TestTier1_NewsletterEmail_NoFinancialEntities(t *testing.T) {
	text := `Découvrez nos nouveautés !
Cliquez ici pour vous désabonner: https://example.com/unsubscribe
Cordialement, l'équipe Newsletter`
	got := ExtractTier1(text)
	if len(got.IBANs) != 0 {
		t.Errorf("expected 0 IBANs, got %v", got.IBANs)
	}
	if len(got.StructuredCommunications) != 0 {
		t.Errorf("expected 0 structured comms, got %v", got.StructuredCommunications)
	}
	if len(got.Amounts) != 0 {
		t.Errorf("expected 0 amounts, got %v", got.Amounts)
	}
}

func TestTier1_Count(t *testing.T) {
	text := "IBAN BE68 5390 0754 7034 montant 100 EUR communication +++090/9337/55493+++"
	got := ExtractTier1(text)
	want := 3 // 1 IBAN + 1 amount + 1 structured comm
	if got.Count() != want {
		t.Errorf("Count() = %d, want %d (entities: %+v)", got.Count(), want, got)
	}
}

func TestTier1_DueDate_FrenchVariants(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"Montant à payer avant le 25 avril 2026", "25 avril 2026"},
		{"Échéance : 25/04/2026", "25/04/2026"},
		{"date d'échéance le 25-04-2026", "25-04-2026"},
		{"Avant le 25 avril 2026 svp", "25 avril 2026"},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			got := ExtractTier1(tc.text)
			if len(got.DueDates) != 1 || got.DueDates[0] != tc.want {
				t.Errorf("DueDates = %v, want [%q]", got.DueDates, tc.want)
			}
		})
	}
}

func TestTier1_DueDate_EnglishVariants(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"Payment due by 25 April 2026", "25 April 2026"},
		{"Due before 25/04/2026", "25/04/2026"},
		{"Please remit due on 25 April 2026", "25 April 2026"},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			got := ExtractTier1(tc.text)
			if len(got.DueDates) != 1 || got.DueDates[0] != tc.want {
				t.Errorf("DueDates = %v, want [%q]", got.DueDates, tc.want)
			}
		})
	}
}

func TestTier1_DueDate_NoTriggerWord_NoMatch(t *testing.T) {
	// Plain dates without a trigger phrase shouldn't be picked up.
	text := "Le 25 avril 2026 nous serons disponibles"
	got := ExtractTier1(text)
	if len(got.DueDates) != 0 {
		t.Errorf("expected no due dates without trigger, got %v", got.DueDates)
	}
}

func TestTier1_DueDate_DeduplicatesIdenticalDates(t *testing.T) {
	text := `À payer avant le 25 avril 2026.
Échéance : 25 avril 2026.`
	got := ExtractTier1(text)
	if len(got.DueDates) != 1 {
		t.Errorf("expected dedup to 1 date, got %v", got.DueDates)
	}
}

func TestNormalizeAmount(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"7421.85", "7421.85"},
		{"7 421,85", "7421.85"},
		{"7.421,85", "7421.85"},
		{"7,421.85", "7421.85"},
		{"100", "100"},
		{"1234", "1234"},
		{"1.000.000,50", "1000000.50"},
		{"abc", ""},
	}
	for _, tc := range tests {
		got := normalizeAmount(tc.in)
		if got != tc.want {
			t.Errorf("normalizeAmount(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
