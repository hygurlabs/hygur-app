package figure

import "testing"

func TestParseAmount(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"7 421,85", "7421.85"}, // FR: space grouping, comma decimal
		{"7 421,85", "7421.85"}, // FR: non-breaking space grouping
		{"7.421,85", "7421.85"}, // FR: dot grouping, comma decimal
		{"7,421.85", "7421.85"}, // US: comma grouping, dot decimal
		{"1000", "1000"},        // plain integer
		{"1 000", "1000"},       // space grouping, no decimal
		{"12,50", "12.50"},      // comma decimal only
		{"0,99", "0.99"},
		{"1234567,89", "1234567.89"},
	}
	for _, c := range cases {
		got, ok := parseAmount(c.in)
		if !ok || got != c.want {
			t.Errorf("parseAmount(%q) = %q,%v; want %q", c.in, got, ok, c.want)
		}
	}
}

func TestFindPeriod(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"déclaration TVA Q1 2026", "2026-Q1"},
		{"trimestre T2/2026", "2026-Q2"},
		{"1er trimestre 2026", "2026-Q1"},
		{"3ème trimestre de 2025", "2025-Q3"},
		{"exercice 2026", "2026"},
		{"mars 2026", "2026-03"},
		{"no period here", ""},
	}
	for _, c := range cases {
		got, _ := findPeriod(c.in)
		if got != c.want {
			t.Errorf("findPeriod(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeDirection(t *testing.T) {
	cases := []struct{ in, want string }{
		{"à payer", DirPayable},
		{"net à payer", DirPayable},
		{"remboursée", DirRefund},
		{"TVA à récupérer", DirRefund},
		{"en votre faveur", DirRefund},
		{"acompte", DirAdvance},
		{"", ""},
		{"quelque chose", ""},
	}
	for _, c := range cases {
		if got := NormalizeDirection(c.in); got != c.want {
			t.Errorf("NormalizeDirection(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeFigureLabel_generic(t *testing.T) {
	// The label mechanism is generic (a seed table), VAT is the only F1 seed. Synonyms resolve;
	// an unseeded label FAILS CLOSED (empty) rather than minting a garbage figure type.
	if NormalizeFigureLabel("TVA") != "vat" {
		t.Error("TVA should normalize to vat")
	}
	if NormalizeFigureLabel("VAT") != "vat" {
		t.Error("VAT should normalize to vat")
	}
	if NormalizeFigureLabel("ma TVA") != "vat" {
		t.Error("a phrase containing the seed token should resolve")
	}
	if NormalizeFigureLabel("chiffre d'affaires") != "" {
		t.Error("an unseeded label must fail closed (empty) in F1")
	}
}

// TestExtract_VATPayable is the real VAT-declaration shape (masked amount): a to-pay figure with a
// quarter, addressed to the owner. The extractor binds the amount to the TVA label with its context.
func TestExtract_VATPayable(t *testing.T) {
	text := "Objet: Déclaration TVA T1 2026\n\nBonjour Denis,\n" +
		"Votre déclaration TVA du 1er trimestre 2026 est finalisée.\n" +
		"Montant de TVA à payer: 7 421,85 €\n" +
		"Merci de procéder au paiement avant le 20 avril."
	figs := Extract(text)
	if len(figs) != 1 {
		t.Fatalf("expected 1 figure, got %d: %+v", len(figs), figs)
	}
	f := figs[0]
	if f.Label != "vat" || f.Value != "7421.85" || f.Unit != "EUR" {
		t.Errorf("node wrong: %+v", f)
	}
	if f.Direction != DirPayable {
		t.Errorf("direction = %q; want payable", f.Direction)
	}
	if f.Period != "2026-Q1" {
		t.Errorf("period = %q; want 2026-Q1", f.Period)
	}
}

// TestExtract_VATRefund: a refund figure (different direction) — the label is the same, the
// direction is what distinguishes it. "TVA remboursée" must never be conflated with "TVA à payer".
func TestExtract_VATRefund(t *testing.T) {
	text := "Déclaration TVA T2 2026.\nTVA remboursée: 1 250,00 €.\n"
	figs := Extract(text)
	if len(figs) != 1 {
		t.Fatalf("expected 1 figure, got %d: %+v", len(figs), figs)
	}
	if figs[0].Direction != DirRefund {
		t.Errorf("direction = %q; want refund", figs[0].Direction)
	}
	if figs[0].Value != "1250.00" {
		t.Errorf("value = %q", figs[0].Value)
	}
}

// TestExtract_noBareAmount: an amount with NO seeded financial label in reach is NOT extracted
// (fail-closed — a bare amount is not a figure).
func TestExtract_noBareAmount(t *testing.T) {
	text := "Facture énergie: 342,10 € à régler avant fin du mois."
	if figs := Extract(text); len(figs) != 0 {
		t.Errorf("expected no figures for an unlabelled amount, got %+v", figs)
	}
}

// TestExtract_bothDirections: a document carrying BOTH a to-pay and a refund VAT figure yields two
// distinct nodes — the resolver later disambiguates by direction.
func TestExtract_bothDirections(t *testing.T) {
	text := "TVA T3 2026.\nTVA à payer: 900,00 €.\nPar ailleurs, TVA remboursée: 120,00 €.\n"
	figs := Extract(text)
	if len(figs) != 2 {
		t.Fatalf("expected 2 figures, got %d: %+v", len(figs), figs)
	}
	dirs := map[string]bool{}
	for _, f := range figs {
		dirs[f.Direction] = true
	}
	if !dirs[DirPayable] || !dirs[DirRefund] {
		t.Errorf("expected both payable and refund, got %+v", dirs)
	}
}
