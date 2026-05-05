package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/agenda"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

func TestAgendaContextHandler_ResponseShape(t *testing.T) {
	ctx := context.Background()

	// Use an in-memory SQLite store.
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to open test DB: %v", err)
	}
	defer db.Close()

	// Insert a knowledge item with a due date.
	now := time.Now()
	item := &store.KnowledgeItem{
		ContentID:      "test-item-1",
		SourceType:     "note",
		Title:          "Envoyer rapport",
		NormalizedText: "Envoyer le rapport avant la deadline",
		Metadata: map[string]any{
			"extracted_due_dates": []interface{}{"2026-06-30"},
		},
		VersionID: "v1",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.InsertKnowledgeItem(ctx, item); err != nil {
		t.Fatalf("failed to insert item: %v", err)
	}

	ext := agenda.NewExtractor(nil) // no LLM
	handler := NewAgendaHandler(ext, db, zerolog.Nop())

	req := httptest.NewRequest(http.MethodGet, "/agenda/context?range_hours=8760", nil) // 1 year window
	rec := httptest.NewRecorder()
	handler.AgendaContext(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp AgendaContextResponseDTO
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.GeneratedAt == "" {
		t.Error("generated_at should not be empty")
	}

	// The actions list should be a non-nil slice.
	if resp.Actions == nil {
		t.Error("actions should be a non-nil slice")
	}
}
