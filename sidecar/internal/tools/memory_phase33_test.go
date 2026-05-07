package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

// Phase 3.3 — long-term chat memory regression tests. These complement the
// existing memory_store_test.go by exercising the source/accepted_at flow,
// the session-level extractor, and SearchAccepted (cosine ranking with
// fallback to recency when embeddings aren't available).

func TestPersistExtracted_LandsAsPending(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close()

	tool := &MemoryStoreTool{store: db}
	in := []ExtractedMemory{{Type: "fact", Content: "Lives in Paris"}}
	stored, err := tool.PersistExtracted(in, "session-A")
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if stored != 1 {
		t.Fatalf("want 1 stored, got %d", stored)
	}
	pending, err := db.ListPendingMemories(context.Background())
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("want 1 pending, got %d", len(pending))
	}
	if pending[0].Source != store.MemorySourceExtracted {
		t.Errorf("want source=extracted, got %q", pending[0].Source)
	}
	if pending[0].AcceptedAt != nil {
		t.Errorf("want accepted_at=nil for pending, got %v", pending[0].AcceptedAt)
	}
	if pending[0].SessionID != "session-A" {
		t.Errorf("want session_id=session-A, got %q", pending[0].SessionID)
	}
}

func TestStore_AutoAcceptsManual(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close()

	tool := &MemoryStoreTool{store: db}
	id, err := tool.Store("My favourite editor is Helix", "preference", "manual-1")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	mem, err := db.GetMemory(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if mem.Source != store.MemorySourceManual {
		t.Errorf("want source=manual, got %q", mem.Source)
	}
	if mem.AcceptedAt == nil {
		t.Errorf("want accepted_at set for manual memory, got nil")
	}
}

func TestAcceptMemory_TransitionsPendingToAccepted(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close()

	tool := &MemoryStoreTool{store: db}
	if _, err := tool.PersistExtracted([]ExtractedMemory{{Type: "fact", Content: "Drives a Tesla"}}, "session-X"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	pending, err := db.ListPendingMemories(context.Background())
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending fixture broken: %v / %d", err, len(pending))
	}
	if err := db.AcceptMemory(context.Background(), pending[0].MemoryID, time.Now()); err != nil {
		t.Fatalf("accept: %v", err)
	}
	stillPending, _ := db.ListPendingMemories(context.Background())
	if len(stillPending) != 0 {
		t.Errorf("want 0 pending after accept, got %d", len(stillPending))
	}
	accepted, err := db.ListAcceptedMemories(context.Background())
	if err != nil {
		t.Fatalf("list accepted: %v", err)
	}
	if len(accepted) != 1 {
		t.Fatalf("want 1 accepted, got %d", len(accepted))
	}
}

func TestDeleteMemoriesBySource_DropsExtractedKeepsManual(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close()

	tool := &MemoryStoreTool{store: db}
	if _, err := tool.Store("Likes Vim", "preference", "manual-1"); err != nil {
		t.Fatalf("store manual: %v", err)
	}
	if _, err := tool.PersistExtracted([]ExtractedMemory{{Type: "fact", Content: "Drives a Tesla"}}, "session-X"); err != nil {
		t.Fatalf("persist extracted: %v", err)
	}

	deleted, err := db.DeleteMemoriesBySource(context.Background(), store.MemorySourceExtracted)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted != 1 {
		t.Errorf("want 1 deleted, got %d", deleted)
	}
	manualCount, _ := db.CountMemoriesBySource(context.Background(), store.MemorySourceManual)
	extractedCount, _ := db.CountMemoriesBySource(context.Background(), store.MemorySourceExtracted)
	if manualCount != 1 {
		t.Errorf("manual count after wipe: want 1, got %d", manualCount)
	}
	if extractedCount != 0 {
		t.Errorf("extracted count after wipe: want 0, got %d", extractedCount)
	}
}

// TestSearchAccepted_SkipsPending verifies that pending candidates do NOT
// leak into chat injection. This is the load-bearing privacy guarantee of
// Phase 3.3 — the user must accept a fact before the LLM ever sees it.
func TestSearchAccepted_SkipsPending(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close()

	store_ := &MemoryStoreTool{store: db}
	if _, err := store_.PersistExtracted([]ExtractedMemory{{Type: "fact", Content: "Lives in Paris"}}, "session-X"); err != nil {
		t.Fatalf("persist: %v", err)
	}

	search := &MemorySearchTool{store: db, llm: nil}
	got, err := search.SearchAccepted(context.Background(), "where does the user live?", 5, 500)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 results (only pending memory exists), got %d: %+v", len(got), got)
	}
}

// TestSearchAccepted_ReturnsAcceptedFallback exercises the recency fallback
// that runs when no LLM client is wired. We seed a single accepted memory
// and confirm it surfaces; this is the "cosine unavailable" code path.
func TestSearchAccepted_ReturnsAcceptedFallback(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close()

	tool := &MemoryStoreTool{store: db}
	id, err := tool.Store("Prefers concise answers", "preference", "manual-1")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	search := &MemorySearchTool{store: db, llm: nil}
	got, err := search.SearchAccepted(context.Background(), "anything", 5, 500)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].MemoryID != id {
		t.Fatalf("want 1 accepted memory, got %+v", got)
	}
}

// TestExtractMemoriesFromSession_ParsesMockedLLM stubs the chat completion
// endpoint and confirms the session-level extractor parses the JSON array
// the LLM returns. Mirrors the per-turn extractor's mocked test for parity.
func TestExtractMemoriesFromSession_ParsesMockedLLM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		resp := llm.ChatResponse{
			Choices: []llm.Choice{{
				Message: &llm.Message{
					Role:    "assistant",
					Content: `[{"type":"fact","content":"Lives in Paris"},{"type":"preference","content":"Prefers French replies"}]`,
				},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := llm.NewClientWithHTTP(srv.URL, 5*time.Second, 0, srv.Client())
	tool := &MemoryStoreTool{llm: client}

	got, err := tool.ExtractMemoriesFromSession(context.Background(), []TranscriptMessage{
		{Role: "user", Content: "I live in Paris and would prefer all replies in French please."},
		{Role: "assistant", Content: "Bien noté, je continuerai en français."},
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 memories, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Content, "Paris") {
		t.Errorf("first memory should mention Paris: %+v", got[0])
	}
}

// TestExtractMemoriesFromSession_ShortTranscriptYieldsNothing verifies the
// pre-filter that drops trivially short conversations before calling the LLM.
func TestExtractMemoriesFromSession_ShortTranscriptYieldsNothing(t *testing.T) {
	tool := &MemoryStoreTool{llm: &llm.Client{}}
	got, err := tool.ExtractMemoriesFromSession(context.Background(), []TranscriptMessage{
		{Role: "user", Content: "merci"},
		{Role: "assistant", Content: "de rien"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("short transcript should yield nil, got %+v", got)
	}
}

// TestRenderTranscript_FiltersSystemAndEmpty checks the helper drops
// non-user/assistant messages and empty content so they don't waste LLM tokens.
func TestRenderTranscript_FiltersSystemAndEmpty(t *testing.T) {
	got := renderTranscript([]TranscriptMessage{
		{Role: "system", Content: "ignored"},
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "  "},
		{Role: "tool", Content: "tool result"},
		{Role: "assistant", Content: "Hello there"},
	})
	if !strings.Contains(got, "User: Hi") {
		t.Errorf("missing user line: %q", got)
	}
	if !strings.Contains(got, "Assistant: Hello there") {
		t.Errorf("missing assistant line: %q", got)
	}
	if strings.Contains(got, "system") || strings.Contains(got, "tool result") {
		t.Errorf("non-conversational content leaked: %q", got)
	}
}
