package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/api/handlers"
	"github.com/hygur/sidecar/internal/config"
	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// TestReconcileEndpoint_E2E drives POST /knowledge/reconcile through the full HTTP
// stack and asserts the user-facing guarantee: a mail no longer present on the
// server disappears from the KB (and thus from every read surface — briefs,
// follow-up, chat — which all read knowledge_items), while present mail is kept,
// the destructive path is gated on a complete enumeration, an empty present-set is
// refused, and a reappearing mail is restored.
func TestReconcileEndpoint_E2E(t *testing.T) {
	logger := zerolog.Nop()
	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	// A non-zero request timeout: with the zero value, middleware.Timeout(0) yields
	// an already-expired deadline and every store query fails instantly.
	cfg := &config.Config{}
	cfg.Server.ReadTimeout = 30 * time.Second
	server := NewServer(cfg, logger, token)

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// Store-only ingestor (no embeddings/LLM): restore re-ingests FTS-only, no network.
	ingestor := ingest.NewIngestorWithStore(db)
	searcher := retrieval.NewHybridSearcher(db, nil)
	server.SetKnowledgeHandler(handlers.NewKnowledgeHandler(db, ingestor, searcher, logger))
	router := server.Router()

	insert := func(id, ref string) {
		now := time.Now().UTC()
		if err := db.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
			ContentID: id, SourceType: "mail", Title: "subj " + id,
			NormalizedText: "body " + id,
			Metadata:       map[string]any{"source_ref": ref, "provider": "proton"},
			VersionID:      "v1", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	post := func(t *testing.T, payload map[string]any) handlers.ReconcileResponse {
		t.Helper()
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/knowledge/reconcile", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Hygur-Token", token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("reconcile HTTP %d: %s", rec.Code, rec.Body.String())
		}
		var out handlers.ReconcileResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode response: %v (%s)", err, rec.Body.String())
		}
		return out
	}
	exists := func(id string) bool {
		it, err := db.GetKnowledgeItem(ctx, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		return it != nil
	}

	insert("uuid-spam", "proton:spam@x.com")
	insert("uuid-keep", "proton:keep@x.com")

	// Gate 1 — an incomplete enumeration is a no-op (never prunes on partial data).
	if out := post(t, map[string]any{"provider": "proton", "seen_refs": []string{}, "complete": false}); out.Status != "skipped_incomplete" {
		t.Errorf("incomplete should skip, got %q", out.Status)
	}
	if !exists("uuid-spam") || !exists("uuid-keep") {
		t.Fatal("incomplete pass must not delete anything")
	}

	// Gate 2 — an empty present-set while the KB holds items is refused.
	if out := post(t, map[string]any{"provider": "proton", "seen_refs": []string{}, "complete": true}); out.Status != "refused_empty" {
		t.Errorf("empty present-set should be refused, got %q", out.Status)
	}
	if !exists("uuid-spam") || !exists("uuid-keep") {
		t.Fatal("refused pass must not delete anything")
	}

	// Recycle the absent (deleted) spam; keep the present mail. grace=1 → purge path
	// is reachable but here the item is absent, so it is recycled (not purged) first.
	out := post(t, map[string]any{
		"provider": "proton", "seen_refs": []string{"proton:keep@x.com"},
		"complete": true, "grace_misses": 3,
	})
	if out.Status != "ok" || out.Recycled != 1 {
		t.Fatalf("expected ok recycled=1, got %+v", out)
	}
	if exists("uuid-spam") {
		t.Error("deleted spam must be gone from the KB (hidden from briefs/follow-up/chat)")
	}
	if !exists("uuid-keep") {
		t.Error("present mail must be kept")
	}

	// Reappearance → restore (re-ingested under a fresh content_id; source_ref back).
	out = post(t, map[string]any{
		"provider": "proton", "seen_refs": []string{"proton:keep@x.com", "proton:spam@x.com"},
		"complete": true, "grace_misses": 3,
	})
	if out.Restored != 1 {
		t.Fatalf("expected restored=1 on reappearance, got %+v", out)
	}
	restored, err := db.GetKnowledgeItemBySourceRef(ctx, "proton:spam@x.com")
	if err != nil {
		t.Fatalf("get restored: %v", err)
	}
	if restored == nil {
		t.Error("reappeared mail must be restored to the KB")
	}
}
