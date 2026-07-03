package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
)

// Phase 3.3 — long-term chat memory. End-to-end-ish test of the new HTTP
// surface. The user's memory file flagged that prior steps shipped components
// in isolation without wiring tests; this exercises the full extract →
// pending → accept loop through the chi router so a regression in any layer
// (handler, store, or routing) trips this test.

func newTestRouter(t *testing.T) (*chi.Mux, *MemoryHandler, *store.DB, *httptest.Server) {
	t.Helper()
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	// Stub LLM endpoint for the extractor. Returns a single fact.
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(llm.ChatResponse{
			Choices: []llm.Choice{{
				Message: &llm.Message{
					Role:    "assistant",
					Content: `[{"type":"preference","content":"Prefers concise replies"}]`,
				},
			}},
		})
	}))
	t.Cleanup(func() { llmSrv.Close() })

	llmClient := llm.NewClientWithHTTP(llmSrv.URL, 5_000_000_000, 0, llmSrv.Client())

	storeTool := tools.NewMemoryStoreTool(db, llmClient, nil)
	searchTool := tools.NewMemorySearchTool(db, llmClient)
	handler := NewMemoryHandler(db, zerolog.Nop())
	handler.SetTools(storeTool, searchTool)

	router := chi.NewRouter()
	router.Post("/memory/extract", handler.Extract)
	router.Get("/memory/pending", handler.Pending)
	router.Post("/memory/{memory_id}/accept", handler.Accept)
	router.Post("/memory/{memory_id}/discard", handler.Discard)
	router.Get("/memory/stats", handler.Stats)
	router.Delete("/memory/extracted", handler.ClearExtracted)
	router.Get("/memory/list", handler.List)
	router.Post("/memory/dedup", handler.Dedup)

	t.Cleanup(func() { db.Close() })
	return router, handler, db, llmSrv
}

// TestMemoryHandler_Dedup exercises the one-time Plan A reconcile end-to-end:
// dry-run reports the plan without mutating; apply removes exactly the
// duplicates + identifier rows and writes a backup; a second apply is a no-op.
func TestMemoryHandler_Dedup(t *testing.T) {
	router, handler, db, _ := newTestRouter(t)
	handler.SetBackupPath(filepath.Join(t.TempDir(), "hygur.db"))

	niss := mkTestNISS("850701123") // fictional, checksum-valid
	acc := time.Now()
	seed := []store.Memory{
		{MemoryID: "n1", Type: store.MemoryFact, Content: "User's name is Denis", Source: store.MemorySourceExtracted, CreatedAt: time.Now()},
		{MemoryID: "n2", Type: store.MemoryFact, Content: "user's name is denis.", Source: store.MemorySourceManual, CreatedAt: time.Now(), AcceptedAt: &acc},
		{MemoryID: "id", Type: store.MemoryFact, Content: "Son numéro national est " + niss, Source: store.MemorySourceExtracted, CreatedAt: time.Now()},
		{MemoryID: "soft", Type: store.MemoryFact, Content: "Travaille avec la Fiduciaire de la Cense", Source: store.MemorySourceExtracted, CreatedAt: time.Now()},
	}
	for i := range seed {
		if err := db.InsertMemory(&seed[i]); err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}

	// Dry-run.
	dry := doDedup(t, router, false)
	if dry.DryRun != true || dry.Deleted != 0 {
		t.Fatalf("dry-run mutated: %+v", dry)
	}
	if dry.DuplicatesRemoved != 1 || dry.IdentifiersRemoved != 1 || dry.KeptSoftFacts != 2 {
		t.Fatalf("unexpected dry-run plan: %+v", dry)
	}
	if dry.TotalBefore != 4 || dry.TotalAfter != 2 {
		t.Fatalf("unexpected dry-run totals: %+v", dry)
	}
	if n := countRows(t, db); n != 4 {
		t.Fatalf("dry-run must not delete; have %d rows", n)
	}

	// Apply.
	applied := doDedup(t, router, true)
	if applied.DryRun != false || applied.Deleted != 2 {
		t.Fatalf("apply did not delete 2: %+v", applied)
	}
	if applied.BackupPath == "" {
		t.Fatal("apply must write a backup path")
	}
	if _, err := os.Stat(applied.BackupPath); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	if n := countRows(t, db); n != 2 {
		t.Fatalf("after apply want 2 rows, got %d", n)
	}

	// Idempotent: a second apply removes nothing.
	again := doDedup(t, router, true)
	if again.Deleted != 0 {
		t.Fatalf("second apply not idempotent: deleted %d", again.Deleted)
	}
}

func doDedup(t *testing.T, router *chi.Mux, apply bool) DedupResponse {
	t.Helper()
	body, _ := json.Marshal(DedupRequest{Apply: apply})
	req := httptest.NewRequest(http.MethodPost, "/memory/dedup", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("dedup status %d: %s", rr.Code, rr.Body.String())
	}
	var out DedupResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode dedup response: %v", err)
	}
	return out
}

// mkTestNISS builds a checksum-valid Belgian national number from a 9-digit
// base (fictional) so the reconcile's identifier detection fires.
func mkTestNISS(base9 string) string {
	b, _ := strconv.ParseInt(base9, 10, 64)
	return base9 + fmt.Sprintf("%02d", 97-(b%97))
}

func countRows(t *testing.T, db *store.DB) int {
	t.Helper()
	all, err := db.ListMemoriesAfter(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return len(all)
}

func TestMemoryHandler_ExtractToAcceptFlow(t *testing.T) {
	router, _, db, _ := newTestRouter(t)

	// 1. Hit /memory/extract with a transcript.
	body, _ := json.Marshal(ExtractRequest{
		SessionID: "session-42",
		Messages: []ExtractMessagePayload{
			{Role: "user", Content: "Please always answer me concisely, I don't have time for fluff. Thanks for the help so far."},
			{Role: "assistant", Content: "Understood, I'll stay concise."},
		},
	})
	req := httptest.NewRequest("POST", "/memory/extract", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("extract status: want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	var extractResp ExtractResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &extractResp); err != nil {
		t.Fatalf("decode extract resp: %v", err)
	}
	if extractResp.Stored != 1 {
		t.Fatalf("want Stored=1, got %d", extractResp.Stored)
	}
	if len(extractResp.Pending) != 1 {
		t.Fatalf("want 1 pending in response, got %d", len(extractResp.Pending))
	}
	if extractResp.Pending[0].Source != string(store.MemorySourceExtracted) {
		t.Errorf("pending source: want extracted, got %q", extractResp.Pending[0].Source)
	}
	if extractResp.Pending[0].AcceptedAt != "" {
		t.Errorf("pending accepted_at should be empty, got %q", extractResp.Pending[0].AcceptedAt)
	}

	// 2. /memory/pending returns the same row.
	req = httptest.NewRequest("GET", "/memory/pending", nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("pending status: %d", rr.Code)
	}
	var pendingResp ListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &pendingResp); err != nil {
		t.Fatalf("decode pending: %v", err)
	}
	if pendingResp.Total != 1 {
		t.Fatalf("pending total: want 1, got %d", pendingResp.Total)
	}
	memID := pendingResp.Memories[0].MemoryID

	// 3. Accept the pending candidate.
	req = httptest.NewRequest("POST", "/memory/"+memID+"/accept", nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("accept status: %d (%s)", rr.Code, rr.Body.String())
	}

	// 4. /memory/pending now returns empty.
	req = httptest.NewRequest("GET", "/memory/pending", nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if err := json.Unmarshal(rr.Body.Bytes(), &pendingResp); err != nil {
		t.Fatalf("decode pending2: %v", err)
	}
	if pendingResp.Total != 0 {
		t.Errorf("expected 0 pending after accept, got %d", pendingResp.Total)
	}

	// 5. The accepted memory should now show up in ListAcceptedMemories.
	accepted, err := db.ListAcceptedMemories(context.Background())
	if err != nil {
		t.Fatalf("list accepted: %v", err)
	}
	if len(accepted) != 1 {
		t.Fatalf("expected 1 accepted memory, got %d", len(accepted))
	}
	if accepted[0].AcceptedAt == nil {
		t.Errorf("accepted_at should be set after accept")
	}
}

func TestMemoryHandler_DiscardRemovesPending(t *testing.T) {
	router, _, db, _ := newTestRouter(t)

	body, _ := json.Marshal(ExtractRequest{
		SessionID: "session-43",
		Messages: []ExtractMessagePayload{
			{Role: "user", Content: "I really really really prefer my replies short and to the point."},
			{Role: "assistant", Content: "Got it."},
		},
	})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest("POST", "/memory/extract", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("extract: %d", rr.Code)
	}
	pending, err := db.ListPendingMemories(context.Background())
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending fixture: %v / %d", err, len(pending))
	}

	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest("POST", "/memory/"+pending[0].MemoryID+"/discard", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("discard: want 204, got %d (%s)", rr.Code, rr.Body.String())
	}

	count, _ := db.CountMemoriesBySource(context.Background(), store.MemorySourceExtracted)
	if count != 0 {
		t.Errorf("want 0 extracted memories after discard, got %d", count)
	}
}

func TestMemoryHandler_ClearExtractedKeepsManual(t *testing.T) {
	router, handler, db, _ := newTestRouter(t)
	// Insert a manual memory directly.
	if _, err := handler.tool.Store("Likes vim", "preference", ""); err != nil {
		t.Fatalf("store manual: %v", err)
	}
	// And one extracted via the route.
	body, _ := json.Marshal(ExtractRequest{
		SessionID: "s",
		Messages: []ExtractMessagePayload{
			{Role: "user", Content: "I really do prefer concise replies, please save this as a long-term preference."},
			{Role: "assistant", Content: "Understood."},
		},
	})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest("POST", "/memory/extract", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("extract: %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest("DELETE", "/memory/extracted", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("clear: %d", rr.Code)
	}
	var clearResp ClearExtractedResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &clearResp); err != nil {
		t.Fatalf("decode clear: %v", err)
	}
	if clearResp.Deleted != 1 {
		t.Errorf("deleted: want 1, got %d", clearResp.Deleted)
	}

	manualCount, _ := db.CountMemoriesBySource(context.Background(), store.MemorySourceManual)
	if manualCount != 1 {
		t.Errorf("manual count after clear: want 1, got %d", manualCount)
	}
}

func TestMemoryHandler_StatsReportsCounts(t *testing.T) {
	router, _, _, _ := newTestRouter(t)

	body, _ := json.Marshal(ExtractRequest{
		SessionID: "s",
		Messages: []ExtractMessagePayload{
			{Role: "user", Content: "I really do prefer concise replies, please save this as a long-term preference."},
			{Role: "assistant", Content: "Understood."},
		},
	})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest("POST", "/memory/extract", bytes.NewReader(body)))

	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest("GET", "/memory/stats", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("stats: %d", rr.Code)
	}
	var stats StatsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if stats.ExtractedCount != 1 {
		t.Errorf("ExtractedCount: want 1, got %d", stats.ExtractedCount)
	}
	if stats.PendingCount != 1 {
		t.Errorf("PendingCount: want 1, got %d", stats.PendingCount)
	}
}

// TestMemoryHandler_ListMarksSourceAndPending verifies the wire shape
// surfaces the new fields the macOS app relies on. Without them, the UI
// can't distinguish manual from extracted memories.
func TestMemoryHandler_ListMarksSourceAndPending(t *testing.T) {
	router, handler, _, _ := newTestRouter(t)
	if _, err := handler.tool.Store("Has a dog", "fact", ""); err != nil {
		t.Fatalf("store: %v", err)
	}
	body, _ := json.Marshal(ExtractRequest{
		SessionID: "s",
		Messages: []ExtractMessagePayload{
			{Role: "user", Content: "I really do prefer concise replies, please save this as a long-term preference."},
			{Role: "assistant", Content: "Understood."},
		},
	})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest("POST", "/memory/extract", bytes.NewReader(body)))

	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest("GET", "/memory/list", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d", rr.Code)
	}
	var listResp ListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listResp.Total != 2 {
		t.Fatalf("expected 2 memories, got %d", listResp.Total)
	}
	var sawManual, sawPending bool
	for _, m := range listResp.Memories {
		if m.Source == string(store.MemorySourceManual) && m.AcceptedAt != "" {
			sawManual = true
		}
		if m.Source == string(store.MemorySourceExtracted) && m.AcceptedAt == "" {
			sawPending = true
		}
	}
	if !sawManual || !sawPending {
		t.Errorf("expected both manual+accepted and extracted+pending in list, got %+v", listResp.Memories)
	}
}

// Sanity check the renderTranscript helper hasn't regressed when passed
// through the handler — the extractor response above only fires if the
// transcript renders non-empty.
func TestExtractRequest_HandlerRejectsEmpty(t *testing.T) {
	router, _, _, _ := newTestRouter(t)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest("POST", "/memory/extract", strings.NewReader(`{"messages": []}`)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("want 400 for empty messages, got %d (%s)", rr.Code, rr.Body.String())
	}
}
