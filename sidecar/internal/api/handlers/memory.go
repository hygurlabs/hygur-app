package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
)

// MemoryHandler handles memory-related API endpoints.
type MemoryHandler struct {
	store    *store.DB
	logger   zerolog.Logger
	tool     *tools.MemoryStoreTool
	toolSrch *tools.MemorySearchTool
	// dbPath locates the live DB so Dedup can write its row backup beside it
	// (in a "backups" folder). Empty disables backup-to-disk (rows are still
	// returned in the response).
	dbPath string
}

// NewMemoryHandler creates a new MemoryHandler.
func NewMemoryHandler(store *store.DB, logger zerolog.Logger) *MemoryHandler {
	return &MemoryHandler{
		store:  store,
		logger: logger.With().Str("handler", "memory").Logger(),
	}
}

// SetTools sets the memory tools for the handler.
func (h *MemoryHandler) SetTools(storeTool *tools.MemoryStoreTool, searchTool *tools.MemorySearchTool) {
	h.tool = storeTool
	h.toolSrch = searchTool
}

// SetBackupPath tells the handler where the live DB lives so Dedup can write
// its pre-apply row backup to "<dbDir>/backups".
func (h *MemoryHandler) SetBackupPath(dbPath string) {
	h.dbPath = dbPath
}

// StoreRequest represents the request body for POST /memory/store.
type StoreRequest struct {
	MemoryType string `json:"type"`
	Content    string `json:"content"`
	ContextID  string `json:"context_id,omitempty"`
	ExpiresIn  int    `json:"expires_in,omitempty"` // minutes, 0 = never expire
}

// StoreResponse represents the response shape for endpoints that return a
// memory. Phase 3.3 adds the source/accepted_at/session_id fields so the
// macOS app can distinguish manual vs extracted rows and surface pending
// candidates for review without inferring state from heuristics.
type StoreResponse struct {
	MemoryID   string `json:"memory_id"`
	Type       string `json:"type"`
	Content    string `json:"content"`
	CreatedAt  string `json:"created_at"`
	Source     string `json:"source,omitempty"`      // "manual" | "extracted"
	AcceptedAt string `json:"accepted_at,omitempty"` // RFC3339; "" = pending
	SessionID  string `json:"session_id,omitempty"`
}

// memoryToResponse converts a *store.Memory to its wire shape, ensuring the
// new fields are always populated.
func memoryToResponse(m *store.Memory) StoreResponse {
	source := string(m.Source)
	if source == "" {
		source = string(store.MemorySourceManual)
	}
	resp := StoreResponse{
		MemoryID:  m.MemoryID,
		Type:      string(m.Type),
		Content:   m.Content,
		CreatedAt: m.CreatedAt.Format(time.RFC3339),
		Source:    source,
		SessionID: m.SessionID,
	}
	if m.AcceptedAt != nil {
		resp.AcceptedAt = m.AcceptedAt.Format(time.RFC3339)
	}
	return resp
}

// Store handles POST /memory/store - store a new memory.
func (h *MemoryHandler) Store(w http.ResponseWriter, r *http.Request) {
	var req StoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMemoryError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	if req.Content == "" {
		writeMemoryError(w, http.StatusBadRequest, "VALIDATION_ERROR", "content is required")
		return
	}

	if req.MemoryType == "" {
		req.MemoryType = string(store.MemoryFact)
	}

	// Validate memory type
	switch req.MemoryType {
	case "fact", "action", "preference":
		// OK
	default:
		writeMemoryError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid memory type")
		return
	}

	// Use the store tool to save the memory
	memoryID, err := h.tool.Store(req.Content, req.MemoryType, req.ContextID)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to store memory")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to store memory")
		return
	}

	now := time.Now().Format(time.RFC3339)
	resp := StoreResponse{
		MemoryID:   memoryID,
		Type:       req.MemoryType,
		Content:    req.Content,
		CreatedAt:  now,
		Source:     string(store.MemorySourceManual),
		AcceptedAt: now, // manual memories are auto-accepted
	}

	writeMemoryJSON(w, http.StatusCreated, resp)
}

// MemorySearchRequest represents the request body for GET /memory/search.
type MemorySearchRequest struct {
	Query      string  `json:"query,omitempty"`
	MaxResults int     `json:"max_results,omitempty"`
	MinScore   float64 `json:"min_score,omitempty"`
}

// MemorySearchResult represents a single memory search result.
type MemorySearchResult struct {
	MemoryID  string  `json:"memory_id"`
	Type      string  `json:"type"`
	Content   string  `json:"content"`
	Score     float64 `json:"score"`
	ContextID string  `json:"context_id,omitempty"`
	CreatedAt string  `json:"created_at"`
}

// MemorySearchResponse wraps the list of memory search results.
type MemorySearchResponse struct {
	Memories []MemorySearchResult `json:"memories"`
	Total    int                  `json:"total"`
}

// MemorySearch handles GET /memory/search - search for memories by query.
func (h *MemoryHandler) MemorySearch(w http.ResponseWriter, r *http.Request) {
	// Parse query from URL parameters
	query := r.URL.Query().Get("q")
	if query == "" {
		writeMemoryError(w, http.StatusBadRequest, "BAD_REQUEST", "q parameter is required")
		return
	}

	results, err := h.toolSrch.Search(query, 10, 0)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to search memories")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to search memories")
		return
	}

	// Convert to response format
	var searchResults []MemorySearchResult
	for _, r := range results {
		searchResults = append(searchResults, MemorySearchResult{
			MemoryID:  r.MemoryID,
			Type:      string(r.Type),
			Content:   r.Content,
			Score:     r.Score,
			ContextID: r.ContextID,
			CreatedAt: r.CreatedAt.Format(time.RFC3339),
		})
	}

	writeMemoryJSON(w, http.StatusOK, MemorySearchResponse{
		Memories: searchResults,
		Total:    len(searchResults),
	})
}

// SyncRequest represents the request body for GET /memory/sync.
type SyncRequest struct {
	LastSync string `json:"last_sync"` // ISO 8601 timestamp
}

// SyncResponse represents the response for GET /memory/sync.
type SyncResponse struct {
	Changes []StoreResponse `json:"changes"`
}

// Sync handles GET /memory/sync - sync new/updated memories since last sync.
func (h *MemoryHandler) Sync(w http.ResponseWriter, r *http.Request) {
	// Parse last sync time
	var lastSync time.Time
	if ts := r.URL.Query().Get("last_sync"); ts != "" {
		// Try to parse as RFC3339
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			lastSync = t
		}
	}

	// Get all memories created after lastSync
	memories, err := h.store.ListMemoriesAfter(r.Context(), lastSync)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list memories after sync")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list memories")
		return
	}

	var changes []StoreResponse
	for _, m := range memories {
		changes = append(changes, memoryToResponse(m))
	}

	writeMemoryJSON(w, http.StatusOK, SyncResponse{Changes: changes})
}

// ListResponse wraps the memory list. The frontend uses this to power the
// MemoriesView; results are ordered most-recent-first.
type ListResponse struct {
	Memories []StoreResponse `json:"memories"`
	Total    int             `json:"total"`
}

// List handles GET /memory/list — returns every stored memory (manual +
// extracted, accepted or pending). The macOS app filters client-side using
// the source/accepted_at fields. Use the existing /memory/sync endpoint when
// you only want recent changes.
func (h *MemoryHandler) List(w http.ResponseWriter, r *http.Request) {
	memories, err := h.store.ListMemoriesAfter(r.Context(), time.Time{})
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list memories")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list memories")
		return
	}
	out := make([]StoreResponse, 0, len(memories))
	for _, m := range memories {
		out = append(out, memoryToResponse(m))
	}
	writeMemoryJSON(w, http.StatusOK, ListResponse{Memories: out, Total: len(out)})
}

// Pending handles GET /memory/pending — returns extracted memories waiting on
// user review. Drives the "Pending review" section in MemoriesView.
func (h *MemoryHandler) Pending(w http.ResponseWriter, r *http.Request) {
	memories, err := h.store.ListPendingMemories(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list pending memories")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list pending memories")
		return
	}
	out := make([]StoreResponse, 0, len(memories))
	for _, m := range memories {
		out = append(out, memoryToResponse(m))
	}
	writeMemoryJSON(w, http.StatusOK, ListResponse{Memories: out, Total: len(out)})
}

// Accept handles POST /memory/{memory_id}/accept — flips accepted_at to now.
// After acceptance the memory becomes eligible for cosine-injection into
// future system prompts.
func (h *MemoryHandler) Accept(w http.ResponseWriter, r *http.Request) {
	memoryID := chi.URLParam(r, "memory_id")
	if memoryID == "" {
		writeMemoryError(w, http.StatusBadRequest, "BAD_REQUEST", "memory_id is required")
		return
	}
	if err := h.store.AcceptMemory(r.Context(), memoryID, time.Now()); err != nil {
		h.logger.Error().Err(err).Str("memory_id", memoryID).Msg("failed to accept memory")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to accept memory")
		return
	}
	mem, err := h.store.GetMemory(r.Context(), memoryID)
	if err != nil {
		// Memory was just updated successfully but we can't fetch it back —
		// surface a generic 200 so the client knows the accept happened.
		writeMemoryJSON(w, http.StatusOK, map[string]string{"memory_id": memoryID})
		return
	}
	writeMemoryJSON(w, http.StatusOK, memoryToResponse(mem))
}

// Discard handles POST /memory/{memory_id}/discard — deletes the candidate
// outright. Discard and Delete are wire-distinct (discard is reserved for
// pending candidates) so the UI can offer different copy and the server can
// log the user's intent, but the underlying SQL is the same DELETE.
func (h *MemoryHandler) Discard(w http.ResponseWriter, r *http.Request) {
	memoryID := chi.URLParam(r, "memory_id")
	if memoryID == "" {
		writeMemoryError(w, http.StatusBadRequest, "BAD_REQUEST", "memory_id is required")
		return
	}
	if err := h.store.DeleteMemory(r.Context(), memoryID); err != nil {
		h.logger.Error().Err(err).Str("memory_id", memoryID).Msg("failed to discard memory")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to discard memory")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ExtractRequest is the body for POST /memory/extract. The macOS app sends
// the conversation transcript because the sidecar's session.Store is
// in-memory and may not hold the full transcript by the time the user
// archives a chat.
type ExtractRequest struct {
	SessionID string                  `json:"session_id,omitempty"`
	Messages  []ExtractMessagePayload `json:"messages"`
}

// ExtractMessagePayload mirrors tools.TranscriptMessage on the wire.
type ExtractMessagePayload struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ExtractResponse summarises the outcome of an /memory/extract call.
type ExtractResponse struct {
	Extracted int             `json:"extracted"`
	Stored    int             `json:"stored"`
	Pending   []StoreResponse `json:"pending"`
}

// Extract handles POST /memory/extract — runs the LLM extractor over a
// transcript and persists candidates as pending. Returns the freshly stored
// candidates so the client can update its UI without round-tripping to
// /memory/pending right after.
func (h *MemoryHandler) Extract(w http.ResponseWriter, r *http.Request) {
	if h.tool == nil {
		writeMemoryError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "memory store tool not configured")
		return
	}
	var req ExtractRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMemoryError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}
	if len(req.Messages) == 0 {
		writeMemoryError(w, http.StatusBadRequest, "VALIDATION_ERROR", "messages required")
		return
	}

	transcript := make([]tools.TranscriptMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		transcript = append(transcript, tools.TranscriptMessage{Role: m.Role, Content: m.Content})
	}

	extracted, err := h.tool.ExtractMemoriesFromSession(r.Context(), transcript)
	if err != nil {
		// Embedding endpoint flakiness or LLM unavailability should yield an
		// empty extraction rather than 500; the caller often retries from a
		// background task. We still log so misconfiguration is visible.
		if err == llm.ErrEmbeddingModelUnavailable {
			writeMemoryJSON(w, http.StatusOK, ExtractResponse{})
			return
		}
		h.logger.Warn().Err(err).Msg("session memory extraction failed")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to extract memories")
		return
	}
	if len(extracted) == 0 {
		writeMemoryJSON(w, http.StatusOK, ExtractResponse{})
		return
	}
	stored, persistErr := h.tool.PersistExtracted(extracted, req.SessionID)
	if persistErr != nil {
		h.logger.Warn().Err(persistErr).Msg("partial persistence of extracted memories")
	}

	pending, err := h.store.ListPendingMemories(r.Context())
	if err != nil {
		h.logger.Warn().Err(err).Msg("failed to fetch pending memories after extract")
	}
	out := make([]StoreResponse, 0, len(pending))
	for _, m := range pending {
		out = append(out, memoryToResponse(m))
	}

	writeMemoryJSON(w, http.StatusOK, ExtractResponse{
		Extracted: len(extracted),
		Stored:    stored,
		Pending:   out,
	})
}

// StatsResponse exposes counts the Settings UI uses to surface memory state.
type StatsResponse struct {
	ManualCount    int `json:"manual_count"`
	ExtractedCount int `json:"extracted_count"`
	PendingCount   int `json:"pending_count"`
}

// Stats handles GET /memory/stats — counts grouped by source/state.
func (h *MemoryHandler) Stats(w http.ResponseWriter, r *http.Request) {
	manualCount, err := h.store.CountMemoriesBySource(r.Context(), store.MemorySourceManual)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to count manual memories")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to count memories")
		return
	}
	extractedCount, err := h.store.CountMemoriesBySource(r.Context(), store.MemorySourceExtracted)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to count extracted memories")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to count memories")
		return
	}
	pending, err := h.store.ListPendingMemories(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list pending memories")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to count memories")
		return
	}
	writeMemoryJSON(w, http.StatusOK, StatsResponse{
		ManualCount:    manualCount,
		ExtractedCount: extractedCount,
		PendingCount:   len(pending),
	})
}

// ClearExtractedResponse reports how many rows the wipe removed.
type ClearExtractedResponse struct {
	Deleted int `json:"deleted"`
}

// ClearExtracted handles DELETE /memory/extracted — wipes every memory with
// source='extracted', leaving manual entries untouched. Settings UI uses
// this behind a confirmation dialog.
func (h *MemoryHandler) ClearExtracted(w http.ResponseWriter, r *http.Request) {
	deleted, err := h.store.DeleteMemoriesBySource(r.Context(), store.MemorySourceExtracted)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to clear extracted memories")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to clear extracted memories")
		return
	}
	h.logger.Info().Int("deleted", deleted).Msg("cleared extracted memories")
	writeMemoryJSON(w, http.StatusOK, ClearExtractedResponse{Deleted: deleted})
}

// Delete handles DELETE /memory/{memory_id}.
func (h *MemoryHandler) Delete(w http.ResponseWriter, r *http.Request) {
	memoryID := chi.URLParam(r, "memory_id")
	if memoryID == "" {
		writeMemoryError(w, http.StatusBadRequest, "BAD_REQUEST", "memory_id is required")
		return
	}
	if err := h.store.DeleteMemory(r.Context(), memoryID); err != nil {
		h.logger.Error().Err(err).Str("memory_id", memoryID).Msg("failed to delete memory")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete memory")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DedupRequest is the body for POST /memory/dedup. apply=false (default) is a
// dry-run: it reports what WOULD be removed without touching data. apply=true
// performs the conservative reconcile (after writing a backup).
type DedupRequest struct {
	Apply bool `json:"apply"`
}

// DedupSample is a PII-safe, digit-masked preview of one row the plan targets.
type DedupSample struct {
	Reason string `json:"reason"` // "duplicate" | "identifier"
	Sample string `json:"sample"` // redacted (digits masked, truncated)
}

// DedupResponse summarises a reconcile pass. All content is redacted; raw
// bodies never appear here.
type DedupResponse struct {
	DryRun             bool          `json:"dry_run"`
	BackupPath         string        `json:"backup_path,omitempty"`
	TotalBefore        int           `json:"total_before"`
	DuplicatesRemoved  int           `json:"duplicates_removed"` // "would remove" on dry-run
	IdentifiersRemoved int           `json:"identifiers_removed"`
	KeptSoftFacts      int           `json:"kept_soft_facts"`
	Deleted            int           `json:"deleted"`     // rows actually deleted (0 on dry-run)
	TotalAfter         int           `json:"total_after"` // projected on dry-run
	Samples            []DedupSample `json:"samples"`
}

// Dedup handles POST /memory/dedup — the one-time (idempotent) Plan A reconcile
// over the live memory store. It is operator-gated by the same auth as every
// /memory route (loopback + token). Steps: (a) BACKUP all rows to disk;
// (b) compute the conservative plan (exact content-duplicates → keep the
// strongest survivor; typed-identifier assertions → deferred to the graph);
// (c) dry-run reports the plan, apply=true deletes exactly that set. Soft facts
// that exist nowhere else are always kept. The identifier graph / claims are
// never touched.
func (h *MemoryHandler) Dedup(w http.ResponseWriter, r *http.Request) {
	var req DedupRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req) // empty body → dry-run
	}

	memories, err := h.store.ListMemoriesAfter(r.Context(), time.Time{})
	if err != nil {
		h.logger.Error().Err(err).Msg("dedup: failed to list memories")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list memories")
		return
	}

	// (a) BACKUP the raw rows to disk BEFORE any mutation. Fail closed: if a
	// backup was requested (apply) and it cannot be written, abort.
	backupPath, err := h.backupMemories(memories)
	if err != nil {
		h.logger.Error().Err(err).Msg("dedup: backup failed")
		if req.Apply {
			writeMemoryError(w, http.StatusInternalServerError, "BACKUP_FAILED", "refusing to apply without a backup")
			return
		}
	}

	// (b) Plan.
	rows := make([]store.Memory, 0, len(memories))
	for _, m := range memories {
		rows = append(rows, *m)
	}
	plan := tools.PlanReconcile(rows)

	resp := DedupResponse{
		DryRun:             !req.Apply,
		BackupPath:         backupPath,
		TotalBefore:        len(memories),
		DuplicatesRemoved:  plan.DuplicateCount(),
		IdentifiersRemoved: plan.IdentifierCount(),
		KeptSoftFacts:      len(plan.Kept),
	}
	const maxSamples = 20
	for _, d := range plan.Deletions {
		if len(resp.Samples) >= maxSamples {
			break
		}
		resp.Samples = append(resp.Samples, DedupSample{
			Reason: string(d.Reason),
			Sample: tools.RedactContent(d.Memory.Content),
		})
	}

	if !req.Apply {
		resp.TotalAfter = len(memories) - len(plan.Deletions)
		h.logger.Info().Int("total", len(memories)).Int("dup", resp.DuplicatesRemoved).
			Int("ident", resp.IdentifiersRemoved).Int("kept", resp.KeptSoftFacts).
			Msg("dedup dry-run")
		writeMemoryJSON(w, http.StatusOK, resp)
		return
	}

	// (c) APPLY — delete exactly the planned set. Idempotent.
	deleted := 0
	for _, d := range plan.Deletions {
		if err := h.store.DeleteMemory(r.Context(), d.Memory.MemoryID); err != nil {
			h.logger.Error().Err(err).Str("memory_id", d.Memory.MemoryID).Msg("dedup: delete failed")
			writeMemoryError(w, http.StatusInternalServerError, "DELETE_FAILED", "reconcile aborted mid-apply; see backup")
			return
		}
		deleted++
	}
	resp.Deleted = deleted
	resp.TotalAfter = len(memories) - deleted
	h.logger.Info().Int("deleted", deleted).Int("dup", resp.DuplicatesRemoved).
		Int("ident", resp.IdentifiersRemoved).Int("kept", resp.KeptSoftFacts).
		Str("backup", backupPath).Msg("dedup applied")
	writeMemoryJSON(w, http.StatusOK, resp)
}

// backupMemories writes the raw memory rows to "<dbDir>/backups/
// memory-dedup-<ts>.json" (0600) and returns the path. Returns ("", nil) when
// no dbPath is configured (backup-to-disk disabled). The file contains raw PII
// and stays on the pod's data volume alongside the DB it came from.
func (h *MemoryHandler) backupMemories(memories []*store.Memory) (string, error) {
	if h.dbPath == "" {
		return "", nil
	}
	dir := filepath.Join(filepath.Dir(h.dbPath), "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("memory-dedup-%s.json", time.Now().Format("20060102-150405.000000000")))
	out := make([]StoreResponse, 0, len(memories))
	for _, m := range memories {
		out = append(out, memoryToResponse(m))
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal backup: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write backup: %w", err)
	}
	return path, nil
}

// writeMemoryJSON writes a JSON response with the given status code.
func writeMemoryJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// writeMemoryError writes a JSON error response.
func writeMemoryError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}
