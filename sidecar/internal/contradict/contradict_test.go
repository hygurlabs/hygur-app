package contradict

import (
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

func TestNormalizeSubject(t *testing.T) {
	cases := map[string]string{
		"Facture 2026-04":        "facture 2026-04",
		"Re: Facture 2026-04":    "facture 2026-04",
		"RE: RE: Facture":        "facture",
		"Fwd: Facture":           "facture",
		"Tr: Facture":            "facture",
		"AW: Facture":            "facture",
		"Re: [42] Facture":       "[42] facture", // list-tags kept (over-stripping risks merging threads)
		"Re[2]: Facture":         "facture",
		"  Facture   2026   04 ": "facture 2026 04",
		"":                       "",
		"   ":                    "",
	}
	for in, want := range cases {
		if got := normalizeSubject(in); got != want {
			t.Errorf("normalizeSubject(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAmountKey(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"1200.00 EUR", "EUR:120000", true},
		{"1000 EUR", "EUR:100000", true},
		{"1200,50 EUR", "EUR:120050", true},
		{"100 USD", "USD:10000", true},
		{"garbage", "", false},
		{"EUR", "", false},
	}
	for _, c := range cases {
		got, ok := amountKey(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("amountKey(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestDateKey(t *testing.T) {
	cases := []struct {
		in      string
		key     string
		hasYear bool
		ok      bool
	}{
		{"25/04/2026", "2026-04-25", true, true},
		{"25-04-2026", "2026-04-25", true, true},
		{"25.04.26", "2026-04-25", true, true},
		{"25 avril 2026", "2026-04-25", true, true},
		{"April 25 2026", "2026-04-25", true, true},
		{"4 mai", "05-04", false, true},
		{"10 mai", "05-10", false, true},
		{"32/04/2026", "", false, false},
		{"25 foobar 2026", "", false, false},
		{"plaintext", "", false, false},
	}
	for _, c := range cases {
		key, hy, ok := dateKey(c.in)
		if ok != c.ok || (ok && (key != c.key || hy != c.hasYear)) {
			t.Errorf("dateKey(%q) = (%q,%v,%v), want (%q,%v,%v)", c.in, key, hy, ok, c.key, c.hasYear, c.ok)
		}
	}
}

// item builds a mail KnowledgeItem with the given extracted_* metadata. meta
// values are []string here; Detect must also tolerate the []any round-trip form
// (covered by TestDetectMetaAnyForm).
func item(id, subject, from string, day int, meta map[string]any) *store.KnowledgeItem {
	if meta == nil {
		meta = map[string]any{}
	}
	meta["mail_from"] = from
	return &store.KnowledgeItem{
		ContentID:  id,
		SourceType: "mail",
		Title:      subject,
		Metadata:   meta,
		CreatedAt:  time.Date(2026, 4, day, 9, 0, 0, 0, time.UTC),
	}
}

// Amounts and due dates are intentionally NOT surfaced by the deterministic
// detector (a thread holds many legitimate values → noise); they're deferred to
// the W6 claim layer, which scopes each value to its (entity, attribute).
func TestDetectAmountsAndDatesNotSurfaced(t *testing.T) {
	items := []*store.KnowledgeItem{
		item("a", "Facture projet X", "alice@acme.com", 1, map[string]any{
			"extracted_amounts":   []string{"1000.00 EUR"},
			"extracted_due_dates": []string{"4 mai"},
		}),
		item("b", "Re: Facture projet X", "bob@acme.com", 2, map[string]any{
			"extracted_amounts":   []string{"1200.00 EUR"},
			"extracted_due_dates": []string{"10 mai"},
		}),
	}
	if got := Detect(items); len(got) != 0 {
		t.Errorf("amounts/dates must not be surfaced deterministically, got %+v", got)
	}
}

func TestDetectNoConflictSameValue(t *testing.T) {
	items := []*store.KnowledgeItem{
		item("a", "Loyer", "x@y.com", 1, map[string]any{"extracted_iban": []string{"BE68 5390 0754 7034"}}),
		item("b", "Re: Loyer", "z@y.com", 2, map[string]any{"extracted_iban": []string{"BE68539007547034"}}), // same, spacing differs
	}
	if got := Detect(items); len(got) != 0 {
		t.Errorf("identical IBANs (spacing aside) must not conflict, got %+v", got)
	}
}

func TestDetectNoConflictSingleSource(t *testing.T) {
	// One email mentioning two values is an explanation, not a cross-source conflict.
	items := []*store.KnowledgeItem{
		item("a", "Loyer", "x@y.com", 1, map[string]any{"extracted_iban": []string{"BE68 5390 0754 7034", "FR7630006000011234567890189"}}),
		item("b", "Re: Loyer", "z@y.com", 2, map[string]any{}),
	}
	if got := Detect(items); len(got) != 0 {
		t.Errorf("single-source divergence must not conflict, got %+v", got)
	}
}

func TestDetectDifferentSubjectsNotClustered(t *testing.T) {
	items := []*store.KnowledgeItem{
		item("a", "Loyer A", "x@y.com", 1, map[string]any{"extracted_iban": []string{"BE68 5390 0754 7034"}}),
		item("b", "Loyer B", "z@y.com", 2, map[string]any{"extracted_iban": []string{"FR7630006000011234567890189"}}),
	}
	if got := Detect(items); len(got) != 0 {
		t.Errorf("different subjects must not cluster, got %+v", got)
	}
}

func TestDetectIBANConflict(t *testing.T) {
	items := []*store.KnowledgeItem{
		item("a", "Paiement loyer", "x@y.com", 1, map[string]any{"extracted_iban": []string{"BE68 5390 0754 7034"}}),
		item("b", "Re: Paiement loyer", "z@y.com", 2, map[string]any{"extracted_iban": []string{"FR7630006000011234567890189"}}),
	}
	got := Detect(items)
	if len(got) != 1 || got[0].Type != "iban" || got[0].Severity != "high" {
		t.Fatalf("want 1 high iban conflict, got %+v", got)
	}
}

func TestDetectMetaAnyForm(t *testing.T) {
	// After a JSON round-trip through the store, []string becomes []any.
	items := []*store.KnowledgeItem{
		item("a", "Loyer", "x@y.com", 1, map[string]any{"extracted_iban": []any{"BE68 5390 0754 7034"}}),
		item("b", "Re: Loyer", "z@y.com", 2, map[string]any{"extracted_iban": []any{"FR7630006000011234567890189"}}),
	}
	if got := Detect(items); len(got) != 1 {
		t.Errorf("want 1 conflict from []any metadata, got %+v", got)
	}
}
