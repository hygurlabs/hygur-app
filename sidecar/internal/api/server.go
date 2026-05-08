// Package api provides the HTTP API server for the Hygur sidecar.
package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/hygur/sidecar/internal/api/handlers"
	"github.com/hygur/sidecar/internal/config"
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
	mailHandler      *handlers.MailHandler
	searchHandler    *handlers.SearchHandler
	notesHandler     *handlers.NotesHandler
	tagHandler       *handlers.TagHandler
	graphHandler     *handlers.GraphHandler
	connectorHandler   *handlers.ConnectorHandler
	marketplaceHandler *handlers.MarketplaceHandler
	memoryHandler    *handlers.MemoryHandler
	eventsHandler    *handlers.EventsHandler
	briefHandler     *handlers.BriefHandler
	timelineHandler  *handlers.TimelineHandler
	agendaHandler    *handlers.AgendaHandler
	configHandler    *handlers.ConfigHandler
	interactionsHandler *handlers.InteractionsHandler
	insightsHandler  *handlers.InsightsHandler
	token            string // Authentication token for API access
}

// NewServer creates a new API server instance.
// The token parameter is used for authenticating API requests via the X-Hygur-Token header.
func NewServer(cfg *config.Config, logger zerolog.Logger, token string) *Server {
	s := &Server{
		cfg:    cfg,
		logger: logger.With().Str("component", "api").Logger(),
		router: chi.NewRouter(),
		token:  token,
		// Initialise the health handler eagerly with a nil LLM client so the
		// /health endpoint reports `version` + `lm_studio: disconnected` even
		// before SetLLMClient is called (e.g. during tests or boot warmup).
		healthHandler: handlers.NewHealthHandler(nil),
	}

	s.setupMiddleware()
	s.setupRoutes()

	return s
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

	// Custom zerolog logger middleware
	s.router.Use(s.loggerMiddleware)

	// Panic recovery with logging
	s.router.Use(s.recovererWithLogger)
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
