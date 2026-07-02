package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/config"
	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/scheduler"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// TestDigestHandler_NoLLMCallsOnRequestPath is the core WP20 proof: GET /digest
// composes PRECOMPUTED state and must make ZERO LLM calls on the request path. The
// LLM client points at a server that fails the test if it is ever hit; the digest is
// served from cached/deterministic reads only.
func TestDigestHandler_NoLLMCallsOnRequestPath(t *testing.T) {
	var llmCalls int32
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&llmCalls, 1)
		t.Errorf("LLM must not be called on the /digest request path: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer llmServer.Close()
	llmClient := llm.NewClientWithHTTP(llmServer.URL, 2*time.Second, 0, llmServer.Client())

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// Precompute the surfaces so the handler has state to serve:
	//  - a fresh durable contradictions cache → served stale-while-revalidate, no reconcile;
	//  - no standing decisions → the positions synopsis is a no-op (no regen);
	//  - a benign corpus item → nothing to generate from.
	if perr := db.PutContradictionCache(ctx, "", "[]", 0); perr != nil {
		t.Fatalf("PutContradictionCache: %v", perr)
	}
	if ierr := db.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
		ContentID: "note:1", SourceType: store.SourceTypeNote, Title: "hello", NormalizedText: "world",
		Metadata: map[string]any{}, VersionID: "v1", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); ierr != nil {
		t.Fatalf("InsertKnowledgeItem: %v", ierr)
	}

	cfg := config.DailyBriefConfig{Enabled: true, LookbackHours: 24, MaxItems: 50}
	brief := scheduler.NewDailyBrief(db, llmClient, events.NewBroker(), cfg, zerolog.New(io.Discard))
	if brief == nil {
		t.Fatal("NewDailyBrief returned nil")
	}
	h := NewBriefHandler(brief, zerolog.New(io.Discard))
	h.SetStore(db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/digest", nil)
	h.Digest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if derr := json.Unmarshal(rec.Body.Bytes(), &body); derr != nil {
		t.Fatalf("decode body: %v", derr)
	}
	for _, k := range []string{"synopsis", "positions", "contradictions", "proposed_decisions", "due_tasks", "upcoming"} {
		if _, ok := body[k]; !ok {
			t.Errorf("digest response missing key %q", k)
		}
	}

	// Give any (unexpected) background goroutine a moment to run, then confirm the LLM
	// was never invoked — on the request path or in a spawned refresh.
	time.Sleep(150 * time.Millisecond)
	if n := atomic.LoadInt32(&llmCalls); n != 0 {
		t.Fatalf("expected 0 LLM calls, got %d", n)
	}
}
