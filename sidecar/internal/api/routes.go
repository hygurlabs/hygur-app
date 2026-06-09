package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// setupRoutes configures all API endpoints.
func (s *Server) setupRoutes() {
	// Public routes (no authentication required)
	// Health endpoint uses a handler that's updated when SetLLMClient is called
	s.router.Get("/health", s.handleHealth)

	// API version — public so clients can detect version skew before auth.
	s.router.Get("/version", s.handleVersion)

	// Web UI — the embedded single-page client that replaces the SwiftUI views.
	// Public so it can bootstrap the API token into the page (see handleWebUI);
	// the loopback bind is the trust boundary.
	s.router.Get("/", s.handleWebUI)
	s.router.Get("/app", s.handleWebUI)
	// Content-hashed SPA bundle (JS/CSS). Public, same trust boundary as the
	// shell; served from the embedded build with long-lived immutable caching.
	s.router.Handle("/assets/*", webUIAssets())

	// Root static assets (favicon, home-screen icons, PWA manifest). Public;
	// requested at the site root by browsers and "Add to Home Screen".
	pub := webUIPublic()
	for _, p := range webUIPublicFiles {
		s.router.Handle(p, pub)
	}

	// Streaming routes (auth, no timeout) — SSE can take minutes for chat
	// with multiple LLM round-trips (tool calls + synthesis). Declared as a
	// separate group BEFORE the timeout-bearing group because chi's Group
	// inherits all parent middleware: once Timeout is applied, sub-Groups
	// cannot opt out.
	s.router.Group(func(r chi.Router) {
		r.Use(s.apiVersionMiddleware)
		r.Use(s.authMiddleware)
		r.Post("/chat", s.handleChat)
		r.Get("/events", s.handleEvents)
		// DB backup/restore — no request timeout (large downloads/uploads).
		r.Get("/admin/db/backup", s.handleBackupDownload)
		r.Post("/admin/db/backup/save", s.handleBackupSave)
		r.Post("/admin/db/restore", s.handleBackupRestore)
		// Encrypted data export (GDPR portability) — user-passphrase-encrypted zip.
		r.Post("/admin/export", s.handleExport)
		// Local at-rest encryption (opt-in; key in the OS keychain).
		r.Get("/admin/db/encryption", s.handleEncryptionStatus)
		r.Post("/admin/db/encrypt", s.handleEncryptionEnable)
	})

	// Protected routes (authentication required) with standard timeout
	s.router.Group(func(r chi.Router) {
		r.Use(s.apiVersionMiddleware)
		r.Use(s.authMiddleware)
		r.Use(middleware.Timeout(s.cfg.Server.ReadTimeout))

		// Model endpoints
		r.Get("/models", s.handleModels)

		// Edge (cloud thin client): on-device Proton folder listing + sync
		// status/trigger. Served LOCALLY (kept off the cloud proxy) because only
		// this device can reach the local Proton Bridge. 503 in non-thin-client.
		r.Get("/edge/status", s.handleEdgeStatus)
		r.Get("/edge/proton/mailboxes", s.handleEdgeMailboxes)
		r.Post("/edge/sync", s.handleEdgeSync)

		// Knowledge endpoints (fast operations)
		r.Route("/knowledge", func(r chi.Router) {
			r.Get("/items", s.handleKnowledgeList)
			r.Get("/diagnostic", s.handleKnowledgeDiagnostic)
			r.Get("/contradictions", s.handleKnowledgeContradictions)
			r.Get("/followup", s.handleKnowledgeFollowup)
			r.Get("/project-timeline", s.handleKnowledgeProjectTimeline)
			r.Get("/followup/report", s.handleKnowledgeFollowupReport)
			r.Post("/ingest", s.handleKnowledgeIngest)
			r.Post("/ingest-text", s.handleKnowledgeIngestText)
			r.Post("/ingest-folder", s.handleKnowledgeIngestFolder)
			r.Post("/upload", s.handleKnowledgeUpload)
			r.Post("/search", s.handleKnowledgeSearch)
			r.Post("/reembed-missing", s.handleKnowledgeReembedMissing)
			r.Post("/retag", s.handleKnowledgeRetag)
			r.Post("/backfill-claims", s.handleKnowledgeBackfillClaims)
			r.Delete("/reset", s.handleKnowledgeReset)
			r.Get("/{content_id}", s.handleKnowledgeGet)
			r.Delete("/{content_id}", s.handleKnowledgeDelete)
			// Tag operations on knowledge items
			r.Get("/{content_id}/tags", s.handleItemTags)
			r.Post("/{content_id}/tags", s.handleAddTagToItem)
			r.Delete("/{content_id}/tags/{tag_id}", s.handleRemoveTagFromItem)
			// Project link operations on knowledge items
			r.Post("/{content_id}/project", s.handleLinkProject)
			r.Delete("/{content_id}/project", s.handleUnlinkProject)
			// Dismiss the proactive project suggestion (W4).
			r.Delete("/{content_id}/project-suggestion", s.handleDismissProjectSuggestion)
			// On-demand grounded reply draft for a mail item (W7).
			r.Post("/{content_id}/draft-reply", s.handleDraftReply)
			r.Get("/{content_id}/claims", s.handleItemClaims)
		})

		// Tag endpoints
		r.Route("/tags", func(r chi.Router) {
			r.Get("/", s.handleTagList)
			r.Post("/", s.handleTagCreate)
			r.Post("/dedupe", s.handleTagDedupe)
			r.Get("/{id}", s.handleTagGet)
			r.Put("/{id}", s.handleTagUpdate)
			r.Delete("/{id}", s.handleTagDelete)
			r.Get("/{id}/items", s.handleTagListItems)
		})

		// Project endpoints
		r.Route("/projects", func(r chi.Router) {
			r.Get("/", s.handleProjectList)
			r.Post("/", s.handleProjectCreate)
			r.Get("/{id}", s.handleProjectGet)
			r.Put("/{id}", s.handleProjectUpdate)
			r.Delete("/{id}", s.handleProjectDelete)
			r.Get("/{id}/items", s.handleProjectListItems)
		})

		// Tasks — local to-do list (optionally linked to a project/item).
		r.Route("/tasks", func(r chi.Router) {
			r.Get("/", s.handleTaskList)
			r.Post("/", s.handleTaskCreate)
			r.Patch("/{id}", s.handleTaskPatch)
			r.Delete("/{id}", s.handleTaskDelete)
		})

		// Mail endpoints
		r.Route("/mail", func(r chi.Router) {
			r.Get("/sources", s.handleMailSources)
			r.Get("/mailboxes", s.handleMailMailboxes)
			r.Get("/labels", s.handleMailLabels)
			r.Get("/threads", s.handleMailThreads)
			r.Post("/threads/{thread_id}/index", s.handleMailIndex)
			r.Post("/threads/{thread_id}/summarize", s.handleMailSummarize)
			r.Get("/threads/{thread_id}/attachments", s.handleMailAttachments)

			// Multi-account endpoints
			r.Get("/accounts", s.handleMailAccounts)
			r.Post("/accounts/{account_id}/verify", s.handleMailAccountVerify)
			r.Get("/accounts/{account_id}/stats", s.handleMailAccountStats)
			r.Get("/accounts/{account_id}/labels", s.handleMailAccountLabels)
			r.Get("/accounts/{account_id}/mailboxes", s.handleMailAccountMailboxes)

			// Credential management endpoints
			r.Get("/credentials", s.handleMailCredentials)
			r.Delete("/credentials/{source}", s.handleMailDeleteCredential)
		})

		// Notes endpoints
		r.Route("/notes", func(r chi.Router) {
			r.Get("/", s.handleNotesList)
			r.Post("/", s.handleNotesCreate)
			r.Get("/{id}", s.handleNotesGet)
			r.Put("/{id}", s.handleNotesUpdate)
			r.Delete("/{id}", s.handleNotesDelete)
		})

		// Chat session transcripts (persistent history)
		r.Route("/sessions", func(r chi.Router) {
			r.Get("/", s.handleSessionsList)
			r.Get("/{id}", s.handleSessionGet)
			r.Put("/{id}", s.handleSessionUpdate)
			r.Delete("/{id}", s.handleSessionDelete)
		})

		// Tools endpoints (global search)
		r.Get("/tools/search", s.handleToolsSearch)

		// Mentions autocomplete — projects + notes/mails/documents for the
		// WebUI composer's "@" context picker.
		r.Get("/mentions", s.handleMentionsSearch)

		// Memory endpoints
		r.Route("/memory", func(r chi.Router) {
			r.Post("/store", s.handleMemoryStore)
			r.Get("/search", s.handleMemorySearch)
			r.Get("/sync", s.handleMemorySync)
			r.Get("/list", s.handleMemoryList)
			// Phase 3.3 long-term memory.
			r.Get("/stats", s.handleMemoryStats)
			r.Get("/pending", s.handleMemoryPending)
			r.Post("/extract", s.handleMemoryExtract)
			r.Delete("/extracted", s.handleMemoryClearExtracted)
			r.Post("/{memory_id}/accept", s.handleMemoryAccept)
			r.Post("/{memory_id}/discard", s.handleMemoryDiscard)
			r.Delete("/{memory_id}", s.handleMemoryDelete)
		})

		// Unified search endpoint
		r.Post("/search", s.handleUnifiedSearch)

		// Graph endpoint
		r.Get("/graph", s.handleGraph)

		// Timeline endpoint — chaptered chronological view of a topic.
		r.Post("/timeline/query", s.handleTimelineQuery)

		// Agenda context — upcoming deadlines and actions within a time window.
		r.Get("/agenda/context", s.handleAgendaContext)
		// Calendar summary — short LLM synthesis of upcoming events (header card).
		r.Get("/agenda/calendar-summary", s.handleCalendarSummary)
		// Calendar events by date window (ordered by date, not ingestion time).
		r.Get("/agenda/events", s.handleAgendaEvents)

		// On-demand brief — POST /brief/run with optional JSON body
		// {"project_id": "...", "lookback_hours": 24}. The brief runs
		// asynchronously; the result lands in /events as a `brief` event.
		r.Post("/brief/run", s.handleBriefRun)

		// Meeting briefing — POST /brief/meeting generates a RAG briefing for
		// one calendar event (the macOS app calls this ~30 min before).
		r.Post("/brief/meeting", s.handleBriefMeeting)

		// GET /briefings — unified list of daily briefs + meeting briefings.
		r.Get("/briefings", s.handleBriefingsList)

		// Config read/write — exposes the tunable sidecar config to the macOS app.
		// Changes are persisted to config.yaml and take effect on next restart.
		r.Get("/config", s.handleGetConfig)
		r.Patch("/config", s.handlePatchConfig)

		// Token usage + cost. Pricing lives in the DB, not config.yaml, so
		// saving it never restarts the sidecar.
		r.Get("/usage/tokens", s.handleGetTokenUsage)
		r.Put("/usage/pricing", s.handleSetTokenPricing)

		// Phase 1 (pair mode) — append-only interaction signals + learning gauge.
		r.Post("/interactions", s.handleInteractionsAppend)
		r.Get("/insights/learning-progress", s.handleLearningProgress)

		// Marketplace endpoints
		r.Route("/marketplace", func(r chi.Router) {
			r.Get("/connectors", s.handleMarketplaceList)
			r.Post("/install/{typeID}", s.handleMarketplaceInstall)
		})

		// Connector endpoints (fast operations)
		r.Route("/connectors", func(r chi.Router) {
			r.Get("/", s.handleConnectorList)
			// Instance endpoints (multi-compte)
			r.Get("/instances", s.handleConnectorListInstances)
			r.Delete("/instances/{instanceID}", s.handleConnectorDeleteInstance)
			r.Post("/{type}/instances", s.handleConnectorCreateInstance)
			// Single-connector endpoints
			r.Get("/{id}", s.handleConnectorGet)
			r.Put("/{id}/config", s.handleConnectorConfigure)
			r.Put("/{id}/credentials", s.handleConnectorSaveCredentials)
			r.Post("/{id}/enable", s.handleConnectorEnable)
			r.Post("/{id}/disable", s.handleConnectorDisable)
			r.Get("/{id}/health", s.handleConnectorHealth)
			r.Get("/{id}/auth/url", s.handleConnectorAuthURL)
			r.Post("/{id}/auth/callback", s.handleConnectorAuthCallback)
			r.Get("/{id}/mailboxes", s.handleConnectorMailboxes)
			r.Get("/{id}/labels", s.handleConnectorLabels)
			r.Post("/{id}/sync", s.handleConnectorSync)
		})
	})
}

// handleModels returns the list of available LLM models.
// It delegates to the ModelsHandler which lists models from LM Studio.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if s.modelsHandler != nil {
		s.modelsHandler.ServeHTTP(w, r)
		return
	}
	// Fallback if modelsHandler is not initialized (before SetLLMClient is called)
	writeError(w, http.StatusServiceUnavailable, "models handler not configured")
}

// handleChat handles chat completion requests with SSE streaming.
// If RAGChatHandler is configured, it delegates to it for RAG-enhanced chat.
// Otherwise, it falls back to the basic ChatHandler.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	// Prefer RAGChatHandler if available
	if s.ragChatHandler != nil {
		s.ragChatHandler.ServeHTTP(w, r)
		return
	}
	// Fall back to basic ChatHandler
	if s.chatHandler != nil {
		s.chatHandler.ServeHTTP(w, r)
		return
	}
	// Neither handler is initialized
	writeError(w, http.StatusServiceUnavailable, "chat handler not configured")
}

// ErrorResponse represents a standard error response.
type ErrorResponse struct {
	Error string `json:"error"`
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		// If encoding fails, the headers are already sent
		// Log the error but we can't change the response
		return
	}
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, ErrorResponse{Error: message})
}

// handleKnowledgeList handles GET /knowledge/items.
// It delegates to the KnowledgeHandler.
func (s *Server) handleKnowledgeList(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.List(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleKnowledgeIngest handles POST /knowledge/ingest.
// It delegates to the KnowledgeHandler.
func (s *Server) handleKnowledgeIngest(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.Ingest(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleKnowledgeIngestText handles POST /knowledge/ingest-text.
// It delegates to the KnowledgeHandler.
func (s *Server) handleKnowledgeIngestText(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.IngestText(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleKnowledgeIngestFolder handles POST /knowledge/ingest-folder.
// It delegates to the KnowledgeHandler.
func (s *Server) handleKnowledgeIngestFolder(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.IngestFolder(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleKnowledgeSearch handles POST /knowledge/search.
// It delegates to the KnowledgeHandler.
func (s *Server) handleKnowledgeSearch(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.Search(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleKnowledgeGet handles GET /knowledge/{content_id}.
// It delegates to the KnowledgeHandler.
func (s *Server) handleKnowledgeGet(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.Get(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleKnowledgeDelete handles DELETE /knowledge/{content_id}.
// It delegates to the KnowledgeHandler.
func (s *Server) handleKnowledgeDelete(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.Delete(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleKnowledgeReset handles DELETE /knowledge/reset.
// It delegates to the KnowledgeHandler.
func (s *Server) handleKnowledgeReset(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.Reset(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleKnowledgeReembedMissing handles POST /knowledge/reembed-missing.
func (s *Server) handleKnowledgeReembedMissing(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.ReembedMissing(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleKnowledgeRetag handles POST /knowledge/retag — backfill mail auto-tags.
func (s *Server) handleKnowledgeRetag(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.Retag(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleKnowledgeBackfillClaims handles POST /knowledge/backfill-claims (W6).
func (s *Server) handleKnowledgeBackfillClaims(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.BackfillClaims(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleKnowledgeDiagnostic handles GET /knowledge/diagnostic.
// It delegates to the KnowledgeHandler.
func (s *Server) handleKnowledgeDiagnostic(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.Diagnostic(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleKnowledgeReindex handles POST /knowledge/reindex.
// It delegates to the KnowledgeHandler.
func (s *Server) handleKnowledgeReindex(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.Reindex(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleProjectList handles GET /projects.
// It delegates to the ProjectHandler.
func (s *Server) handleProjectList(w http.ResponseWriter, r *http.Request) {
	if s.projectHandler != nil {
		s.projectHandler.List(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "project handler not configured")
}

// handleProjectCreate handles POST /projects.
// It delegates to the ProjectHandler.
func (s *Server) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	if s.projectHandler != nil {
		s.projectHandler.Create(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "project handler not configured")
}

// handleProjectGet handles GET /projects/{id}.
// It delegates to the ProjectHandler.
func (s *Server) handleProjectGet(w http.ResponseWriter, r *http.Request) {
	if s.projectHandler != nil {
		s.projectHandler.Get(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "project handler not configured")
}

// handleProjectUpdate handles PUT /projects/{id}.
// It delegates to the ProjectHandler.
func (s *Server) handleProjectUpdate(w http.ResponseWriter, r *http.Request) {
	if s.projectHandler != nil {
		s.projectHandler.Update(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "project handler not configured")
}

// handleProjectDelete handles DELETE /projects/{id}.
// It delegates to the ProjectHandler.
func (s *Server) handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	if s.projectHandler != nil {
		s.projectHandler.Delete(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "project handler not configured")
}

// handleProjectListItems handles GET /projects/{id}/items.
// It delegates to the ProjectHandler.
func (s *Server) handleProjectListItems(w http.ResponseWriter, r *http.Request) {
	if s.projectHandler != nil {
		s.projectHandler.ListItems(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "project handler not configured")
}

// handleMailSources handles GET /mail/sources.
// It delegates to the MailHandler.
func (s *Server) handleMailSources(w http.ResponseWriter, r *http.Request) {
	if s.mailHandler != nil {
		s.mailHandler.Sources(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "mail handler not configured")
}

// handleMailMailboxes handles GET /mail/mailboxes.
// It delegates to the MailHandler.
func (s *Server) handleMailMailboxes(w http.ResponseWriter, r *http.Request) {
	if s.mailHandler != nil {
		s.mailHandler.Mailboxes(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "mail handler not configured")
}

// handleMailLabels handles GET /mail/labels.
// It delegates to the MailHandler.
func (s *Server) handleMailLabels(w http.ResponseWriter, r *http.Request) {
	if s.mailHandler != nil {
		s.mailHandler.Labels(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "mail handler not configured")
}

// handleMailThreads handles GET /mail/threads.
// It delegates to the MailHandler.
func (s *Server) handleMailThreads(w http.ResponseWriter, r *http.Request) {
	if s.mailHandler != nil {
		s.mailHandler.Threads(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "mail handler not configured")
}

// handleMailIndex handles POST /mail/threads/{thread_id}/index.
// It delegates to the MailHandler.
func (s *Server) handleMailIndex(w http.ResponseWriter, r *http.Request) {
	if s.mailHandler != nil {
		s.mailHandler.Index(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "mail handler not configured")
}

// handleMailSummarize handles POST /mail/threads/{thread_id}/summarize.
// It delegates to the MailHandler.
func (s *Server) handleMailSummarize(w http.ResponseWriter, r *http.Request) {
	if s.mailHandler != nil {
		s.mailHandler.Summarize(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "mail handler not configured")
}

// handleMailAttachments handles GET /mail/threads/{thread_id}/attachments.
// It delegates to the MailHandler.
func (s *Server) handleMailAttachments(w http.ResponseWriter, r *http.Request) {
	if s.mailHandler != nil {
		s.mailHandler.Attachments(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "mail handler not configured")
}

// handleMailCredentials handles GET /mail/credentials.
// It delegates to the MailHandler to list saved credentials.
func (s *Server) handleMailCredentials(w http.ResponseWriter, r *http.Request) {
	if s.mailHandler != nil {
		s.mailHandler.Credentials(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "mail handler not configured")
}

// handleMailDeleteCredential handles DELETE /mail/credentials/{source}.
// It delegates to the MailHandler to delete a saved credential.
func (s *Server) handleMailDeleteCredential(w http.ResponseWriter, r *http.Request) {
	if s.mailHandler != nil {
		s.mailHandler.DeleteCredential(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "mail handler not configured")
}

// handleMailAccounts handles GET /mail/accounts.
func (s *Server) handleMailAccounts(w http.ResponseWriter, r *http.Request) {
	if s.mailHandler != nil {
		s.mailHandler.Accounts(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "mail handler not configured")
}

// handleMailAccountVerify handles POST /mail/accounts/{account_id}/verify.
func (s *Server) handleMailAccountVerify(w http.ResponseWriter, r *http.Request) {
	if s.mailHandler != nil {
		s.mailHandler.VerifyAccount(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "mail handler not configured")
}

// handleMailAccountStats handles GET /mail/accounts/{account_id}/stats.
func (s *Server) handleMailAccountStats(w http.ResponseWriter, r *http.Request) {
	if s.mailHandler != nil {
		s.mailHandler.AccountStats(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "mail handler not configured")
}

// handleToolsSearch handles GET /tools/search.
// It delegates to the SearchHandler for global search across all projects.
func (s *Server) handleToolsSearch(w http.ResponseWriter, r *http.Request) {
	if s.searchHandler != nil {
		s.searchHandler.Search(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "search handler not configured")
}

// handleNotesList handles GET /notes.
// It delegates to the NotesHandler to list all notes.
func (s *Server) handleNotesList(w http.ResponseWriter, r *http.Request) {
	if s.notesHandler != nil {
		s.notesHandler.List(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "notes handler not configured")
}

// handleNotesCreate handles POST /notes.
// It delegates to the NotesHandler to create a new note.
func (s *Server) handleNotesCreate(w http.ResponseWriter, r *http.Request) {
	if s.notesHandler != nil {
		s.notesHandler.Create(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "notes handler not configured")
}

// handleNotesGet handles GET /notes/{id}.
// It delegates to the NotesHandler to get a single note.
func (s *Server) handleNotesGet(w http.ResponseWriter, r *http.Request) {
	if s.notesHandler != nil {
		s.notesHandler.Get(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "notes handler not configured")
}

// handleNotesUpdate handles PUT /notes/{id}.
// It delegates to the NotesHandler to update an existing note.
func (s *Server) handleNotesUpdate(w http.ResponseWriter, r *http.Request) {
	if s.notesHandler != nil {
		s.notesHandler.Update(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "notes handler not configured")
}

// handleNotesDelete handles DELETE /notes/{id}.
// It delegates to the NotesHandler to delete a note.
func (s *Server) handleNotesDelete(w http.ResponseWriter, r *http.Request) {
	if s.notesHandler != nil {
		s.notesHandler.Delete(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "notes handler not configured")
}

// handleSessionsList handles GET /sessions.
func (s *Server) handleSessionsList(w http.ResponseWriter, r *http.Request) {
	if s.sessionsHandler != nil {
		s.sessionsHandler.List(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "sessions handler not configured")
}

// handleSessionGet handles GET /sessions/{id}.
func (s *Server) handleSessionGet(w http.ResponseWriter, r *http.Request) {
	if s.sessionsHandler != nil {
		s.sessionsHandler.Get(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "sessions handler not configured")
}

// handleSessionUpdate handles PUT /sessions/{id}.
func (s *Server) handleSessionUpdate(w http.ResponseWriter, r *http.Request) {
	if s.sessionsHandler != nil {
		s.sessionsHandler.Update(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "sessions handler not configured")
}

// handleSessionDelete handles DELETE /sessions/{id}.
func (s *Server) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	if s.sessionsHandler != nil {
		s.sessionsHandler.Delete(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "sessions handler not configured")
}

// handleUnifiedSearch handles POST /search.
// It delegates to the SearchHandler for unified search across knowledge and mail.
func (s *Server) handleUnifiedSearch(w http.ResponseWriter, r *http.Request) {
	if s.searchHandler != nil {
		s.searchHandler.UnifiedSearch(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "search handler not configured")
}

// handleTagList handles GET /tags.
// It delegates to the TagHandler.
func (s *Server) handleTagList(w http.ResponseWriter, r *http.Request) {
	if s.tagHandler != nil {
		s.tagHandler.List(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "tag handler not configured")
}

// handleTagCreate handles POST /tags.
// It delegates to the TagHandler.
func (s *Server) handleTagCreate(w http.ResponseWriter, r *http.Request) {
	if s.tagHandler != nil {
		s.tagHandler.Create(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "tag handler not configured")
}

// handleTagGet handles GET /tags/{id}.
// It delegates to the TagHandler.
func (s *Server) handleTagGet(w http.ResponseWriter, r *http.Request) {
	if s.tagHandler != nil {
		s.tagHandler.Get(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "tag handler not configured")
}

// handleTagUpdate handles PUT /tags/{id}.
// It delegates to the TagHandler.
func (s *Server) handleTagUpdate(w http.ResponseWriter, r *http.Request) {
	if s.tagHandler != nil {
		s.tagHandler.Update(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "tag handler not configured")
}

// handleTagDelete handles DELETE /tags/{id}.
// It delegates to the TagHandler.
func (s *Server) handleTagDelete(w http.ResponseWriter, r *http.Request) {
	if s.tagHandler != nil {
		s.tagHandler.Delete(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "tag handler not configured")
}

// handleItemTags handles GET /knowledge/{content_id}/tags.
// It delegates to the TagHandler.
func (s *Server) handleItemTags(w http.ResponseWriter, r *http.Request) {
	if s.tagHandler != nil {
		s.tagHandler.GetItemTags(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "tag handler not configured")
}

// handleAddTagToItem handles POST /knowledge/{content_id}/tags.
// It delegates to the TagHandler.
func (s *Server) handleAddTagToItem(w http.ResponseWriter, r *http.Request) {
	if s.tagHandler != nil {
		s.tagHandler.AddTagToItem(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "tag handler not configured")
}

// handleRemoveTagFromItem handles DELETE /knowledge/{content_id}/tags/{tag_id}.
// It delegates to the TagHandler.
func (s *Server) handleRemoveTagFromItem(w http.ResponseWriter, r *http.Request) {
	if s.tagHandler != nil {
		s.tagHandler.RemoveTagFromItem(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "tag handler not configured")
}

// handleLinkProject handles POST /knowledge/{content_id}/project.
// It delegates to the KnowledgeHandler.
func (s *Server) handleLinkProject(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.LinkProject(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleUnlinkProject handles DELETE /knowledge/{content_id}/project.
// It delegates to the KnowledgeHandler.
func (s *Server) handleUnlinkProject(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.UnlinkProject(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleDismissProjectSuggestion handles DELETE /knowledge/{content_id}/project-suggestion.
func (s *Server) handleDismissProjectSuggestion(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.DismissProjectSuggestion(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleKnowledgeContradictions handles GET /knowledge/contradictions.
// It delegates to the KnowledgeHandler.
func (s *Server) handleKnowledgeContradictions(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.Contradictions(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleKnowledgeProjectTimeline handles GET /knowledge/project-timeline.
// It delegates to the KnowledgeHandler.
func (s *Server) handleKnowledgeProjectTimeline(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeHandler != nil {
		s.knowledgeHandler.ProjectTimeline(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "knowledge handler not configured")
}

// handleGraph handles GET /graph.
// It delegates to the GraphHandler.
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	if s.graphHandler != nil {
		s.graphHandler.Get(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "graph handler not configured")
}

// handleTagListItems handles GET /tags/{id}/items.
// It delegates to the TagHandler.
func (s *Server) handleTagListItems(w http.ResponseWriter, r *http.Request) {
	if s.tagHandler != nil {
		s.tagHandler.ListItems(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "tag handler not configured")
}

// handleTagDedupe handles POST /tags/dedupe.
// It delegates to the TagHandler to merge tags with identical normalized names.
func (s *Server) handleTagDedupe(w http.ResponseWriter, r *http.Request) {
	if s.tagHandler != nil {
		s.tagHandler.Dedupe(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "tag handler not configured")
}

// handleConnectorList handles GET /connectors.
func (s *Server) handleConnectorList(w http.ResponseWriter, r *http.Request) {
	if s.connectorHandler != nil {
		s.connectorHandler.List(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "connector handler not configured")
}

// handleConnectorGet handles GET /connectors/{id}.
func (s *Server) handleConnectorGet(w http.ResponseWriter, r *http.Request) {
	if s.connectorHandler != nil {
		s.connectorHandler.Get(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "connector handler not configured")
}

// handleConnectorConfigure handles PUT /connectors/{id}/config.
func (s *Server) handleConnectorConfigure(w http.ResponseWriter, r *http.Request) {
	if s.connectorHandler != nil {
		s.connectorHandler.Configure(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "connector handler not configured")
}

// handleConnectorSaveCredentials handles PUT /connectors/{id}/credentials.
func (s *Server) handleConnectorSaveCredentials(w http.ResponseWriter, r *http.Request) {
	if s.connectorHandler != nil {
		s.connectorHandler.SaveCredentials(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "connector handler not configured")
}

// handleConnectorEnable handles POST /connectors/{id}/enable.
func (s *Server) handleConnectorEnable(w http.ResponseWriter, r *http.Request) {
	if s.connectorHandler != nil {
		s.connectorHandler.Enable(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "connector handler not configured")
}

// handleConnectorDisable handles POST /connectors/{id}/disable.
func (s *Server) handleConnectorDisable(w http.ResponseWriter, r *http.Request) {
	if s.connectorHandler != nil {
		s.connectorHandler.Disable(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "connector handler not configured")
}

// handleConnectorHealth handles GET /connectors/{id}/health.
func (s *Server) handleConnectorHealth(w http.ResponseWriter, r *http.Request) {
	if s.connectorHandler != nil {
		s.connectorHandler.Health(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "connector handler not configured")
}

// handleConnectorAuthURL handles GET /connectors/{id}/auth/url.
func (s *Server) handleConnectorAuthURL(w http.ResponseWriter, r *http.Request) {
	if s.connectorHandler != nil {
		s.connectorHandler.AuthURL(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "connector handler not configured")
}

// handleConnectorAuthCallback handles POST /connectors/{id}/auth/callback.
func (s *Server) handleConnectorAuthCallback(w http.ResponseWriter, r *http.Request) {
	if s.connectorHandler != nil {
		s.connectorHandler.AuthCallback(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "connector handler not configured")
}

// handleConnectorSync handles POST /connectors/{id}/sync.
func (s *Server) handleConnectorSync(w http.ResponseWriter, r *http.Request) {
	if s.connectorHandler != nil {
		s.connectorHandler.Sync(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "connector handler not configured")
}

// handleHealth returns the server health status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if s.healthHandler != nil {
		s.healthHandler.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// handleConnectorMailboxes handles GET /connectors/{id}/mailboxes.
func (s *Server) handleConnectorMailboxes(w http.ResponseWriter, r *http.Request) {
	if s.connectorHandler != nil {
		s.connectorHandler.Mailboxes(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "connector handler not configured")
}

// handleConnectorLabels handles GET /connectors/{id}/labels.
func (s *Server) handleConnectorLabels(w http.ResponseWriter, r *http.Request) {
	if s.connectorHandler != nil {
		s.connectorHandler.Labels(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "connector handler not configured")
}

// handleConnectorListInstances handles GET /connectors/instances.
func (s *Server) handleConnectorListInstances(w http.ResponseWriter, r *http.Request) {
	if s.connectorHandler != nil {
		s.connectorHandler.ListInstances(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "connector handler not configured")
}

// handleConnectorCreateInstance handles POST /connectors/{type}/instances.
func (s *Server) handleConnectorCreateInstance(w http.ResponseWriter, r *http.Request) {
	if s.connectorHandler != nil {
		s.connectorHandler.CreateInstance(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "connector handler not configured")
}

// handleConnectorDeleteInstance handles DELETE /connectors/instances/{instanceID}.
func (s *Server) handleConnectorDeleteInstance(w http.ResponseWriter, r *http.Request) {
	if s.connectorHandler != nil {
		s.connectorHandler.DeleteInstance(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "connector handler not configured")
}

// handleMarketplaceList handles GET /marketplace/connectors.
func (s *Server) handleMarketplaceList(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceHandler != nil {
		s.marketplaceHandler.List(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "marketplace handler not configured")
}

// handleMarketplaceInstall handles POST /marketplace/install/{typeID}.
func (s *Server) handleMarketplaceInstall(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceHandler != nil {
		s.marketplaceHandler.Install(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "marketplace handler not configured")
}

// handleMailAccountLabels handles GET /mail/accounts/{account_id}/labels.
func (s *Server) handleMailAccountLabels(w http.ResponseWriter, r *http.Request) {
	if s.mailHandler != nil {
		s.mailHandler.AccountLabels(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "mail handler not configured")
}

// handleMailAccountMailboxes handles GET /mail/accounts/{account_id}/mailboxes.
func (s *Server) handleMailAccountMailboxes(w http.ResponseWriter, r *http.Request) {
	if s.mailHandler != nil {
		s.mailHandler.AccountMailboxes(w, r)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "mail handler not configured")
}
