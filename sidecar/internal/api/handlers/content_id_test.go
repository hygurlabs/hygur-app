package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// TestKnowledgeGet_DecodesEscapedContentID guards the regression where a mail
// content_id containing '@' (sent percent-encoded as %40 by the WebUI) 404'd
// because chi returns the raw, still-escaped path segment — leaving the item's
// tags/project panel blank in the Library. Exercised through a real chi router
// so the URL decoding path is what's under test.
func TestKnowledgeGet_DecodesEscapedContentID(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()

	const cid = "imap:abc123@mail.example.com"
	now := time.Now()
	if err := db.InsertKnowledgeItem(context.Background(), &store.KnowledgeItem{
		ContentID:      cid,
		SourceType:     "mail",
		Title:          "Déclaration",
		NormalizedText: "corps",
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("InsertKnowledgeItem: %v", err)
	}

	h := NewKnowledgeHandler(db, nil, nil, zerolog.Nop())
	router := chi.NewRouter()
	router.Get("/knowledge/{content_id}", h.Get)

	// What the WebUI's cidPath() produces: encodeURIComponent then restore ':'.
	req := httptest.NewRequest(http.MethodGet, "/knowledge/imap:abc123%40mail.example.com", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (escaped @ should decode); body=%s", w.Code, w.Body.String())
	}
	var resp KnowledgeItemResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ContentID != cid {
		t.Errorf("content_id = %q, want %q", resp.ContentID, cid)
	}
}
