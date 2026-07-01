package agenda

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

func makeItem(id, title string, metadata map[string]any) store.KnowledgeItem {
	return store.KnowledgeItem{
		ContentID:      id,
		Title:          title,
		NormalizedText: "Some text about " + title,
		Metadata:       metadata,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
}

func TestExtractsActionsFromExtractedDueDates(t *testing.T) {
	// A future date so the today-relative deadline filter keeps it (not date-flaky).
	due := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	item := makeItem("doc-1", "Rapport Q2", map[string]any{
		"extracted_due_dates": []interface{}{due},
		"extracted_topics":    []interface{}{"finance", "reporting"},
	})

	ext := NewExtractor(nil) // no LLM needed for templated path
	actions, err := ext.ExtractActions(context.Background(), []store.KnowledgeItem{item})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(actions) == 0 {
		t.Fatal("expected at least 1 action, got 0")
	}
	if actions[0].SourceID != "doc-1" {
		t.Errorf("expected source_id=doc-1, got %s", actions[0].SourceID)
	}
	if actions[0].DeadlineISO != due {
		t.Errorf("expected deadline %s, got %s", due, actions[0].DeadlineISO)
	}
}

func TestSkipsMarketing(t *testing.T) {
	item := makeItem("promo-1", "Grande solde du mois", map[string]any{
		"extracted_due_dates": []interface{}{"2026-06-01"},
		"extracted_topics":    []interface{}{"newsletter", "promo"},
	})

	ext := NewExtractor(nil)
	actions, err := ext.ExtractActions(context.Background(), []store.KnowledgeItem{item})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(actions) != 0 {
		t.Errorf("expected 0 actions for marketing item, got %d", len(actions))
	}
}

func TestLLMFallbackWhenNoDueDates(t *testing.T) {
	// Item with no extracted_due_dates — should go to LLM fallback.
	// Without a real LLM, the call will fail and fail-soft returns 0 actions.
	item := makeItem("task-1", "Réviser contrat fournisseur", map[string]any{
		"extracted_topics": []interface{}{"legal"},
	})

	// No LLM configured — fail-soft path should not panic and return empty.
	ext := NewExtractor(nil)
	actions, err := ext.ExtractActions(context.Background(), []store.KnowledgeItem{item})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With nil LLM, extractViaLLM returns nil without error (guard clause).
	_ = actions // 0 actions is valid when LLM not available
}

func TestSkipsPastDeadlines(t *testing.T) {
	// Today the deadline 2024-01-01 has already passed — even though the item
	// was just (re)indexed and so shows up in ListRecentItems, the action
	// must not surface as "upcoming".
	pastItem := makeItem("doc-old", "Vieille facture", map[string]any{
		"extracted_due_dates": []interface{}{"2024-01-01"},
	})
	futureItem := makeItem("doc-future", "Bilan annuel", map[string]any{
		"extracted_due_dates": []interface{}{"2099-12-31"},
	})

	ext := NewExtractor(nil)
	actions, err := ext.ExtractActions(context.Background(), []store.KnowledgeItem{pastItem, futureItem})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("expected 1 action (future only), got %d: %+v", len(actions), actions)
	}
	if actions[0].SourceID != "doc-future" {
		t.Errorf("expected future item to survive, got source_id=%s", actions[0].SourceID)
	}
}

// Regression — TVA quarterly declarations from April / July 2025 surfaced
// as "upcoming focus" actions when running in May 2026 because tier-1 stores
// raw regex captures ("30/04/2025", "31 juillet 2025") and the past-deadline
// filter does a lex string compare against today's ISO date. Lex compare on
// "30/04/2025" vs "2026-05-07" returns ">=" because '3' > '2'. Normalising
// to ISO before the filter is the fix.
func TestNormalisesNonISODueDatesBeforeFiltering(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"DD/MM/YYYY French", "30/04/2025"},
		{"DD-MM-YYYY French", "30-04-2025"},
		{"FR textual lowercase", "31 juillet 2025"},
		{"FR textual with accents", "25 février 2025"},
		{"EN textual full", "31 July 2025"},
		{"EN textual abbreviated", "31 Jul 2025"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := makeItem("tva-"+tc.name, "Déclaration TVA", map[string]any{
				"extracted_due_dates": []interface{}{tc.raw},
			})
			ext := NewExtractor(nil)
			actions, err := ext.ExtractActions(context.Background(), []store.KnowledgeItem{item})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(actions) != 0 {
				t.Errorf("expected 0 actions for past-deadline %q, got %d: %+v", tc.raw, len(actions), actions)
			}
		})
	}
}

func TestNormaliseToISO(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		// Already-ISO short-circuit.
		{"2026-04-30", "2026-04-30", true},
		{" 2026-4-5 ", "2026-04-05", true},
		// French numeric.
		{"30/04/2026", "2026-04-30", true},
		{"5-1-2026", "2026-01-05", true},
		{"5.1.2026", "2026-01-05", true},
		// French textual.
		{"30 avril 2026", "2026-04-30", true},
		{"25 février 2026", "2026-02-25", true},
		{"1 août 2026", "2026-08-01", true},
		// English textual (full + abbreviated).
		{"30 April 2026", "2026-04-30", true},
		{"30 Apr 2026", "2026-04-30", true},
		{"30 Sept 2026", "2026-09-30", true},
		// Garbage — must be rejected, not silently coerced.
		{"Q1 2026", "", false},
		{"avril 2026", "", false},
		{"30/13/2026", "", false},
		{"31/02/2026", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, ok := normalizeToISO(tc.raw)
			if ok != tc.ok || got != tc.want {
				t.Errorf("normalizeToISO(%q) = (%q, %v), want (%q, %v)", tc.raw, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestFailSoftWhenLLMErrors(t *testing.T) {
	// Item with a templated due date plus an item without one.
	// If LLM fails, we should still get the templated action and no panic.
	templatedItem := makeItem("doc-2", "Bilan annuel", map[string]any{
		"extracted_due_dates": []interface{}{"2026-12-31"},
	})
	noDateItem := makeItem("doc-3", "Réunion sans date", map[string]any{})

	// With nil LLM, extractViaLLM is a no-op — simulating fail-soft.
	ext := NewExtractor(nil)
	actions, err := ext.ExtractActions(context.Background(), []store.KnowledgeItem{templatedItem, noDateItem})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(actions) == 0 {
		t.Fatal("expected templated action even when LLM not available")
	}
	if actions[0].DeadlineISO != "2026-12-31" {
		t.Errorf("expected 2026-12-31, got %s", actions[0].DeadlineISO)
	}
}

func TestExtractJSONArray(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", `[{"what":"x"}]`, `[{"what":"x"}]`},
		{"think_block", "<think>\nFirst I reason...\n</think>\n[{\"what\":\"x\"}]", `[{"what":"x"}]`},
		{"think_with_prose", "Voici le JSON:\n[{\"what\":\"y\"}]\nVoilà.", `[{"what":"y"}]`},
		{"unclosed_think_no_json", "<think>\nFirst, I need to extract actions with deadlines", ""},
		{"empty_array", "<think>nothing</think>[]", `[]`},
		{"no_array", "no deadlines found", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractJSONArray(c.in); got != c.want {
				t.Errorf("extractJSONArray(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
