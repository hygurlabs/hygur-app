// Package handlers provides HTTP handlers for the Hygur API.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/auth"
	"github.com/hygur/sidecar/internal/mail"
	"github.com/hygur/sidecar/internal/store"
	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
)

// AccountSyncRunner is the subset of the unified mail connector we need to
// run account-aware verify/sync from this handler. Decoupled as an interface
// to keep the api/handlers package unaware of the connector implementation
// type and to enable testing with a fake.
type AccountSyncRunner interface {
	Accounts() AccountRegistrySnapshot
	VerifyAccount(ctx context.Context, accountID string) (AccountSnapshot, error)
	SyncAccount(ctx context.Context, accountID string, opts AccountSyncOptions) (*AccountSyncResult, error)
}

// AccountSnapshot is the secret-free view of one mail account passed back
// through the AccountSyncRunner interface.
type AccountSnapshot struct {
	AccountID    string
	Provider     string
	Email        string
	Status       string
	BriefReason  string
	LastSync     time.Time
	LastVerified time.Time
}

// AccountRegistrySnapshot enumerates all configured mail accounts.
type AccountRegistrySnapshot interface {
	Snapshot() []AccountSnapshot
}

// AccountSyncOptions are the per-account sync knobs accepted by the handler.
type AccountSyncOptions struct {
	Mailbox string
	Limit   int
	Full    bool
}

// AccountSyncResult is the per-account sync outcome.
type AccountSyncResult struct {
	Processed int
	Skipped   int
	Failed    int
	Duration  time.Duration
}

// AccountCounts looks up persisted item counts per account; supplied by the
// store layer to avoid a circular import.
type AccountCounts interface {
	// CountMailItemsByAccount counts email knowledge_items for a given account.
	// provider is the fallback value used to match legacy rows that stored the
	// provider name ("gmail", "proton") instead of the account email address.
	CountMailItemsByAccount(ctx context.Context, accountID, provider string) (count int64, lastIndexed time.Time, err error)
}

// MailHandler handles mail-related API endpoints.
type MailHandler struct {
	connectors          map[string]mail.MailConnector // "gmail", "proton"
	indexer             *mail.EmailIndexer
	summarizeTool       *tools.SummarizeThreadTool
	listAttachmentsTool *tools.ListAttachmentsTool
	credentialStore     *auth.CredentialStore
	accountRunner       AccountSyncRunner
	accountCounts       AccountCounts
	logger              zerolog.Logger
	mu                  sync.RWMutex
}

// SetAccountRunner wires the multi-account capable mail connector adapter.
func (h *MailHandler) SetAccountRunner(r AccountSyncRunner) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.accountRunner = r
}

// SetAccountCounts wires the per-account count lookup.
func (h *MailHandler) SetAccountCounts(c AccountCounts) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.accountCounts = c
}

// ensureConnected checks if the connector is connected and attempts reconnection if not.
// Returns true if connected (or reconnected), false otherwise.
// The source parameter is used for logging.
func (h *MailHandler) ensureConnected(ctx context.Context, source string, connector mail.MailConnector) bool {
	if connector.IsConnected() {
		return true
	}

	// Try to reconnect if the connector supports it
	if reconnector, ok := connector.(mail.Reconnector); ok {
		h.logger.Info().Str("source", source).Msg("attempting auto-reconnect")
		if err := reconnector.Reconnect(ctx); err != nil {
			h.logger.Warn().Err(err).Str("source", source).Msg("auto-reconnect failed")
			return false
		}
		h.logger.Info().Str("source", source).Msg("auto-reconnect successful")
		return true
	}

	return false
}

// NewMailHandler creates a new MailHandler.
func NewMailHandler(logger zerolog.Logger) *MailHandler {
	return &MailHandler{
		connectors: make(map[string]mail.MailConnector),
		logger:     logger.With().Str("handler", "mail").Logger(),
	}
}

// SetConnector registers a connector (called from main.go).
func (h *MailHandler) SetConnector(name string, connector mail.MailConnector) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.connectors[name] = connector
}

// SetIndexer configures the indexer.
func (h *MailHandler) SetIndexer(indexer *mail.EmailIndexer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.indexer = indexer
}

// SetSummarizeTool configures the summarize tool.
func (h *MailHandler) SetSummarizeTool(tool *tools.SummarizeThreadTool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.summarizeTool = tool
}

// SetListAttachmentsTool configures the list attachments tool.
func (h *MailHandler) SetListAttachmentsTool(tool *tools.ListAttachmentsTool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.listAttachmentsTool = tool
}

// SetCredentialStore configures the credential store for persisting mail credentials.
func (h *MailHandler) SetCredentialStore(store *auth.CredentialStore) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.credentialStore = store
}

// SourcesResponse represents the response for GET /mail/sources.
type SourcesResponse struct {
	Sources []Source `json:"sources"`
}

// Source represents a mail source status.
type Source struct {
	Name   string `json:"name"`   // "gmail", "proton"
	Status string `json:"status"` // "connected", "disconnected", "error"
	Error  string `json:"error,omitempty"`
}

// ThreadsResponse represents the response for GET /mail/threads.
type ThreadsResponse struct {
	Threads []ThreadDTO `json:"threads"`
	Total   int         `json:"total"`
}

// ThreadDTO represents a thread in API responses.
type ThreadDTO struct {
	ID             string   `json:"id"`
	Subject        string   `json:"subject"`
	Participants   []string `json:"participants"`
	MessageCount   int      `json:"message_count"`
	HasAttachments bool     `json:"has_attachments"`
	DateStart      string   `json:"date_start"`
	DateEnd        string   `json:"date_end"`
}

// IndexRequest represents the request body for POST /mail/threads/{thread_id}/index.
type IndexRequest struct {
	Source string `json:"source"`
}

// IndexResponse represents the response for POST /mail/threads/{thread_id}/index.
type IndexResponse struct {
	ContentID  string `json:"content_id"`
	ChunkCount int    `json:"chunk_count"`
	Status     string `json:"status"`
}

// SummarizeRequest represents the request body for POST /mail/threads/{thread_id}/summarize.
type SummarizeRequest struct {
	Source string `json:"source"`
	Model  string `json:"model"`
}

// SummaryResponse represents the response for POST /mail/threads/{thread_id}/summarize.
type SummaryResponse struct {
	SummaryID     string   `json:"summary_id"`
	SourceRef     string   `json:"source_ref"`
	ModelUsed     string   `json:"model_used"`
	Decisions     []string `json:"decisions"`
	Actions       []string `json:"actions"`
	OpenQuestions []string `json:"open_questions"`
	CreatedAt     string   `json:"created_at"`
}

// MailboxLister is an interface for connectors that can list mailboxes.
type MailboxLister interface {
	ListMailboxes(ctx context.Context) ([]string, error)
}

// MailboxesResponse represents the response for GET /mail/mailboxes.
type MailboxesResponse struct {
	Mailboxes []string `json:"mailboxes"`
}

// LabelDTO represents a label in API responses.
type LabelDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// LabelsResponse represents the response for GET /mail/labels.
type LabelsResponse struct {
	Labels []LabelDTO `json:"labels"`
}

// Mailboxes handles GET /mail/mailboxes.
func (h *MailHandler) Mailboxes(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	if source == "" {
		writeMailError(w, http.StatusBadRequest, "VALIDATION_ERROR", "source is required")
		return
	}

	h.mu.RLock()
	connector, exists := h.connectors[source]
	h.mu.RUnlock()

	if !exists {
		writeMailError(w, http.StatusNotFound, "NOT_FOUND", "source not found")
		return
	}

	if !h.ensureConnected(r.Context(), source, connector) {
		writeMailError(w, http.StatusServiceUnavailable, "NOT_CONNECTED", "source is not connected")
		return
	}

	lister, ok := connector.(MailboxLister)
	if !ok {
		writeMailError(w, http.StatusBadRequest, "NOT_SUPPORTED", "source does not support listing mailboxes")
		return
	}

	mailboxes, err := lister.ListMailboxes(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Str("source", source).Msg("failed to list mailboxes")
		writeMailError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	writeMailJSON(w, http.StatusOK, MailboxesResponse{Mailboxes: mailboxes})
}

// Labels handles GET /mail/labels.
func (h *MailHandler) Labels(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	if source == "" {
		writeMailError(w, http.StatusBadRequest, "VALIDATION_ERROR", "source is required")
		return
	}

	h.mu.RLock()
	connector, exists := h.connectors[source]
	h.mu.RUnlock()

	if !exists {
		writeMailError(w, http.StatusNotFound, "NOT_FOUND", "source not found")
		return
	}

	if !h.ensureConnected(r.Context(), source, connector) {
		writeMailError(w, http.StatusServiceUnavailable, "NOT_CONNECTED", "source is not connected")
		return
	}

	lister, ok := connector.(mail.LabelLister)
	if !ok {
		writeMailError(w, http.StatusBadRequest, "NOT_SUPPORTED", "source does not support listing labels")
		return
	}

	labels, err := lister.ListLabels(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Str("source", source).Msg("failed to list labels")
		writeMailError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	// Convert to DTOs
	dtos := make([]LabelDTO, 0, len(labels))
	for _, l := range labels {
		dtos = append(dtos, LabelDTO{
			ID:   l.ID,
			Name: l.Name,
			Type: l.Type,
		})
	}

	h.logger.Info().Str("source", source).Int("count", len(labels)).Msg("listed labels")
	writeMailJSON(w, http.StatusOK, LabelsResponse{Labels: dtos})
}

// Sources handles GET /mail/sources.
func (h *MailHandler) Sources(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	sources := make([]Source, 0, len(h.connectors))

	for name, connector := range h.connectors {
		source := Source{
			Name:   name,
			Status: "disconnected",
		}

		if connector.IsConnected() {
			source.Status = "connected"
		}

		sources = append(sources, source)
	}

	// If no connectors are registered, return empty list
	if len(sources) == 0 {
		sources = []Source{}
	}

	writeMailJSON(w, http.StatusOK, SourcesResponse{Sources: sources})
}

// Threads handles GET /mail/threads.
//
// Query params:
//   - account_id (preferred): scopes the request to a single MailAccount.
//   - source (legacy): provider name "gmail"/"proton". Kept for backwards
//     compatibility while the macOS app migrates to account_id.
//   - mailbox: optional mailbox/label filter.
//   - limit, offset: pagination (limit defaults to 20, max 100).
func (h *MailHandler) Threads(w http.ResponseWriter, r *http.Request) {
	accountID := r.URL.Query().Get("account_id")
	source := r.URL.Query().Get("source")
	if accountID == "" && source == "" {
		writeMailError(w, http.StatusBadRequest, "VALIDATION_ERROR", "account_id or source query parameter is required")
		return
	}

	var connector mail.MailConnector
	var routeKey string
	var provider string
	if accountID != "" {
		h.mu.RLock()
		runner := h.accountRunner
		h.mu.RUnlock()
		if runner == nil {
			writeMailError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "multi-account runner not configured")
			return
		}
		registry, ok := runner.Accounts().(interface {
			ConnectorFor(accountID string) (mail.MailConnector, string, error)
		})
		if !ok {
			writeMailError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "registry does not expose ConnectorFor")
			return
		}
		var err error
		connector, provider, err = registry.ConnectorFor(accountID)
		if err != nil {
			writeMailError(w, http.StatusNotFound, "NOT_FOUND", "account not found")
			return
		}
		routeKey = accountID
	} else {
		h.mu.RLock()
		c, exists := h.connectors[source]
		h.mu.RUnlock()
		if !exists {
			writeMailError(w, http.StatusNotFound, "NOT_FOUND", "source not found")
			return
		}
		connector = c
		provider = source
		routeKey = source
	}

	if !h.ensureConnected(r.Context(), routeKey, connector) {
		writeMailError(w, http.StatusServiceUnavailable, "NOT_CONNECTED", "account is not connected")
		return
	}

	// Parse limit and offset
	limit := 20
	offset := 0

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
			if limit > 100 {
				limit = 100
			}
		}
	}

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if parsed, err := strconv.Atoi(offsetStr); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	// Get mailbox (default to "All Mail" for Proton, "INBOX" for others)
	mailbox := r.URL.Query().Get("mailbox")
	if mailbox == "" {
		if provider == "proton" {
			mailbox = "All Mail"
		}
		// For other sources, leave empty to use connector default
	}

	// Parse label_ids (comma-separated or repeated param, e.g. ?label_ids=INBOX&label_ids=Label_123)
	var labelIDs []string
	for _, raw := range r.URL.Query()["label_ids"] {
		for _, id := range strings.Split(raw, ",") {
			if id = strings.TrimSpace(id); id != "" {
				labelIDs = append(labelIDs, id)
			}
		}
	}

	// Fetch threads
	opts := mail.ListOptions{
		Limit:     limit,
		Offset:    offset,
		MailboxID: mailbox,
		LabelIDs:  labelIDs,
	}

	threads, err := connector.ListThreads(r.Context(), opts)
	if err != nil {
		h.logger.Error().Err(err).Str("route", routeKey).Msg("failed to list threads")
		writeMailError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list threads")
		return
	}

	// Sort newest-first as a safety net — Proton already does so but Gmail
	// relies on the API's default ordering, so we enforce it here.
	sortThreadsByDateDesc(threads)

	// Convert to DTOs
	dtos := make([]ThreadDTO, 0, len(threads))
	for _, t := range threads {
		dto := ThreadDTO{
			ID:             t.ID,
			Subject:        t.Subject,
			Participants:   t.Participants,
			MessageCount:   t.MessageCount,
			HasAttachments: t.HasAttachments,
			DateStart:      t.DateRange[0].Format("2006-01-02T15:04:05Z07:00"),
			DateEnd:        t.DateRange[1].Format("2006-01-02T15:04:05Z07:00"),
		}
		dtos = append(dtos, dto)
	}

	writeMailJSON(w, http.StatusOK, ThreadsResponse{
		Threads: dtos,
		Total:   len(dtos),
	})
}

// sortThreadsByDateDesc sorts the supplied slice in place by the latest
// activity date (DateRange[1]) descending. Stable sort keeps the relative
// order of threads that share the same date.
func sortThreadsByDateDesc(threads []mail.Thread) {
	if len(threads) < 2 {
		return
	}
	sort.SliceStable(threads, func(i, j int) bool {
		return threads[i].DateRange[1].After(threads[j].DateRange[1])
	})
}

// Index handles POST /mail/threads/{thread_id}/index.
func (h *MailHandler) Index(w http.ResponseWriter, r *http.Request) {
	threadID := chi.URLParam(r, "thread_id")
	if threadID == "" {
		writeMailError(w, http.StatusBadRequest, "VALIDATION_ERROR", "thread_id is required")
		return
	}

	var req IndexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Debug().Err(err).Msg("failed to parse request body")
		writeMailError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	if req.Source == "" {
		writeMailError(w, http.StatusBadRequest, "VALIDATION_ERROR", "source is required")
		return
	}

	h.mu.RLock()
	connector, exists := h.connectors[req.Source]
	indexer := h.indexer
	h.mu.RUnlock()

	if !exists {
		writeMailError(w, http.StatusNotFound, "NOT_FOUND", "source not found")
		return
	}

	if !h.ensureConnected(r.Context(), req.Source, connector) {
		writeMailError(w, http.StatusServiceUnavailable, "NOT_CONNECTED", "source is not connected")
		return
	}

	if indexer == nil {
		writeMailError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "indexer not configured")
		return
	}

	// Fetch thread
	thread, err := connector.GetThread(r.Context(), threadID)
	if err != nil {
		h.logger.Error().Err(err).Str("thread_id", threadID).Msg("failed to get thread")
		writeMailError(w, http.StatusNotFound, "NOT_FOUND", "thread not found")
		return
	}

	// Fetch messages
	messages, err := connector.GetMessages(r.Context(), threadID)
	if err != nil {
		h.logger.Error().Err(err).Str("thread_id", threadID).Msg("failed to get messages")
		writeMailError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get messages")
		return
	}

	// Index the thread. ?ocr=1 re-extracts scanned/image-only PDF attachments with
	// OCR (operator-triggered recovery of a key hidden in a scan, e.g. an insurance
	// relevé's plate); default off so bulk sync cost is unchanged.
	idxCtx := r.Context()
	if r.URL.Query().Get("ocr") == "1" {
		idxCtx = mail.WithAttachmentOCR(idxCtx)
	}
	result, err := indexer.IndexThread(idxCtx, thread, messages, "")
	if err != nil {
		h.logger.Error().Err(err).Str("thread_id", threadID).Msg("failed to index thread")
		writeMailError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to index thread")
		return
	}

	h.logger.Info().
		Str("thread_id", threadID).
		Str("content_id", result.ContentID).
		Int("chunk_count", result.ChunkCount).
		Str("status", result.Status).
		Msg("thread indexed")

	writeMailJSON(w, http.StatusCreated, IndexResponse{
		ContentID:  result.ContentID,
		ChunkCount: result.ChunkCount,
		Status:     result.Status,
	})
}

// Summarize handles POST /mail/threads/{thread_id}/summarize.
func (h *MailHandler) Summarize(w http.ResponseWriter, r *http.Request) {
	threadID := chi.URLParam(r, "thread_id")
	if threadID == "" {
		writeMailError(w, http.StatusBadRequest, "VALIDATION_ERROR", "thread_id is required")
		return
	}

	var req SummarizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Debug().Err(err).Msg("failed to parse request body")
		writeMailError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	if req.Source == "" {
		writeMailError(w, http.StatusBadRequest, "VALIDATION_ERROR", "source is required")
		return
	}

	if req.Model == "" {
		writeMailError(w, http.StatusBadRequest, "VALIDATION_ERROR", "model is required")
		return
	}

	h.mu.RLock()
	connector, exists := h.connectors[req.Source]
	summarizeTool := h.summarizeTool
	h.mu.RUnlock()

	if !exists {
		writeMailError(w, http.StatusNotFound, "NOT_FOUND", "source not found")
		return
	}

	if !h.ensureConnected(r.Context(), req.Source, connector) {
		writeMailError(w, http.StatusServiceUnavailable, "NOT_CONNECTED", "source is not connected")
		return
	}

	if summarizeTool == nil {
		writeMailError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "summarize tool not configured")
		return
	}

	// Fetch thread
	thread, err := connector.GetThread(r.Context(), threadID)
	if err != nil {
		h.logger.Error().Err(err).Str("thread_id", threadID).Msg("failed to get thread")
		writeMailError(w, http.StatusNotFound, "NOT_FOUND", "thread not found")
		return
	}

	// Fetch messages
	messages, err := connector.GetMessages(r.Context(), threadID)
	if err != nil {
		h.logger.Error().Err(err).Str("thread_id", threadID).Msg("failed to get messages")
		writeMailError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get messages")
		return
	}

	// Generate summary
	summary, err := summarizeTool.Run(r.Context(), thread, messages, req.Model)
	if err != nil {
		h.logger.Error().Err(err).Str("thread_id", threadID).Msg("failed to summarize thread")
		writeMailError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to summarize thread")
		return
	}

	h.logger.Info().
		Str("thread_id", threadID).
		Str("summary_id", summary.SummaryID).
		Str("model", req.Model).
		Msg("thread summarized")

	writeMailJSON(w, http.StatusOK, convertSummaryToResponse(summary))
}

// convertSummaryToResponse converts a store.Summary to SummaryResponse.
func convertSummaryToResponse(s *store.Summary) SummaryResponse {
	return SummaryResponse{
		SummaryID:     s.SummaryID,
		SourceRef:     s.SourceRef,
		ModelUsed:     s.ModelUsed,
		Decisions:     s.Decisions,
		Actions:       s.Actions,
		OpenQuestions: s.OpenQuestions,
		CreatedAt:     s.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// AttachmentsResponse represents the response for GET /mail/threads/{thread_id}/attachments.
type AttachmentsResponse struct {
	Attachments []AttachmentDTO `json:"attachments"`
	ThreadID    string          `json:"thread_id"`
	Source      string          `json:"source"`
}

// AttachmentDTO represents an attachment in API responses.
type AttachmentDTO struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"`
}

// Attachments handles GET /mail/threads/{thread_id}/attachments.
func (h *MailHandler) Attachments(w http.ResponseWriter, r *http.Request) {
	threadID := chi.URLParam(r, "thread_id")
	if threadID == "" {
		writeMailError(w, http.StatusBadRequest, "VALIDATION_ERROR", "thread_id is required")
		return
	}

	source := r.URL.Query().Get("source")
	if source == "" {
		writeMailError(w, http.StatusBadRequest, "VALIDATION_ERROR", "source query parameter is required")
		return
	}

	h.mu.RLock()
	listAttachmentsTool := h.listAttachmentsTool
	h.mu.RUnlock()

	if listAttachmentsTool == nil {
		writeMailError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "list attachments tool not configured")
		return
	}

	// Run the tool
	result, err := listAttachmentsTool.Run(r.Context(), tools.ListAttachmentsRequest{
		ThreadID: threadID,
		Source:   source,
	})
	if err != nil {
		h.logger.Error().Err(err).Str("thread_id", threadID).Str("source", source).Msg("failed to list attachments")

		// Determine appropriate error code based on error message
		if contains(err.Error(), "not found") {
			writeMailError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
			return
		}
		if contains(err.Error(), "not connected") {
			writeMailError(w, http.StatusServiceUnavailable, "NOT_CONNECTED", err.Error())
			return
		}
		writeMailError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list attachments")
		return
	}

	// Convert to response DTOs
	attachments := make([]AttachmentDTO, 0, len(result.Attachments))
	for _, att := range result.Attachments {
		attachments = append(attachments, AttachmentDTO{
			ID:       att.ID,
			Filename: att.Filename,
			MIMEType: att.MIMEType,
			Size:     att.Size,
		})
	}

	h.logger.Info().
		Str("thread_id", threadID).
		Str("source", source).
		Int("count", len(attachments)).
		Msg("listed attachments")

	writeMailJSON(w, http.StatusOK, AttachmentsResponse{
		Attachments: attachments,
		ThreadID:    threadID,
		Source:      source,
	})
}

// writeMailJSON writes a JSON response with the given status code.
func writeMailJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// writeMailError writes a JSON error response.
func writeMailError(w http.ResponseWriter, status int, code, message string) {
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

// CredentialsResponse represents the response for GET /mail/credentials.
type CredentialsResponse struct {
	Credentials []CredentialDTO `json:"credentials"`
}

// CredentialDTO represents a saved credential without sensitive data.
type CredentialDTO struct {
	Source   string `json:"source"`
	Username string `json:"username,omitempty"`
}

// Credentials handles GET /mail/credentials.
// Returns a list of saved mail credentials without sensitive data.
func (h *MailHandler) Credentials(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	credStore := h.credentialStore
	h.mu.RUnlock()

	if credStore == nil {
		writeMailError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "credential storage not configured")
		return
	}

	creds, err := credStore.ListCredentials()
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list credentials")
		writeMailError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list credentials")
		return
	}

	dtos := make([]CredentialDTO, 0, len(creds))
	for _, c := range creds {
		dtos = append(dtos, CredentialDTO{
			Source:   c.Source,
			Username: c.Username,
		})
	}

	writeMailJSON(w, http.StatusOK, CredentialsResponse{Credentials: dtos})
}

// DeleteCredential handles DELETE /mail/credentials/{source}.
// Removes saved credentials for the specified source.
func (h *MailHandler) DeleteCredential(w http.ResponseWriter, r *http.Request) {
	source := chi.URLParam(r, "source")
	if source == "" {
		writeMailError(w, http.StatusBadRequest, "VALIDATION_ERROR", "source is required")
		return
	}

	h.mu.RLock()
	credStore := h.credentialStore
	h.mu.RUnlock()

	if credStore == nil {
		writeMailError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "credential storage not configured")
		return
	}

	if err := credStore.DeleteMailCredential(source); err != nil {
		h.logger.Error().Err(err).Str("source", source).Msg("failed to delete credential")
		writeMailError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete credential")
		return
	}

	h.logger.Info().Str("source", source).Msg("credential deleted")
	writeMailJSON(w, http.StatusOK, map[string]string{
		"status":  "deleted",
		"source":  source,
		"message": "Credential deleted successfully",
	})
}

// accountConnectorFor is the subset of AccountRegistrySnapshot needed to
// resolve a live connector by account ID. AccountRegistry implements this;
// test fakes can implement it too without exposing the full registry type.
type accountConnectorFor interface {
	ConnectorFor(accountID string) (mail.MailConnector, string, error)
}

// resolveAccountConnector extracts a connector from the runner for the given
// account. Returns 404 if the runner is nil, account is not found, or the
// registry does not support ConnectorFor.
func (h *MailHandler) resolveAccountConnector(w http.ResponseWriter, accountID string, runner AccountSyncRunner) (mail.MailConnector, string, bool) {
	if runner == nil {
		writeMailError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "multi-account runner not configured")
		return nil, "", false
	}
	registry, ok := runner.Accounts().(accountConnectorFor)
	if !ok {
		writeMailError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "registry does not expose ConnectorFor")
		return nil, "", false
	}
	conn, provider, err := registry.ConnectorFor(accountID)
	if err != nil {
		writeMailError(w, http.StatusNotFound, "NOT_FOUND", "account not found")
		return nil, "", false
	}
	return conn, provider, true
}

// ---------------------------------------------------------------------------
// Multi-account endpoints
// ---------------------------------------------------------------------------

// MailAccountDTO is the API representation of a single configured mail
// account. brief_reason is one of the codes defined by internal/mail/diag.
type MailAccountDTO struct {
	AccountID    string `json:"account_id"`
	Provider     string `json:"provider"`
	Email        string `json:"email"`
	Status       string `json:"status"`
	BriefReason  string `json:"brief_reason"`
	ThreadCount  int64  `json:"thread_count"`
	LastSync     string `json:"last_sync,omitempty"`
	LastVerified string `json:"last_verified,omitempty"`
	LastIndexed  string `json:"last_indexed,omitempty"`
}

// AccountsResponse is the body of GET /mail/accounts.
type AccountsResponse struct {
	Accounts []MailAccountDTO `json:"accounts"`
}

// Accounts handles GET /mail/accounts. Returns the list of configured mail
// accounts with health, brief_reason and per-account thread counts.
func (h *MailHandler) Accounts(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	runner := h.accountRunner
	counts := h.accountCounts
	h.mu.RUnlock()

	if runner == nil {
		writeMailJSON(w, http.StatusOK, AccountsResponse{Accounts: []MailAccountDTO{}})
		return
	}

	snapshots := runner.Accounts().Snapshot()
	dtos := make([]MailAccountDTO, 0, len(snapshots))
	for _, s := range snapshots {
		dto := MailAccountDTO{
			AccountID:   s.AccountID,
			Provider:    s.Provider,
			Email:       s.Email,
			Status:      mapStatusForUI(s.Status),
			BriefReason: defaultBriefReason(s.BriefReason),
		}
		if !s.LastSync.IsZero() {
			dto.LastSync = s.LastSync.Format(time.RFC3339)
		}
		if !s.LastVerified.IsZero() {
			dto.LastVerified = s.LastVerified.Format(time.RFC3339)
		}
		if counts != nil {
			if c, lastIdx, cerr := counts.CountMailItemsByAccount(r.Context(), s.AccountID, s.Provider); cerr == nil {
				dto.ThreadCount = c
				if !lastIdx.IsZero() {
					dto.LastIndexed = lastIdx.Format(time.RFC3339)
				}
			}
		}
		dtos = append(dtos, dto)
	}

	writeMailJSON(w, http.StatusOK, AccountsResponse{Accounts: dtos})
}

// VerifyAccountResponse is the body of POST /mail/accounts/{account_id}/verify.
type VerifyAccountResponse struct {
	AccountID    string `json:"account_id"`
	Status       string `json:"status"`
	BriefReason  string `json:"brief_reason"`
	LastVerified string `json:"last_verified"`
}

// VerifyAccount handles POST /mail/accounts/{account_id}/verify. Triggers
// (or reuses, when fresh) an active connectivity check.
func (h *MailHandler) VerifyAccount(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "account_id")
	if accountID == "" {
		writeMailError(w, http.StatusBadRequest, "VALIDATION_ERROR", "account_id is required")
		return
	}

	h.mu.RLock()
	runner := h.accountRunner
	h.mu.RUnlock()
	if runner == nil {
		writeMailError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "multi-account runner not configured")
		return
	}

	snap, err := runner.VerifyAccount(r.Context(), accountID)
	if err != nil {
		if isAccountNotFound(err) {
			writeMailError(w, http.StatusNotFound, "NOT_FOUND", "account not found")
			return
		}
		writeMailError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	resp := VerifyAccountResponse{
		AccountID:   snap.AccountID,
		Status:      mapStatusForUI(snap.Status),
		BriefReason: defaultBriefReason(snap.BriefReason),
	}
	if !snap.LastVerified.IsZero() {
		resp.LastVerified = snap.LastVerified.Format(time.RFC3339)
	}
	writeMailJSON(w, http.StatusOK, resp)
}

// AccountStatsResponse is the body of GET /mail/accounts/{account_id}/stats.
type AccountStatsResponse struct {
	AccountID   string `json:"account_id"`
	ThreadCount int64  `json:"thread_count"`
	LastIndexed string `json:"last_indexed,omitempty"`
}

// AccountStats handles GET /mail/accounts/{account_id}/stats.
func (h *MailHandler) AccountStats(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "account_id")
	if accountID == "" {
		writeMailError(w, http.StatusBadRequest, "VALIDATION_ERROR", "account_id is required")
		return
	}

	h.mu.RLock()
	counts := h.accountCounts
	runner := h.accountRunner
	h.mu.RUnlock()
	if counts == nil {
		writeMailJSON(w, http.StatusOK, AccountStatsResponse{AccountID: accountID})
		return
	}

	// Resolve the provider for legacy-data fallback matching.
	provider := ""
	if runner != nil {
		for _, s := range runner.Accounts().Snapshot() {
			if s.AccountID == accountID {
				provider = s.Provider
				break
			}
		}
	}

	count, lastIndexed, err := counts.CountMailItemsByAccount(r.Context(), accountID, provider)
	if err != nil {
		writeMailError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	resp := AccountStatsResponse{AccountID: accountID, ThreadCount: count}
	if !lastIndexed.IsZero() {
		resp.LastIndexed = lastIndexed.Format(time.RFC3339)
	}
	writeMailJSON(w, http.StatusOK, resp)
}

// AccountLabels handles GET /mail/accounts/{account_id}/labels. Returns the
// labels available for the given account by calling the provider connector
// directly, scoped to this account's credentials.
func (h *MailHandler) AccountLabels(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "account_id")
	if accountID == "" {
		writeMailError(w, http.StatusBadRequest, "VALIDATION_ERROR", "account_id is required")
		return
	}

	h.mu.RLock()
	runner := h.accountRunner
	h.mu.RUnlock()

	conn, _, ok := h.resolveAccountConnector(w, accountID, runner)
	if !ok {
		return
	}

	if !h.ensureConnected(r.Context(), accountID, conn) {
		writeMailError(w, http.StatusServiceUnavailable, "NOT_CONNECTED", "account is not connected")
		return
	}

	lister, ok := conn.(mail.LabelLister)
	if !ok {
		writeMailError(w, http.StatusBadRequest, "NOT_SUPPORTED", "account provider does not support listing labels")
		return
	}

	labels, err := lister.ListLabels(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Str("account_id", accountID).Msg("failed to list labels for account")
		writeMailError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	dtos := make([]LabelDTO, 0, len(labels))
	for _, l := range labels {
		dtos = append(dtos, LabelDTO{ID: l.ID, Name: l.Name, Type: l.Type})
	}
	h.logger.Info().Str("account_id", accountID).Int("count", len(labels)).Msg("listed account labels")
	writeMailJSON(w, http.StatusOK, LabelsResponse{Labels: dtos})
}

// AccountMailboxes handles GET /mail/accounts/{account_id}/mailboxes. Returns
// the IMAP folders (or equivalent) for the given account.
func (h *MailHandler) AccountMailboxes(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "account_id")
	if accountID == "" {
		writeMailError(w, http.StatusBadRequest, "VALIDATION_ERROR", "account_id is required")
		return
	}

	h.mu.RLock()
	runner := h.accountRunner
	h.mu.RUnlock()

	conn, _, ok := h.resolveAccountConnector(w, accountID, runner)
	if !ok {
		return
	}

	if !h.ensureConnected(r.Context(), accountID, conn) {
		writeMailError(w, http.StatusServiceUnavailable, "NOT_CONNECTED", "account is not connected")
		return
	}

	lister, ok := conn.(MailboxLister)
	if !ok {
		writeMailError(w, http.StatusBadRequest, "NOT_SUPPORTED", "account provider does not support listing mailboxes")
		return
	}

	mailboxes, err := lister.ListMailboxes(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Str("account_id", accountID).Msg("failed to list mailboxes for account")
		writeMailError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	h.logger.Info().Str("account_id", accountID).Int("count", len(mailboxes)).Msg("listed account mailboxes")
	writeMailJSON(w, http.StatusOK, MailboxesResponse{Mailboxes: mailboxes})
}

// mapStatusForUI converts plugin.Status (e.g. "healthy", "degraded",
// "unhealthy", "unconfigured") to the simpler vocabulary the macOS app
// expects ("connected" / "disconnected" / "error").
func mapStatusForUI(status string) string {
	switch status {
	case "healthy":
		return "connected"
	case "degraded":
		return "connected"
	case "unhealthy", "":
		return "disconnected"
	case "unconfigured":
		return "disconnected"
	}
	return status
}

// defaultBriefReason ensures the response always has a non-empty reason code
// — empty maps to "unknown_issue" so the client can render a fallback label.
func defaultBriefReason(reason string) string {
	if reason == "" {
		return "unknown_issue"
	}
	return reason
}

// isAccountNotFound checks for the connector-level not-found sentinel via
// error message matching to avoid importing the connector package here.
func isAccountNotFound(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "not found")
}
