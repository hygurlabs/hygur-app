package labelfact

import (
	"testing"

	"github.com/hygur/sidecar/internal/recognize"
)

// All values below are FICTIONAL. No real founder data.

func TestNormalizeLabel(t *testing.T) {
	cases := map[string]string{
		// Family-B labels normalize + dealias.
		"D-U-N-S Number":   "duns",
		"DUNS":             "duns",
		"d.u.n.s.":         "duns",
		"Dun & Bradstreet": "duns",
		"SIRET":            "siret",
		"Order Number":     "order",
		"Customer ID":      "customer",
		"Membership No":    "membership",
		// Family-A synonyms dealias onto the checksum canonical (so a VAT/national query hits the
		// validated node) and are idempotent on the canonical keys themselves.
		"vat":               recognize.TypeEnterprise,
		"TVA":               recognize.TypeEnterprise,
		"enterprise_number": recognize.TypeEnterprise,
		"numéro national":   recognize.TypeNationalNumber,
		"national_number":   recognize.TypeNationalNumber,
		"IBAN":              recognize.TypeIBAN,
		"iban":              recognize.TypeIBAN,
		// Nothing usable → empty.
		"number":        "",
		"the reference": "",
		"":              "",
	}
	for in, want := range cases {
		if got := NormalizeLabel(in); got != want {
			t.Errorf("NormalizeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// one asserts Extract returns exactly one fact of the given type+value.
func one(t *testing.T, text, wantType, wantVal string) {
	t.Helper()
	got := Extract(text)
	if len(got) != 1 {
		t.Fatalf("Extract(%q) = %+v, want exactly one fact", text, got)
	}
	if got[0].Type != wantType || got[0].Value != wantVal {
		t.Fatalf("Extract(%q) = {%q,%q}, want {%q,%q}", text, got[0].Type, got[0].Value, wantType, wantVal)
	}
}

// none asserts Extract returns nothing (fail-closed).
func none(t *testing.T, text string) {
	t.Helper()
	if got := Extract(text); len(got) != 0 {
		t.Fatalf("Extract(%q) = %+v, want nothing extracted", text, got)
	}
}

func TestExtract_AdjacentSameLine(t *testing.T) {
	// The Apple case, adjacent form: "D-U-N-S Number: <value>" on one line → id_duns.
	one(t, "Dear founder, your D-U-N-S Number: 824190537 is now active.", "duns", "824190537")
	// A bare acronym + colon.
	one(t, "SIRET: 731148520 (registered).", "siret", "731148520")
	// Cue-derived label root.
	one(t, "Order number: 900112233 confirmed.", "order", "900112233")
	// Value written with in-value separators still normalizes.
	one(t, "D-U-N-S Number: 82-419-0537.", "duns", "824190537")
}

func TestExtract_DocumentLevelSingle(t *testing.T) {
	// Label in the subject line, value alone in the body, exactly one of each → doc-level bind.
	text := "Your D-U-N-S Number is enclosed\n\nCongratulations, it is now active.\n\n824190537\n"
	one(t, text, "duns", "824190537")
}

func TestExtract_DocLevelSurvivesFooterAcronyms(t *testing.T) {
	// The real Apple-mail shape: the label and value sit in one sentence but are separated by an
	// entity ("… for 0X0800 is …"), and the footer carries bare acronyms (MS, CA, TM). Only ONE
	// identifier-grade value and ONE strong label (D-U-N-S) → doc-level bind to id_duns; the weak
	// footer acronyms must not defeat it.
	text := "Subject: Your D-U-N-S Number is enclosed.\n\nDear Someone,\n\n" +
		"The D-U-N-S Number for 0X0800 is 824190537. If you have the authority you can use " +
		"this number to enroll for your company.\n\n" +
		"One Apple Park Way, MS 301-1TEV, Cupertino, CA 95014. TM and © 2026.\n"
	one(t, text, "duns", "824190537")
}

func TestExtract_GenericNumberBackReference(t *testing.T) {
	// "this number"/"the number" is a back-reference, not a label root — it must NOT mint a
	// spurious id_use/id_the label that would defeat the real one (regression from the Apple mail).
	none(t, "You can use this number 900112233 to enroll for your company.")
	none(t, "Please quote the number 900112233 when you call.")
}

func TestExtract_MultipleAdjacentPairs(t *testing.T) {
	// Two unambiguous same-line pairs → both bind (adjacency is not ambiguous).
	got := Extract("SIRET: 111111119 and SIREN: 222222226")
	if len(got) != 2 {
		t.Fatalf("want 2 facts, got %+v", got)
	}
	seen := map[string]string{}
	for _, f := range got {
		seen[f.Type] = f.Value
	}
	if seen["siret"] != "111111119" || seen["siren"] != "222222226" {
		t.Fatalf("mismatched pairs: %+v", seen)
	}
}

func TestExtract_DeclinesOnAmbiguity(t *testing.T) {
	// Multiple labels + multiple values, none syntactically adjacent → doc-level cannot
	// disambiguate → extract nothing.
	none(t, "Our SIRET, SIREN and EIN are on file.\n\nThe numbers: 111111119, 222222226, 333333330 and 444444440.")
	// One value but two distinct labels in the doc → ambiguous → nothing.
	none(t, "Is it the SIRET or the SIREN?\n\n111111119")
	// A value with no label at all → nothing.
	none(t, "The grand total came to 444444440 over the year.")
}

func TestExtract_UnknownCapsTokenNotALabel(t *testing.T) {
	// A proper noun in caps (surname / city) next to a number must NOT mint a garbage label —
	// only alias-table-known acronyms are trusted. (Regression: id_dubois / id_paris in the corpus.)
	none(t, "DUBOIS 123456789 est le vendeur.")
	none(t, "RCS PARIS: 552081317 en France.") // RCS/PARIS not seeded → nothing, never id_paris
}

func TestExtract_AdjacencyRequiresSeparator(t *testing.T) {
	// Without a separator AND with a competing value (so doc-level cannot rescue), a label does not
	// bind a merely-nearby value — prose precision guard.
	none(t, "Order number 900112233 and amount 555555550 due.")
	// The same with a separator binds the labelled value (and leaves the bare one unbound).
	one(t, "Order number: 900112233 and amount 555555550 due.", "order", "900112233")
}

func TestExtract_ValueGrade(t *testing.T) {
	// Too few digits (an amount) → not identifier-grade → nothing.
	none(t, "Réf: 4500 payée.")
	// 8-digit date-like (YYYYMMDD) → below the 9-digit floor → nothing.
	none(t, "Order number: 20240315 shipped.")
}

func TestExtract_ChecksumTypesNotEmitted(t *testing.T) {
	// A national-number label → labelfact emits NOTHING (recognize owns + validates family A),
	// so an unvalidated value can never pollute the checksum type.
	none(t, "Numéro national: 12345678901 pour la personne.")
	none(t, "TVA: 0123456789 sur la facture.")
	none(t, "IBAN: BE68539007547034 pour le paiement.")
}

func TestExtract_ChecksumLabelBlocksDocLevel(t *testing.T) {
	// A single value whose only label is a checksum-type label must NOT be captured by the
	// doc-level fallback either (family A owns it).
	none(t, "Votre numéro national\n\n12345678901\n")
}
