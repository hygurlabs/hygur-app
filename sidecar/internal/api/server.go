// Package api provides the HTTP API server for the Hygur sidecar.
package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/hygur/sidecar/internal/api/handlers"
	"github.com/hygur/sidecar/internal/auth"
	"github.com/hygur/sidecar/internal/config"
	"github.com/hygur/sidecar/internal/edge"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/rs/zerolog"
)

// Server represents the HTTP API server.
type Server struct {
	cfg              *config.Config
	logger           zerolog.Logger
	router           chi.Router
	llmClient        *llm.Client
	httpServer       *http.Server
	healthHandler    *handlers.HealthHandler
	chatHandler      *handlers.ChatHandler
	ragChatHandler   *handlers.RAGChatHandler
	modelsHandler    *handlers.ModelsHandler
	knowledgeHandler *handlers.KnowledgeHandler
	projectHandler   *handlers.ProjectHandler
	taskHandler      *handlers.TaskHandler
	mailHandler      *handlers.MailHandler
	searchHandler    *handlers.SearchHandler
	notesHandler     *handlers.NotesHandler
	sessionsHandler  *handlers.SessionsHandler
	tagHandler       *handlers.TagHandler
	graphHandler     *handlers.GraphHandler
	connectorHandler   *handlers.ConnectorHandler
	marketplaceHandler *handlers.MarketplaceHandler
	memoryHandler    *handlers.MemoryHandler
	eventsHandler    *handlers.EventsHandler
	briefHandler     *handlers.BriefHandler
	chronicleHandler *handlers.ChronicleHandler
	timelineHandler  *handlers.TimelineHandler
	agendaHandler    *handlers.AgendaHandler
	configHandler    *handlers.ConfigHandler
	mentionsHandler  *handlers.MentionsHandler
	interactionsHandler *handlers.InteractionsHandler
	insightsHandler  *handlers.InsightsHandler
	usageHandler     *handlers.UsageHandler
	backupHandler    *handlers.BackupHandler
	exportHandler    *handlers.ExportHandler
	encryptionHandler *handlers.EncryptionHandler
	token            string             // Static token (local mode) + WebUI bootstrap
	authenticator    auth.Authenticator // Selected by config: local token or remote JWT
	hostGuardEnabled bool               // DNS-rebinding Host allow-list (SetHostGuard)
	allowedHosts     map[string]bool
	managed          bool // Hygur-operated cloud tenant: don't inject the loopback token into the SPA
	cloudProxy       *httputil.ReverseProxy // non-nil = cloud-backed thin-client mode (SetCloudProxy)
	extraOrigins     map[string]bool        // extra CORS-allowed origins (SetAllowedOrigins) — e.g. the cloud web shell
	cspConnectSrc    []string               // resolved connect-src sources for the served SPA CSP (SetCSPConnectSources)
	edgeRunner       *edge.Runner           // local edge push loop (cloud thin client) — backs the /edge/* routes
}

// SetManaged marks this as a Hygur-operated cloud tenant. The served SPA then
// ships no bootstrap token (auth is per-device JWT; the user connects explicitly).
func (s *Server) SetManaged(v bool) { s.managed = v }

// SetHostGuard enables DNS-rebinding protection: requests whose Host isn't loopback
// or in `hosts` are rejected (except /health, /version). Loopback is always allowed
// so the local desktop works without configuration. No-op when enabled=false.
func (s *Server) SetHostGuard(enabled bool, hosts []string) {
	s.hostGuardEnabled = enabled
	s.allowedHosts = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	for _, h := range hosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			s.allowedHosts[h] = true
		}
	}
}

// SetAllowedOrigins permits additional cross-origin web shells to call this API
// (CORS), on top of the always-allowed loopback + Tauri origins. In Hygur Cloud
// the central web app (e.g. https://cloud.hygur.ai) is served on a different host
// than the tenant API, so it calls the tenant cross-origin; list its origin here.
func (s *Server) SetAllowedOrigins(origins []string) {
	s.extraOrigins = make(map[string]bool, len(origins))
	for _, o := range origins {
		if o = strings.ToLower(strings.TrimSpace(o)); o != "" {
			s.extraOrigins[o] = true
		}
	}
}

// SetCSPConnectSources sets the extra connect-src origins for the served SPA's
// Content-Security-Policy (see buildCSP). main resolves these from the cloud
// upstream host, HYGUR_CONSOLE_ORIGIN and HYGUR_ALLOWED_ORIGINS so the desktop
// CSP tightens to the exact tenant/console host instead of the *.hygur.ai
// wildcard. The unconditional fail-safe sources ('self', ipc:, http://ipc.localhost)
// are added in buildCSP regardless of what is passed here. Mirrors
// SetAllowedOrigins.
func (s *Server) SetCSPConnectSources(origins []string) {
	s.cspConnectSrc = nil
	for _, o := range origins {
		if o = strings.TrimSpace(o); o != "" {
			s.cspConnectSrc = append(s.cspConnectSrc, o)
		}
	}
}

// SetEdgeRunner wires the on-device edge push loop so the /edge/* routes can list
// local Proton folders, report sync status, and trigger a sync. Cloud thin client
// only; nil in local/self-host mode (the routes then return 503).
func (s *Server) SetEdgeRunner(r *edge.Runner) { s.edgeRunner = r }

// NewServer creates a new API server instance.
// The token parameter is used for authenticating API requests via the X-Hygur-Token header.
func NewServer(cfg *config.Config, logger zerolog.Logger, token string) *Server {
	s := &Server{
		cfg:    cfg,
		logger: logger.With().Str("component", "api").Logger(),
		router: chi.NewRouter(),
		token:  token,
		// Default to the loopback single-token scheme. Remote (JWT) auth is
		// opted into in main via SetAuthenticator when auth.mode == "remote".
		authenticator: auth.LocalTokenAuth{Token: token},
		// Initialise the health handler eagerly with a nil LLM client so the
		// /health endpoint reports `version` + `lm_studio: disconnected` even
		// before SetLLMClient is called (e.g. during tests or boot warmup).
		healthHandler: handlers.NewHealthHandler(nil),
	}

	s.setupMiddleware()
	s.setupRoutes()

	return s
}

// SetAuthenticator overrides the request authenticator. main calls this when
// auth.mode == "remote" to swap the default loopback token scheme for per-device
// JWT verification.
func (s *Server) SetAuthenticator(a auth.Authenticator) {
	if a != nil {
		s.authenticator = a
	}
}

// SetLLMClient sets the LLM client for the server.
// This allows dependency injection of the LLM client.
func (s *Server) SetLLMClient(client *llm.Client) {
	s.llmClient = client
	// Update the health handler with the new client
	s.healthHandler = handlers.NewHealthHandler(client)
	// Update the chat handler with the new client
	s.chatHandler = handlers.NewChatHandler(client, s.logger)
	// Update the models handler with the new client
	s.modelsHandler = handlers.NewModelsHandler(client, s.logger)
}

// SetKnowledgeHandler sets the knowledge handler for the server.
// This allows dependency injection of the knowledge handler.
func (s *Server) SetKnowledgeHandler(handler *handlers.KnowledgeHandler) {
	s.knowledgeHandler = handler
}

// SetProjectHandler sets the project handler for the server.
// This allows dependency injection of the project handler.
func (s *Server) SetProjectHandler(handler *handlers.ProjectHandler) {
	s.projectHandler = handler
}

// SetTaskHandler sets the task handler for the server.
func (s *Server) SetTaskHandler(handler *handlers.TaskHandler) {
	s.taskHandler = handler
}

// SetChronicleHandler sets the chronicle handler for the server.
func (s *Server) SetChronicleHandler(handler *handlers.ChronicleHandler) {
	s.chronicleHandler = handler
}

func (s *Server) handleChronicleList(w http.ResponseWriter, r *http.Request) {
	if s.chronicleHandler != nil {
		s.chronicleHandler.List(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "chronicle handler not configured")
}

func (s *Server) handleChronicleGet(w http.ResponseWriter, r *http.Request) {
	if s.chronicleHandler != nil {
		s.chronicleHandler.Get(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "chronicle handler not configured")
}

func (s *Server) handleChronicleRun(w http.ResponseWriter, r *http.Request) {
	if s.chronicleHandler != nil {
		s.chronicleHandler.Run(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "chronicle handler not configured")
}

func (s *Server) handleChronicleClose(w http.ResponseWriter, r *http.Request) {
	if s.chronicleHandler != nil {
		s.chronicleHandler.Close(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "chronicle handler not configured")
}

func (s *Server) handleChronicleReopen(w http.ResponseWriter, r *http.Request) {
	if s.chronicleHandler != nil {
		s.chronicleHandler.Reopen(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "chronicle handler not configured")
}

func (s *Server) handleTaskList(w http.ResponseWriter, r *http.Request) {
	if s.taskHandler != nil {
		s.taskHandler.List(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "task handler not configured")
}

func (s *Server) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	if s.taskHandler != nil {
		s.taskHandler.Create(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "task handler not configured")
}

func (s *Server) handleTaskGet(w http.ResponseWriter, r *http.Request) {
	if s.taskHandler != nil {
		s.taskHandler.Get(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "task handler not configured")
}

func (s *Server) handleTaskPatch(w http.ResponseWriter, r *http.Request) {
	if s.taskHandler != nil {
		s.taskHandler.Patch(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "task handler not configured")
}

func (s *Server) handleTaskDelete(w http.ResponseWriter, r *http.Request) {
	if s.taskHandler != nil {
		s.taskHandler.Delete(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "task handler not configured")
}

// SetMailHandler sets the mail handler for the server.
// This allows dependency injection of the mail handler.
func (s *Server) SetMailHandler(handler *handlers.MailHandler) {
	s.mailHandler = handler
}

// SetSearchHandler sets the search handler for the server.
// This allows dependency injection of the search handler.
func (s *Server) SetSearchHandler(handler *handlers.SearchHandler) {
	s.searchHandler = handler
}

// SetNotesHandler sets the notes handler for the server.
// This allows dependency injection of the notes handler.
func (s *Server) SetNotesHandler(handler *handlers.NotesHandler) {
	s.notesHandler = handler
}

// SetSessionsHandler sets the chat-sessions handler for the server.
func (s *Server) SetSessionsHandler(handler *handlers.SessionsHandler) {
	s.sessionsHandler = handler
}

// SetRAGChatHandler sets the RAG chat handler for the server.
// This allows dependency injection of the RAG-enhanced chat handler.
func (s *Server) SetRAGChatHandler(handler *handlers.RAGChatHandler) {
	s.ragChatHandler = handler
}

// SetTagHandler sets the tag handler for the server.
// This allows dependency injection of the tag handler.
func (s *Server) SetTagHandler(handler *handlers.TagHandler) {
	s.tagHandler = handler
}

// SetGraphHandler sets the graph handler for the server.
// This allows dependency injection of the graph handler.
func (s *Server) SetGraphHandler(handler *handlers.GraphHandler) {
	s.graphHandler = handler
}

// SetConnectorHandler sets the connector handler for the server.
func (s *Server) SetConnectorHandler(handler *handlers.ConnectorHandler) {
	s.connectorHandler = handler
}

// SetMarketplaceHandler sets the marketplace handler for the server.
func (s *Server) SetMarketplaceHandler(handler *handlers.MarketplaceHandler) {
	s.marketplaceHandler = handler
}

// setupMiddleware configures the middleware stack in the correct order.
// Order matters: RequestID -> RealIP -> Logger -> Recoverer
// Note: Timeout is applied per-route group in setupRoutes() to allow different timeouts.
func (s *Server) setupMiddleware() {
	// Request ID for tracing
	s.router.Use(middleware.RequestID)

	// Real IP extraction (for proxied requests)
	s.router.Use(middleware.RealIP)

	// DNS-rebinding guard (Host allow-list). Runs early; no-op unless enabled.
	s.router.Use(s.hostGuardMiddleware)

	// CORS — required for cross-origin clients (the Tauri desktop shell serves
	// its UI from tauri://localhost; vite dev from http://localhost:5173). Runs
	// before auth so preflight OPTIONS needs no token. Same-origin clients (the
	// sidecar-served UI) are unaffected.
	s.router.Use(s.corsMiddleware)

	// Custom zerolog logger middleware
	s.router.Use(s.loggerMiddleware)

	// Panic recovery with logging
	s.router.Use(s.recovererWithLogger)

	// Cloud-backed thin-client proxy. No-op in local mode; when SetCloudProxy is
	// active it forwards data/AI routes to the tenant (runs after logger+recoverer
	// so proxied requests are still logged + panic-protected, and before the
	// per-route auth so the device token — not the local token — authenticates).
	s.router.Use(s.cloudProxyMiddleware)
}

// LongOperationTimeout is the timeout for long-running operations like folder ingestion.
const LongOperationTimeout = 5 * time.Minute

// Start starts the HTTP server and blocks until the context is cancelled.
// It handles graceful shutdown when the context is done.
func (s *Server) Start(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)

	// WriteTimeout must accommodate long-running operations like folder ingestion
	writeTimeout := s.cfg.Server.WriteTimeout
	if writeTimeout < LongOperationTimeout {
		writeTimeout = LongOperationTimeout + 10*time.Second // Add buffer
	}

	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           s.router,
		ReadTimeout:       s.cfg.Server.ReadTimeout,
		WriteTimeout:      writeTimeout,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Channel to receive server errors
	errCh := make(chan error, 1)

	// Start the server in a goroutine
	go func() {
		s.logger.Info().Str("addr", addr).Msg("starting HTTP server")
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for context cancellation or server error
	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		return s.Shutdown(context.Background())
	}
}

// Shutdown gracefully shuts down the server.
// It waits for active connections to complete up to the configured timeout.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}

	shutdownStart := time.Now()
	s.logger.Info().Msg("initiating graceful shutdown")

	// Create a timeout context for shutdown
	shutdownCtx, cancel := context.WithTimeout(ctx, s.cfg.Server.ShutdownTimeout)
	defer cancel()

	// Attempt graceful shutdown
	err := s.httpServer.Shutdown(shutdownCtx)

	shutdownDuration := time.Since(shutdownStart)
	if err != nil {
		s.logger.Error().
			Err(err).
			Dur("duration", shutdownDuration).
			Msg("shutdown error")
		return fmt.Errorf("shutdown error: %w", err)
	}

	s.logger.Info().
		Dur("duration", shutdownDuration).
		Msg("server shutdown complete")

	return nil
}

// Router returns the underlying chi router.
// This is useful for testing purposes.
func (s *Server) Router() chi.Router {
	return s.router
}

// Addr returns the server address if the server is running.
func (s *Server) Addr() string {
	if s.httpServer != nil {
		return s.httpServer.Addr
	}
	return ""
}

// SetMemoryHandler sets the memory handler for the server.
func (s *Server) SetMemoryHandler(handler *handlers.MemoryHandler) {
	s.memoryHandler = handler
}

// handleMemoryStore handles POST /memory/store.
func (s *Server) handleMemoryStore(w http.ResponseWriter, r *http.Request) {
	if s.memoryHandler != nil {
		s.memoryHandler.Store(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "memory handler not configured")
}

// handleMemorySearch handles GET /memory/search.
func (s *Server) handleMemorySearch(w http.ResponseWriter, r *http.Request) {
	if s.memoryHandler != nil {
		s.memoryHandler.MemorySearch(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "memory handler not configured")
}

// handleMemorySync handles GET /memory/sync.
func (s *Server) handleMemorySync(w http.ResponseWriter, r *http.Request) {
	if s.memoryHandler != nil {
		s.memoryHandler.Sync(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "memory handler not configured")
}

// handleMemoryList handles GET /memory/list.
func (s *Server) handleMemoryList(w http.ResponseWriter, r *http.Request) {
	if s.memoryHandler != nil {
		s.memoryHandler.List(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "memory handler not configured")
}

// handleMemoryDelete handles DELETE /memory/{memory_id}.
func (s *Server) handleMemoryDelete(w http.ResponseWriter, r *http.Request) {
	if s.memoryHandler != nil {
		s.memoryHandler.Delete(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "memory handler not configured")
}

// handleMemoryPending handles GET /memory/pending — Phase 3.3 long-term memory.
func (s *Server) handleMemoryPending(w http.ResponseWriter, r *http.Request) {
	if s.memoryHandler != nil {
		s.memoryHandler.Pending(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "memory handler not configured")
}

// handleMemoryAccept handles POST /memory/{memory_id}/accept.
func (s *Server) handleMemoryAccept(w http.ResponseWriter, r *http.Request) {
	if s.memoryHandler != nil {
		s.memoryHandler.Accept(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "memory handler not configured")
}

// handleMemoryDiscard handles POST /memory/{memory_id}/discard.
func (s *Server) handleMemoryDiscard(w http.ResponseWriter, r *http.Request) {
	if s.memoryHandler != nil {
		s.memoryHandler.Discard(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "memory handler not configured")
}

// handleMemoryExtract handles POST /memory/extract.
func (s *Server) handleMemoryExtract(w http.ResponseWriter, r *http.Request) {
	if s.memoryHandler != nil {
		s.memoryHandler.Extract(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "memory handler not configured")
}

// handleMemoryStats handles GET /memory/stats.
func (s *Server) handleMemoryStats(w http.ResponseWriter, r *http.Request) {
	if s.memoryHandler != nil {
		s.memoryHandler.Stats(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "memory handler not configured")
}

// handleMemoryClearExtracted handles DELETE /memory/extracted.
func (s *Server) handleMemoryClearExtracted(w http.ResponseWriter, r *http.Request) {
	if s.memoryHandler != nil {
		s.memoryHandler.ClearExtracted(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "memory handler not configured")
}

// SetEventsHandler sets the events handler for the server.
func (s *Server) SetEventsHandler(handler *handlers.EventsHandler) {
	s.eventsHandler = handler
}

// handleEvents handles GET /events (SSE for background operations).
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.eventsHandler != nil {
		s.eventsHandler.Handle(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "events handler not configured")
}

// SetBriefHandler attaches an on-demand brief handler. May be left nil
// when daily_brief.enabled=false — the route then returns 503.
func (s *Server) SetBriefHandler(handler *handlers.BriefHandler) {
	s.briefHandler = handler
}

// handleBriefRun handles POST /brief/run (manual trigger).
func (s *Server) handleBriefRun(w http.ResponseWriter, r *http.Request) {
	if s.briefHandler != nil {
		s.briefHandler.RunNow(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "brief handler not configured")
}

// handleBriefingsList handles GET /briefings.
func (s *Server) handleBriefingsList(w http.ResponseWriter, r *http.Request) {
	if s.briefHandler != nil {
		s.briefHandler.List(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "brief handler not configured")
}

// SetTimelineHandler attaches the timeline query handler.
func (s *Server) SetTimelineHandler(handler *handlers.TimelineHandler) {
	s.timelineHandler = handler
}

// handleTimelineQuery handles POST /timeline/query.
func (s *Server) handleTimelineQuery(w http.ResponseWriter, r *http.Request) {
	if s.timelineHandler != nil {
		s.timelineHandler.Query(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "timeline handler not configured")
}

// SetAgendaHandler attaches the agenda context handler.
func (s *Server) SetAgendaHandler(handler *handlers.AgendaHandler) {
	s.agendaHandler = handler
}

// handleAgendaContext handles GET /agenda/context.
func (s *Server) handleAgendaContext(w http.ResponseWriter, r *http.Request) {
	if s.agendaHandler != nil {
		s.agendaHandler.AgendaContext(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "agenda handler not configured")
}

// handleCalendarSummary handles GET /agenda/calendar-summary.
func (s *Server) handleCalendarSummary(w http.ResponseWriter, r *http.Request) {
	if s.briefHandler != nil {
		s.briefHandler.CalendarSummary(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"summary":"","window":"","count":0}`))
}

// handleKnowledgeFollowup handles GET /knowledge/followup — the grounded
// Follow-up digest. Delegates to the BriefHandler (it owns the LLM + store).
func (s *Server) handleKnowledgeFollowup(w http.ResponseWriter, r *http.Request) {
	if s.briefHandler != nil {
		s.briefHandler.FollowUp(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"topics":[],"contradictions":[],"scanned":0,"window":""}`))
}

// handleAgendaEvents handles GET /agenda/events — calendar events by date window.
func (s *Server) handleAgendaEvents(w http.ResponseWriter, r *http.Request) {
	if s.briefHandler != nil {
		s.briefHandler.AgendaEvents(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"events":[]}`))
}

// handleDraftReply handles POST /knowledge/{content_id}/draft-reply.
func (s *Server) handleDraftReply(w http.ResponseWriter, r *http.Request) {
	if s.briefHandler != nil {
		s.briefHandler.DraftReply(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "brief handler not configured")
}

// handleItemClaims handles GET /knowledge/{content_id}/claims (W6 stage-1 preview).
func (s *Server) handleItemClaims(w http.ResponseWriter, r *http.Request) {
	if s.briefHandler != nil {
		s.briefHandler.Claims(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "brief handler not configured")
}

// handleClaimContradictions handles GET /knowledge/claim-contradictions (W6 3c).
func (s *Server) handleClaimContradictions(w http.ResponseWriter, r *http.Request) {
	if s.briefHandler != nil {
		s.briefHandler.ClaimContradictions(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "brief handler not configured")
}

// handleDismissContradiction handles POST /knowledge/contradictions/dismiss —
// records (or undoes) a user's dismissal of a contradiction.
func (s *Server) handleDismissContradiction(w http.ResponseWriter, r *http.Request) {
	if s.briefHandler != nil {
		s.briefHandler.DismissContradiction(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "brief handler not configured")
}

// handleKnowledgeFollowupReport handles GET /knowledge/followup/report — the
// streamed natural-language report (SSE). Delegates to the BriefHandler.
func (s *Server) handleKnowledgeFollowupReport(w http.ResponseWriter, r *http.Request) {
	if s.briefHandler != nil {
		s.briefHandler.FollowUpReport(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte("data: {\"done\":true}\n\n"))
}

// SetConfigHandler attaches the config read/write handler.
func (s *Server) SetConfigHandler(handler *handlers.ConfigHandler) {
	s.configHandler = handler
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if s.configHandler != nil {
		s.configHandler.GetConfig(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "config handler not configured")
}

func (s *Server) handlePatchConfig(w http.ResponseWriter, r *http.Request) {
	if s.configHandler != nil {
		s.configHandler.PatchConfig(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "config handler not configured")
}

// SetUsageHandler attaches the token-usage / cost handler.
func (s *Server) SetUsageHandler(handler *handlers.UsageHandler) {
	s.usageHandler = handler
}

// SetBackupHandler attaches the DB backup/restore handler.
func (s *Server) SetBackupHandler(handler *handlers.BackupHandler) {
	s.backupHandler = handler
}

func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	if s.backupHandler != nil {
		s.backupHandler.Download(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "backup handler not configured")
}

func (s *Server) handleBackupSave(w http.ResponseWriter, r *http.Request) {
	if s.backupHandler != nil {
		s.backupHandler.SaveLocal(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "backup handler not configured")
}

func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	if s.backupHandler != nil {
		s.backupHandler.Restore(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "backup handler not configured")
}

// SetExportHandler attaches the encrypted data-export handler.
func (s *Server) SetExportHandler(handler *handlers.ExportHandler) {
	s.exportHandler = handler
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if s.exportHandler != nil {
		s.exportHandler.Export(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "export handler not configured")
}

// SetEncryptionHandler attaches the local at-rest encryption handler.
func (s *Server) SetEncryptionHandler(handler *handlers.EncryptionHandler) {
	s.encryptionHandler = handler
}

func (s *Server) handleEncryptionStatus(w http.ResponseWriter, r *http.Request) {
	if s.encryptionHandler != nil {
		s.encryptionHandler.Status(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "encryption handler not configured")
}

func (s *Server) handleEncryptionEnable(w http.ResponseWriter, r *http.Request) {
	if s.encryptionHandler != nil {
		s.encryptionHandler.Enable(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "encryption handler not configured")
}

func (s *Server) handleGetTokenUsage(w http.ResponseWriter, r *http.Request) {
	if s.usageHandler != nil {
		s.usageHandler.GetTokens(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "usage handler not configured")
}

func (s *Server) handleSetTokenPricing(w http.ResponseWriter, r *http.Request) {
	if s.usageHandler != nil {
		s.usageHandler.SetPricing(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "usage handler not configured")
}

// SetMentionsHandler attaches the mentions autocomplete handler.
func (s *Server) SetMentionsHandler(handler *handlers.MentionsHandler) {
	s.mentionsHandler = handler
}

// handleMentionsSearch handles GET /mentions.
func (s *Server) handleMentionsSearch(w http.ResponseWriter, r *http.Request) {
	if s.mentionsHandler != nil {
		s.mentionsHandler.Search(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "mentions handler not configured")
}

// handleKnowledgeUpload handles POST /knowledge/upload.
func (s *Server) handleKnowledgeUpload(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.Upload(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// SetInteractionsHandler attaches the interactions ingestion handler.
func (s *Server) SetInteractionsHandler(handler *handlers.InteractionsHandler) {
	s.interactionsHandler = handler
}

// handleInteractionsAppend handles POST /interactions.
func (s *Server) handleInteractionsAppend(w http.ResponseWriter, r *http.Request) {
	if s.interactionsHandler != nil {
		s.interactionsHandler.Append(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "interactions handler not configured")
}

// SetInsightsHandler attaches the learning-progress handler.
func (s *Server) SetInsightsHandler(handler *handlers.InsightsHandler) {
	s.insightsHandler = handler
}

// handleLearningProgress handles GET /insights/learning-progress.
func (s *Server) handleLearningProgress(w http.ResponseWriter, r *http.Request) {
	if s.insightsHandler != nil {
		s.insightsHandler.LearningProgress(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "insights handler not configured")
}
