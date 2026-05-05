// Package handlers provides HTTP handlers for the Hygur API.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hygur/sidecar/internal/agenda"
	"github.com/hygur/sidecar/internal/intent"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/session"
	"github.com/hygur/sidecar/internal/store"
	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
)

// RAGChatRequest extends ChatRequest with RAG-specific options.
type RAGChatRequest struct {
	Messages    []llm.Message `json:"messages"`
	Model       string        `json:"model,omitempty"`
	Stream      bool          `json:"stream"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`

	// RAG-specific fields
	RAGEnabled   *bool    `json:"rag_enabled,omitempty"`   // Override default RAG behavior
	RAGTopK      int      `json:"rag_top_k,omitempty"`     // Override default top_k
	RAGSources   []string `json:"rag_sources,omitempty"`   // Override source detection
	AlwaysSearch bool     `json:"always_search,omitempty"` // Force search even for simple queries
	// MailAccountID scopes mail retrieval to a single account.
	MailAccountID string `json:"mail_account_id,omitempty"`
	// MailLabels scopes mail retrieval to threads with any of these Gmail label IDs.
	MailLabels []string `json:"mail_labels,omitempty"`
	// RecentSourceIDs contains content_ids cited in the last 1-2 assistant turns.
	// These items receive a soft boost in retrieval to reinforce conversational continuity.
	RecentSourceIDs []string `json:"recent_source_ids,omitempty"`

	// FocusScope, when set, restricts retrieval to documents linked to any of
	// the listed projects OR tagged with any of the listed tags. Drives the
	// "Mode Focus" UX: a project selected in the sidebar narrows chat answers
	// to that project's corpus.
	FocusScope *retrieval.FocusScope `json:"focus_scope,omitempty"`

	// SessionID identifies the in-memory session_context accumulator (entities,
	// resolved queries, active topic). When non-empty, the handler will:
	//   1) Try to satisfy entity-type follow-ups directly from the context
	//      (e.g. "and the IBAN?" after IBAN was already produced).
	//   2) Extract entities from search results and from the assistant's answer,
	//      merging them into the context for use on subsequent turns.
	// Empty means a transient (per-request) context — no caching across turns.
	SessionID string `json:"session_id,omitempty"`
}

// RAGSource represents a single source used for RAG context.
type RAGSource struct {
	ContentID  string  `json:"content_id"`
	SourceType string  `json:"source_type"` // "file", "note", "mail", etc.
	Title      string  `json:"title"`
	Excerpt    string  `json:"excerpt"`
	Score      float64 `json:"score"`
	// Mail-specific fields
	MailFrom    string `json:"mail_from,omitempty"`
	MailDate    string `json:"mail_date,omitempty"`
	MailSubject string `json:"mail_subject,omitempty"`
}

// RAGContext holds the retrieved context for a RAG request.
type RAGContext struct {
	Query      string                     `json:"query"`
	Intent     *IntentDTO                 `json:"intent,omitempty"`
	Sources    []RAGSource                `json:"sources"`
	TotalChars int                        `json:"total_chars"`
	Debug      *retrieval.SearchDebugInfo `json:"debug,omitempty"`
	// Rewritten holds the standalone-query rewrite when one was produced —
	// surfaced in the debug SSE event so users can see what got embedded.
	Rewritten string `json:"rewritten,omitempty"`
}

// RAGContextEvent represents the SSE event sent before streaming the LLM response.
type RAGContextEvent struct {
	Type    string      `json:"type"` // "rag_context"
	Sources []RAGSource `json:"sources"`
	Intent  *IntentDTO  `json:"intent,omitempty"`
}

// RAGChatHandler handles the /chat endpoint with RAG enhancement.
type RAGChatHandler struct {
	llmClient       *llm.Client
	unifiedSearcher *retrieval.UnifiedSearcher
	sessionStore    *session.Store
	memoryStore     *tools.MemoryStoreTool
	memorySearch    *tools.MemorySearchTool
	agendaExtractor *agenda.Extractor
	agendaStore     *store.DB
	config          RAGConfig
	logger          zerolog.Logger
}

// NewRAGChatHandler creates a new RAGChatHandler. sessionStore may be nil to
// disable in-memory session context (the handler then behaves like before).
func NewRAGChatHandler(
	llmClient *llm.Client,
	unifiedSearcher *retrieval.UnifiedSearcher,
	sessionStore *session.Store,
	config RAGConfig,
	logger zerolog.Logger,
) *RAGChatHandler {
	return &RAGChatHandler{
		llmClient:       llmClient,
		unifiedSearcher: unifiedSearcher,
		sessionStore:    sessionStore,
		config:          config.Validate(),
		logger:          logger.With().Str("handler", "rag_chat").Logger(),
	}
}

// SetMemoryTools wires the persistent-memory subsystem into the handler.
// Both arguments must be non-nil to enable memory injection AND auto-extraction;
// passing nil keeps the handler behaving exactly as before.
func (h *RAGChatHandler) SetMemoryTools(storeTool *tools.MemoryStoreTool, searchTool *tools.MemorySearchTool) {
	h.memoryStore = storeTool
	h.memorySearch = searchTool
}

// SetAgendaExtractor wires the agenda subsystem into the RAG chat handler so
// that upcoming deadlines are injected into the system prompt on each turn.
// Both arguments must be non-nil; passing nil disables the feature.
func (h *RAGChatHandler) SetAgendaExtractor(ext *agenda.Extractor, db *store.DB) {
	h.agendaExtractor = ext
	h.agendaStore = db
}

// injectAgendaIntoSystemPrompt prepends an urgency block to the system prompt
// listing the actions that are due in the next 48 h.
func injectAgendaIntoSystemPrompt(prompt string, actions []agenda.AgendaAction) string {
	if len(actions) == 0 {
		return prompt
	}
	var b strings.Builder
	b.WriteString("Voici les actions urgentes des prochaines 48h :\n")
	for _, a := range actions {
		b.WriteString(fmt.Sprintf("- [%s] %s (deadline : %s)\n", a.Priority, a.What, a.DeadlineISO))
	}
	b.WriteString("\n")
	return b.String() + prompt
}

// injectAgendaIntoMessages injects an agenda urgency block into the messages
// list, augmenting the system message when one exists or prepending a new one.
func injectAgendaIntoMessages(messages []llm.Message, actions []agenda.AgendaAction) []llm.Message {
	if len(actions) == 0 {
		return messages
	}
	var b strings.Builder
	b.WriteString("## Actions urgentes (prochaines 48h)\n\n")
	for _, a := range actions {
		b.WriteString(fmt.Sprintf("- [%s] %s (deadline : %s)\n", a.Priority, a.What, a.DeadlineISO))
	}
	agendaBlock := b.String()

	out := make([]llm.Message, 0, len(messages)+1)
	if len(messages) > 0 && messages[0].Role == "system" {
		out = append(out, llm.Message{
			Role:    "system",
			Content: agendaBlock + "\n" + messages[0].Content,
		})
		out = append(out, messages[1:]...)
	} else {
		out = append(out, llm.Message{Role: "system", Content: agendaBlock})
		out = append(out, messages...)
	}
	return out
}

// ServeHTTP implements http.Handler for the RAG chat endpoint.
func (h *RAGChatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		writeChatError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only POST method is allowed")
		return
	}

	// Parse the request body
	var req RAGChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Debug().Err(err).Msg("failed to parse request body")
		writeChatError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	// Validate request - at least one message is required
	if len(req.Messages) == 0 {
		writeChatError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "messages required")
		return
	}

	// Check if LLM client is available
	if h.llmClient == nil {
		writeChatError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "LLM client not configured")
		return
	}

	// Determine if RAG is enabled for this request
	ragEnabled := h.config.Enabled
	if req.RAGEnabled != nil {
		ragEnabled = *req.RAGEnabled
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	// Get the Flusher interface for streaming
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.logger.Error().Msg("ResponseWriter does not support Flusher interface")
		writeSSEError(w, "INTERNAL_ERROR", "Streaming not supported")
		return
	}

	// Resolve the latest user message — needed both for direct-answer and for
	// post-synthesis session updates.
	latestUserQuery := lastUserMessage(req.Messages)

	// Load session context (or a transient one when SessionID is empty).
	var sessionCtx *session.SessionContext
	if h.sessionStore != nil {
		sessionCtx = h.sessionStore.Get(req.SessionID)
	} else {
		sessionCtx = &session.SessionContext{}
	}

	// Direct-source fast-path. When the latest user query is a tight
	// entity-type follow-up (e.g. "et la communication ?") and we already
	// know that entity from a prior turn in this session, skip the slow
	// retrieval pipeline (vector search + query rewrite, both bottlenecked
	// by the LLM) and instead pre-fetch the original source emails by
	// content_id. The LLM still generates the answer naturally, grounded
	// on those sources — but in seconds rather than minutes.
	directRetrievalUsed := false
	var prefetchedContext *RAGContext
	if direct, ok := session.CanAnswerFromContext(latestUserQuery, sessionCtx); ok && h.unifiedSearcher != nil && len(direct.SourceIDs) > 0 {
		results, err := h.unifiedSearcher.FetchByContentIDs(r.Context(), direct.SourceIDs)
		if err != nil {
			h.logger.Warn().Err(err).Msg("direct fetch failed, falling back to normal retrieval")
		} else if len(results) > 0 {
			prefetchedContext = buildRAGContextFromResults(latestUserQuery, results)
			directRetrievalUsed = true
			h.logger.Info().
				Str("session_id", req.SessionID).
				Str("entity_type", direct.EntityType).
				Int("sources", len(results)).
				Msg("direct retrieval — skipping vector search + rewrite")
		}
	}

	// Prepare messages for LLM
	messages := req.Messages

	// If RAG is enabled, retrieve context and augment messages.
	if ragEnabled && h.unifiedSearcher != nil {
		var ragContext *RAGContext
		var retrieveErr error
		if directRetrievalUsed {
			ragContext = prefetchedContext
		} else {
			ragContext, retrieveErr = h.retrieveContext(r, req)
		}
		switch {
		case retrieveErr != nil:
			h.logger.Warn().Err(retrieveErr).Msg("failed to retrieve RAG context, continuing without")
		case ragContext != nil && len(ragContext.Sources) > 0:
			// Merge entities extracted from retrieved sources into the session
			// so that subsequent direct-source attempts have something to work with.
			mergeSourcesIntoSession(sessionCtx, ragContext.Sources)

			// Debug SSE event when requested via ?debug=1 or X-Hygur-Debug.
			debugRequested := r.URL.Query().Get("debug") == "1" || r.Header.Get("X-Hygur-Debug") == "1"
			if debugRequested {
				sessionMode := "context_injected"
				if directRetrievalUsed {
					sessionMode = "direct_retrieval"
				}
				dbg := buildDebugEvent(ragContext, sessionMode)
				if err := h.writeSSEEvent(w, flusher, dbg); err != nil {
					h.logger.Debug().Err(err).Msg("failed to write debug event")
				}
			}

			contextEvent := RAGContextEvent{
				Type:    "rag_context",
				Sources: ragContext.Sources,
				Intent:  ragContext.Intent,
			}
			if err := h.writeSSEEvent(w, flusher, contextEvent); err != nil {
				h.logger.Debug().Err(err).Msg("failed to write rag_context event")
				return
			}

			messages = h.buildMessagesWithContext(req.Messages, ragContext)
		case ragContext != nil && ragContext.Intent != nil:
			// Search was attempted but found no results - inform the LLM.
			h.logger.Debug().
				Str("query", ragContext.Query).
				Float64("confidence", ragContext.Intent.Confidence).
				Msg("RAG search returned no results")

			contextEvent := RAGContextEvent{
				Type:    "rag_context",
				Sources: []RAGSource{},
				Intent:  ragContext.Intent,
			}
			if err := h.writeSSEEvent(w, flusher, contextEvent); err != nil {
				h.logger.Debug().Err(err).Msg("failed to write empty rag_context event")
			}

			messages = h.buildNoResultsMessage(req.Messages, ragContext)
		}
	}

	// Agenda injection: prepend upcoming deadlines to the system prompt so the
	// LLM can reference them proactively. Best-effort — any error is silently
	// swallowed to avoid breaking chat.
	if h.agendaExtractor != nil && h.agendaStore != nil {
		if recentItems, err := h.agendaStore.ListRecentItems(r.Context(), 48); err == nil {
			if actions, err := h.agendaExtractor.ExtractActions(r.Context(), recentItems); err == nil && len(actions) > 0 {
				messages = injectAgendaIntoMessages(messages, actions)
			}
		}
	}

	// Persistent-memory injection: prepend any durable user facts that match
	// the current query so the LLM has them available even when the RAG
	// pipeline didn't surface them. Best-effort — failure here must not
	// break chat.
	if h.memorySearch != nil && latestUserQuery != "" {
		if hits, err := h.memorySearch.Search(latestUserQuery, 3, 0); err == nil && len(hits) > 0 {
			messages = injectMemoriesIntoSystem(messages, hits)
		}
	}

	// Build the LLM request
	llmReq := llm.ChatRequest{
		Model:       req.Model,
		Messages:    messages,
		Stream:      true,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}

	// During the LLM's prefill phase (loading the full context into KV cache)
	// no tokens are sent, leaving the SSE connection silent for potentially
	// tens of seconds. URLSession and browser EventSource implementations
	// time out on idle connections. Send an SSE comment every 20 s to keep
	// the connection alive; comments are ignored by clients but reset their
	// idle timers. A mutex serialises writes between this goroutine and the
	// StreamChat callback below.
	var writeMu sync.Mutex
	stopKeepalive := make(chan struct{})
	var stopOnce sync.Once

	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-stopKeepalive:
				return
			case <-ticker.C:
				writeMu.Lock()
				_, _ = fmt.Fprintf(w, ": ping\n\n")
				flusher.Flush()
				writeMu.Unlock()
			}
		}
	}()

	// Accumulate assistant deltas so we can extract entities from the full
	// answer once streaming completes. Bounded by Go's slice growth — a
	// typical assistant turn is < 4 KB, well under any worry threshold.
	var assistantBuf strings.Builder

	// Stream from LLM
	err := h.llmClient.StreamChat(r.Context(), llmReq, func(delta string, done bool, usage *llm.Usage) error {
		// Stop keepalive as soon as the first token arrives.
		stopOnce.Do(func() { close(stopKeepalive) })

		// Check if client disconnected
		select {
		case <-r.Context().Done():
			h.logger.Debug().Msg("client disconnected during stream")
			return r.Context().Err()
		default:
		}

		if !done {
			assistantBuf.WriteString(delta)
		}

		var event map[string]any
		if done {
			event = map[string]any{
				"done": true,
			}
			if usage != nil {
				event["usage"] = map[string]int{
					"prompt_tokens":     usage.PromptTokens,
					"completion_tokens": usage.CompletionTokens,
					"total_tokens":      usage.TotalTokens,
				}
			}
		} else {
			event = map[string]any{
				"delta": delta,
				"done":  false,
			}
		}

		data, err := json.Marshal(event)
		if err != nil {
			h.logger.Error().Err(err).Msg("failed to marshal SSE event")
			return err
		}

		writeMu.Lock()
		_, writeErr := fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		writeMu.Unlock()
		if writeErr != nil {
			h.logger.Debug().Err(writeErr).Msg("failed to write SSE event")
			return writeErr
		}
		return nil
	})

	// Ensure the keepalive goroutine exits even if StreamChat returned an error
	// before any token was received.
	stopOnce.Do(func() { close(stopKeepalive) })

	if err != nil {
		// Check if it's a client disconnect - don't log as error
		if r.Context().Err() != nil {
			h.logger.Debug().Msg("stream ended due to client disconnect")
			return
		}

		// Log the error
		h.logger.Error().Err(err).Msg("chat stream error")

		// Send error as SSE event (mid-stream error)
		writeSSEError(w, "LLM_STUDIO_ERROR", err.Error())
		flusher.Flush()
	}

	// Post-stream: extract entities from the assistant answer and append a
	// ResolvedQuery so the next turn's direct-answer check has fresh context.
	// Skip when the session is transient (no SessionID) or the answer is empty.
	if req.SessionID != "" && assistantBuf.Len() > 0 {
		updateSessionPostSynthesis(sessionCtx, latestUserQuery, assistantBuf.String(), req.RecentSourceIDs)
	}

	// Fire-and-forget memory extraction. The extractor calls the LLM (1-3 s),
	// so detach from the request context — we don't want to block returning
	// to the client and we also want extraction to survive the client
	// disconnecting once the stream ends. ContextID = SessionID when present
	// so memories can later be traced back to the conversation that produced
	// them.
	if h.memoryStore != nil && assistantBuf.Len() > 0 && latestUserQuery != "" {
		userMsg := latestUserQuery
		assistantMsg := assistantBuf.String()
		ctxID := req.SessionID
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			extracted, err := h.memoryStore.ExtractMemoriesFromTurn(ctx, userMsg, assistantMsg)
			if err != nil {
				h.logger.Debug().Err(err).Msg("memory extraction failed")
				return
			}
			if len(extracted) == 0 {
				return
			}
			stored, persistErr := h.memoryStore.PersistExtracted(extracted, ctxID)
			evt := h.logger.Info()
			if persistErr != nil {
				evt = h.logger.Warn().Err(persistErr)
			}
			evt.Int("extracted", len(extracted)).Int("stored", stored).Msg("memories persisted from turn")
		}()
	}
}

// injectMemoriesIntoSystem prepends a "Faits durables" block to the system
// prompt so the LLM can reference durable user facts even when retrieval
// didn't surface them. If a system message already exists, the block is
// appended to its content; otherwise a fresh system message is inserted.
func injectMemoriesIntoSystem(messages []llm.Message, memories []tools.MemoryResult) []llm.Message {
	if len(memories) == 0 {
		return messages
	}
	var b strings.Builder
	b.WriteString("## Faits durables connus sur l'utilisateur\n\n")
	for _, m := range memories {
		b.WriteString("- ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	memBlock := b.String()

	hasSystem := len(messages) > 0 && messages[0].Role == "system"
	out := make([]llm.Message, 0, len(messages)+1)
	if hasSystem {
		out = append(out, llm.Message{
			Role:    "system",
			Content: messages[0].Content + "\n\n" + memBlock,
		})
		out = append(out, messages[1:]...)
	} else {
		out = append(out, llm.Message{Role: "system", Content: memBlock})
		out = append(out, messages...)
	}
	return out
}

// retrieveContext retrieves relevant context from the knowledge base and mail.
func (h *RAGChatHandler) retrieveContext(r *http.Request, req RAGChatRequest) (*RAGContext, error) {
	// Get the last user message for context retrieval
	var userQuery string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			userQuery = req.Messages[i].Content
			break
		}
	}

	if userQuery == "" {
		return &RAGContext{}, nil
	}

	// Multi-turn: rewrite the latest question as a standalone query using history.
	// This ensures follow-up questions like "and the IBAN?" carry forward the topic
	// ("TVA précompte") without requiring the user to repeat it.
	searchQuery := userQuery
	if len(req.Messages) > 1 && h.llmClient != nil {
		history := req.Messages[:len(req.Messages)-1]
		if rewritten, err := retrieval.RewriteStandaloneQuery(r.Context(), h.llmClient, history, userQuery); err != nil {
			h.logger.Warn().Err(err).Str("original", userQuery).Msg("query rewrite failed, falling back to verbatim")
		} else {
			searchQuery = rewritten
		}
	}
	h.logger.Info().
		Str("original", userQuery).
		Str("rewritten", searchQuery).
		Bool("rewrite_triggered", searchQuery != userQuery).
		Msg("RAG query")

	// Determine top_k
	topK := h.config.TopK
	if req.RAGTopK > 0 {
		topK = req.RAGTopK
	}

	// Convert source strings to intent.SourceType
	var sources []intent.SourceType
	for _, s := range req.RAGSources {
		switch s {
		case "knowledge":
			sources = append(sources, intent.SourceKnowledge)
		case "mail":
			sources = append(sources, intent.SourceMail)
		case "all":
			sources = append(sources, intent.SourceAll)
		}
	}

	// Build search request
	debugRequested := r.URL.Query().Get("debug") == "1" || r.Header.Get("X-Hygur-Debug") == "1"
	searchReq := retrieval.UnifiedSearchRequest{
		Query:                  searchQuery,
		TopK:                   topK,
		Sources:                sources,
		MailAccountID:          req.MailAccountID,
		MailLabels:             req.MailLabels,
		PriorSourceBoost:       req.RecentSourceIDs,
		FocusScope:             req.FocusScope,
		ScoringMode:            h.config.TemporalScoringMode,
		CurrentStateFilterDays: h.config.CurrentStateFilterDays,
		Debug:                  debugRequested,
	}

	// Perform search
	result, err := h.unifiedSearcher.Search(r.Context(), searchReq)
	if err != nil {
		return nil, fmt.Errorf("unified search failed: %w", err)
	}

	// Re-rank results using LLM if we have enough results
	if len(result.Results) > 0 && h.config.ReRankEnabled {
		h.logger.Debug().Int("count", len(result.Results)).Msg("reranking results")
		if orderedIDs, err := h.unifiedSearcher.Rerank(r.Context(), userQuery, result.Results); err != nil {
			h.logger.Warn().Err(err).Msg("failed to re-rank, using original order")
		} else if len(orderedIDs) > 0 {
			// Re-order results by the LLM-returned content IDs
			result.Results = retrieval.ReOrderBy(result.Results, orderedIDs)
		}
	}

	// Convert results to RAGSources, filtering by confidence
	var ragSources []RAGSource
	var totalChars int

	for _, r := range result.Results {
		// Filter by minimum confidence
		if r.Score < h.config.MinConfidence {
			continue
		}

		// Check if we're exceeding max context tokens (rough estimate: 4 chars per token)
		excerptChars := len(r.Excerpt)
		if totalChars+excerptChars > h.config.MaxContextTokens*4 {
			break
		}

		ragSources = append(ragSources, RAGSource{
			ContentID:   r.ContentID,
			SourceType:  r.SourceType,
			Title:       r.Title,
			Excerpt:     r.Excerpt,
			Score:       r.Score,
			MailFrom:    r.MailFrom,
			MailDate:    r.MailDate,
			MailSubject: r.MailSubject,
		})
		totalChars += excerptChars
	}

	// Convert intent to DTO if present
	var intentDTO *IntentDTO
	if result.Intent != nil {
		intentSources := make([]string, len(result.Intent.Sources))
		for i, s := range result.Intent.Sources {
			intentSources[i] = string(s)
		}
		intentWeights := make(map[string]float64)
		for k, v := range result.Intent.Weights {
			intentWeights[string(k)] = v
		}
		intentDTO = &IntentDTO{
			Query:          result.Intent.Query,
			Sources:        intentSources,
			Weights:        intentWeights,
			Confidence:     result.Intent.Confidence,
			TemporalMode:   string(result.Intent.TemporalMode),
			TemporalWeight: result.Intent.TemporalWeight,
		}
	}

	rewritten := ""
	if searchQuery != userQuery {
		rewritten = searchQuery
	}
	return &RAGContext{
		Query:      userQuery,
		Intent:     intentDTO,
		Sources:    ragSources,
		TotalChars: totalChars,
		Debug:      result.Debug,
		Rewritten:  rewritten,
	}, nil
}

// buildMessagesWithContext injects RAG context into the message list.
func (h *RAGChatHandler) buildMessagesWithContext(messages []llm.Message, ragContext *RAGContext) []llm.Message {
	if len(ragContext.Sources) == 0 {
		return messages
	}

	// Build context string
	var contextBuilder strings.Builder
	contextBuilder.WriteString("## Contexte pertinent\n\n")

	for i, source := range ragContext.Sources {
		// Determine source label
		var sourceLabel string
		switch source.SourceType {
		case "mail", "email", "thread":
			sourceLabel = "Email"
		case "note":
			sourceLabel = "Note"
		default:
			sourceLabel = "Document"
		}

		contextBuilder.WriteString(fmt.Sprintf("[%s %d] %s\n", sourceLabel, i+1, source.Title))
		contextBuilder.WriteString(source.Excerpt)
		contextBuilder.WriteString("\n\n")
	}

	contextBuilder.WriteString("---\nCite les sources avec [Document N], [Email N] ou [Note N] quand tu utilises ces informations.")

	contextString := contextBuilder.String()

	// Copy messages to avoid mutating the original
	result := make([]llm.Message, 0, len(messages)+1)

	// Check if there's an existing system message
	hasSystemMessage := len(messages) > 0 && messages[0].Role == "system"

	if hasSystemMessage {
		// Append context to existing system message
		augmentedSystem := llm.Message{
			Role:    "system",
			Content: messages[0].Content + "\n\n" + contextString,
		}
		result = append(result, augmentedSystem)
		result = append(result, messages[1:]...)
	} else {
		// Create new system message with context
		systemMessage := llm.Message{
			Role:    "system",
			Content: contextString,
		}
		result = append(result, systemMessage)
		result = append(result, messages...)
	}

	return result
}

// buildNoResultsMessage adds a system hint when RAG search found no results.
func (h *RAGChatHandler) buildNoResultsMessage(messages []llm.Message, ragContext *RAGContext) []llm.Message {
	// Build a hint based on what was searched
	var searchedSources string
	if ragContext.Intent != nil {
		sources := make([]string, 0)
		for _, w := range ragContext.Intent.Weights {
			if w > 0.5 {
				// High-weight sources
			}
		}
		if len(sources) == 0 {
			searchedSources = "les notes et documents"
		} else {
			searchedSources = strings.Join(sources, " et ")
		}
	} else {
		searchedSources = "les notes et documents"
	}

	noResultsHint := fmt.Sprintf(
		"## Information système\n\nUne recherche a été effectuée dans %s pour répondre à la question de l'utilisateur, mais aucun résultat pertinent n'a été trouvé. Informe l'utilisateur qu'aucune information correspondante n'a été trouvée dans sa base de connaissances.",
		searchedSources,
	)

	// Copy messages to avoid mutating the original
	result := make([]llm.Message, 0, len(messages)+1)

	// Check if there's an existing system message
	hasSystemMessage := len(messages) > 0 && messages[0].Role == "system"

	if hasSystemMessage {
		// Append hint to existing system message
		augmentedSystem := llm.Message{
			Role:    "system",
			Content: messages[0].Content + "\n\n" + noResultsHint,
		}
		result = append(result, augmentedSystem)
		result = append(result, messages[1:]...)
	} else {
		// Create new system message with hint
		systemMessage := llm.Message{
			Role:    "system",
			Content: noResultsHint,
		}
		result = append(result, systemMessage)
		result = append(result, messages...)
	}

	return result
}

// writeSSEEvent writes a single SSE event to the response writer.
func (h *RAGChatHandler) writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, event interface{}) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	_, writeErr := fmt.Fprintf(w, "data: %s\n\n", data)
	if writeErr != nil {
		return writeErr
	}
	flusher.Flush()
	return nil
}
