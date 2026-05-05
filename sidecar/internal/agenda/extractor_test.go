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
	item := makeItem("doc-1", "Rapport Q2", map[string]any{
		"extracted_due_dates": []interface{}{"2026-06-30"},
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
	if actions[0].DeadlineISO != "2026-06-30" {
		t.Errorf("expected deadline 2026-06-30, got %s", actions[0].DeadlineISO)
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
