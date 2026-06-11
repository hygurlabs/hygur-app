// Package handlers provides HTTP handlers for the Hygur API.
package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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
	// Date is the document's canonical date (ISO 8601), always populated so the
	// LLM can reason about "when" for pre-injected context (entity follow-ups),
	// matching what the search_knowledge_base tool exposes.
	Date string `json:"date,omitempty"`
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

// maxToolRounds caps how many consecutive tool-call rounds the chat loop
// will service before forcing the conversation to a final assistant turn.
// Five is comfortably above any plausible legitimate need (multi-step plans
// rarely chain past 2-3) while preventing a runaway model from looping
// indefinitely against a misbehaving tool.
const maxToolRounds = 5

// memoryInjectionTokenBudget caps the size of the "Faits durables" block we
// prepend to the system prompt when injecting accepted memories. ~500 tokens
// is enough for ~10 short facts (the Phase 3.3 spec's recommended ceiling)
// without crowding out the actual conversation context.
const memoryInjectionTokenBudget = 500

// decisionInjectionMax caps how many standing decisions are injected as brain
// context per turn (most recent first), so the block never dominates the prompt.
const decisionInjectionMax = 8

// RAGChatHandler handles the /chat endpoint with RAG enhancement.
type RAGChatHandler struct {
	llmClient       *llm.Client
	unifiedSearcher *retrieval.UnifiedSearcher
	sessionStore    *session.Store
	memoryStore     *tools.MemoryStoreTool
	memorySearch    *tools.MemorySearchTool
	agendaExtractor *agenda.Extractor
	agendaStore     *store.DB
	chatStore       *store.DB
	toolRegistry    *tools.Registry
	chatTokenCap    int           // monthly chat-token cap; 0 = unlimited (local default)
	chatTokenCapDay int           // daily chat-token cap; 0 = unlimited (the fast fuse)
	rpmLimiter      *rateLimiter  // per-tenant request-rate fuse; nil = off
	chatSem         chan struct{} // per-tenant concurrency cap; nil = off
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

// SetChatTokenCap sets the monthly LLM-token budget (prompt + completion of the
// 'chat' category). 0 disables enforcement (the local default). On a managed cloud
// tenant it's set from HYGUR_CHAT_TOKEN_CAP_MONTHLY to protect margin under
// per-token inference pricing; embeddings/indexing are a separate budget.
func (h *RAGChatHandler) SetChatTokenCap(n int) {
	if n > 0 {
		h.chatTokenCap = n
	}
}

// SetDailyTokenCap sets the DAILY LLM-token budget — a fast fuse against a
// runaway loop that complements the monthly cap. 0 disables it. Set on a managed
// tenant from HYGUR_CHAT_TOKEN_CAP_DAILY.
func (h *RAGChatHandler) SetDailyTokenCap(n int) {
	if n > 0 {
		h.chatTokenCapDay = n
	}
}

// SetRateLimits enables per-tenant request-rate (rpm, req/min) and concurrency
// guards on the chat endpoint — fast fuses against a runaway client loop,
// complementing the slower monthly token cap. 0 leaves a guard disabled (the
// local default). Set on a managed tenant from HYGUR_CHAT_RPM_PER_TENANT /
// HYGUR_CHAT_CONCURRENCY_PER_TENANT.
func (h *RAGChatHandler) SetRateLimits(rpm, concurrency int) {
	if rpm > 0 {
		h.rpmLimiter = newRateLimiter(rpm)
	}
	if concurrency > 0 {
		h.chatSem = make(chan struct{}, concurrency)
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

// SetChatStore wires the persistent-transcript store so conversations are saved
// (chat_sessions / chat_messages) as they happen. Passing nil keeps chat
// stateless — exactly the prior behaviour.
func (h *RAGChatHandler) SetChatStore(db *store.DB) {
	h.chatStore = db
}

// SetToolRegistry wires the callable-tool registry into the handler. When
// the registry holds at least one tool, the handler advertises them to the
// LLM via the `tools` field of each request and will execute any tool_calls
// the model emits, looping back to the LLM with the results until the model
// produces a final text answer (capped at maxToolRounds rounds). Passing
// nil — or a registry that ends up empty — disables tool calling and the
// handler behaves exactly as it did before this method existed.
func (h *RAGChatHandler) SetToolRegistry(registry *tools.Registry) {
	h.toolRegistry = registry
}

// baseFormatGuidance is the persona/format hint prepended to every chat turn.
// The macOS client renders assistant messages with MarkdownUI (headings,
// fenced code, GFM tables, blockquotes, lists, hr), so we tell the model to
// lean on Markdown when it helps comprehension. Kept short to avoid bloating
// the prompt budget on small local models.
const baseFormatGuidance = `You are Hygur, the user's personal assistant. ` +
	`The interface renders full Markdown — use it when it improves readability, ` +
	`but stay concise (no Markdown for a one-word, number, or yes/no answer).` +
	"\n\n" +
	`Ground every answer in the retrieved sources; never invent a date, amount, reference or fact. ` +
	`Search (search_knowledge_base) before asking a clarifying question, and only ask if the sources ` +
	`genuinely can't settle it. Anchor an ambiguous term on the meaning dominant in the data rather ` +
	`than guessing. For a question that spans a period, compute the window and pass date_from/date_to ` +
	`so you get every item in range. Tie a document's figures to the period stated in its content, not ` +
	`its received date. Sort dated lists oldest-first unless asked otherwise. Build a total by adding ` +
	`the unit items, and never add an aggregate to the items it already summarises.`

// injectFormatGuidance ensures every chat turn carries the base persona +
// markdown-rendering hint at the top of the system prompt. Subsequent
// augmentations (agenda, memories, RAG context) merge into the same system
// message so the LLM sees one unified system block.
// todayGuidance stamps the current date into the system prompt and tells the
// model to anchor relative time expressions on it. Without this the model has
// no "now" and silently computes periods from dates found in the retrieved
// content (e.g. answering about 2025 when asked for the last two months of
// 2026). Each retrieved source carries a `date` field, so the model can filter
// to the requested window itself — no query-side date parsing needed.
func todayGuidance() string {
	return fmt.Sprintf(
		"Today's date: %s. Resolve relative time expressions against this date, not against dates "+
			"found in the documents. Each source has a `date` field (ISO 8601); for a period "+
			"question, compute date_from/date_to from today and keep only sources inside the window. "+
			"If none fall inside, say so rather than presenting out-of-window documents as if they were.",
		time.Now().Format("2006-01-02"))
}

func injectFormatGuidance(messages []llm.Message) []llm.Message {
	guidance := todayGuidance() + "\n\n" + baseFormatGuidance
	if len(messages) > 0 && messages[0].Role == "system" {
		merged := guidance + "\n\n" + messages[0].Content
		out := make([]llm.Message, len(messages))
		out[0] = llm.Message{Role: "system", Content: merged}
		copy(out[1:], messages[1:])
		return out
	}
	out := make([]llm.Message, 0, len(messages)+1)
	out = append(out, llm.Message{Role: "system", Content: guidance})
	out = append(out, messages...)
	return out
}

// injectAgendaIntoSystemPrompt prepends an urgency block to the system prompt
// listing the actions that are due in the next 48 h.
func injectAgendaIntoSystemPrompt(prompt string, actions []agenda.AgendaAction) string {
	if len(actions) == 0 {
		return prompt
	}
	var b strings.Builder
	b.WriteString("Urgent actions in the next 48h:\n")
	for _, a := range actions {
		b.WriteString(fmt.Sprintf("- [%s] %s (deadline: %s)\n", a.Priority, a.What, a.DeadlineISO))
	}
	b.WriteString("\n")
	return b.String() + prompt
}

// resolveDocumentAttachments expands any Attachment of type "document" into
// inline text on the same message, then strips those attachments from the
// list. The LLM client never sees document refs — only the resolved excerpts.
// Image and audio attachments pass through untouched so the multimodal
// MarshalJSON path can serialise them to OpenAI content blocks.
//
// Best-effort: when a content_id can't be hydrated (deleted, never ingested),
// the attachment is dropped silently rather than failing the whole turn.
func (h *RAGChatHandler) resolveDocumentAttachments(ctx context.Context, messages []llm.Message) []llm.Message {
	if h.unifiedSearcher == nil {
		return messages
	}
	out := make([]llm.Message, len(messages))
	for i, m := range messages {
		out[i] = m
		if len(m.Attachments) == 0 {
			continue
		}
		var docIDs []string
		var keep []llm.Attachment
		for _, att := range m.Attachments {
			if att.Type == llm.AttachmentTypeDocument && att.ContentID != "" {
				docIDs = append(docIDs, att.ContentID)
			} else {
				keep = append(keep, att)
			}
		}
		if len(docIDs) == 0 {
			continue
		}
		results, err := h.unifiedSearcher.FetchByContentIDs(ctx, docIDs)
		if err != nil {
			h.logger.Warn().Err(err).Strs("content_ids", docIDs).
				Msg("failed to resolve document attachments")
			out[i].Attachments = keep
			continue
		}
		var b strings.Builder
		if m.Content != "" {
			b.WriteString(m.Content)
			b.WriteString("\n\n")
		}
		for _, r := range results {
			label := r.Title
			if label == "" {
				label = r.ContentID
			}
			b.WriteString(fmt.Sprintf("[Document: %s]\n%s\n\n", label, r.Excerpt))
		}
		out[i].Content = strings.TrimRight(b.String(), "\n")
		out[i].Attachments = keep
	}
	return out
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

	// Per-tenant monthly LLM budget — the cloud margin guard under per-token
	// inference. Enforced only when a cap is set (HYGUR_CHAT_TOKEN_CAP_MONTHLY);
	// local installs leave it 0. Checked here, before any LLM call, so we refuse
	// cleanly (pre-SSE JSON) rather than mid-stream. A query error fails OPEN
	// (never block a paying user because the usage read hiccuped).
	if h.chatTokenCap > 0 && h.chatStore != nil {
		if used, err := h.chatStore.ChatTokensThisMonth(r.Context()); err == nil && used >= h.chatTokenCap {
			h.logger.Warn().Int("used", used).Int("cap", h.chatTokenCap).Msg("monthly chat-token cap reached")
			writeChatError(w, http.StatusTooManyRequests, "QUOTA_EXCEEDED",
				"You've reached this month's usage limit. It resets at the start of next month.")
			return
		}
	}

	// Per-tenant DAILY budget — the fast fuse: catches a runaway loop within a day,
	// long before the monthly cap. Fails OPEN on a read error.
	if h.chatTokenCapDay > 0 && h.chatStore != nil {
		if used, err := h.chatStore.ChatTokensToday(r.Context()); err == nil && used >= h.chatTokenCapDay {
			h.logger.Warn().Int("used", used).Int("cap", h.chatTokenCapDay).Msg("daily chat-token cap reached")
			writeChatError(w, http.StatusTooManyRequests, "QUOTA_EXCEEDED",
				"You've reached today's usage limit. It resets tomorrow.")
			return
		}
	}

	// Per-tenant request-rate fuse (req/min) — a fast guard against a runaway
	// client loop, before any work. 0/nil = off (local default).
	if h.rpmLimiter != nil && !h.rpmLimiter.Allow() {
		writeChatError(w, http.StatusTooManyRequests, "RATE_LIMITED",
			"Too many requests in a short time — give it a moment and try again.")
		return
	}

	// Per-tenant concurrency cap — bound simultaneous generations. Non-blocking:
	// reject at capacity rather than queue (a queued SSE stream would just hang).
	// Held for the whole request (the streamed generation). nil = off.
	if h.chatSem != nil {
		select {
		case h.chatSem <- struct{}{}:
			defer func() { <-h.chatSem }()
		default:
			writeChatError(w, http.StatusTooManyRequests, "BUSY",
				"Too many chats in flight right now — try again in a moment.")
			return
		}
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

	// Persist the conversation transcript when a session id is supplied. The
	// user turn is saved up-front so the question survives a mid-stream failure;
	// the assistant turn is appended after streaming completes. turnSources
	// accumulates the citations emitted this turn so they ride into the saved
	// assistant message. Best-effort — persistence never breaks the chat.
	var turnSources []RAGSource
	if req.SessionID != "" && h.chatStore != nil {
		h.persistUserTurn(r.Context(), req, latestUserQuery)
	}

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

	// Prepare messages for LLM. Document attachments must be resolved to
	// text BEFORE any RAG/agenda/memory injection so the resolved content
	// is part of the context the LLM sees on this turn.
	messages := h.resolveDocumentAttachments(r.Context(), req.Messages)

	// Persona + Markdown-rendering hint goes first so all subsequent
	// augmentations (agenda, memories, RAG context) merge into the same
	// system block.
	messages = injectFormatGuidance(messages)

	// Direct-retrieval fast-path: when the latest user query is an entity
	// follow-up ("et son IBAN ?") and we already know the relevant sources
	// from the session, pre-inject them so the LLM doesn't need to call
	// search_knowledge_base. The full RAG pipeline (classify/expand/judge)
	// is now driven by the LLM itself via the search_knowledge_base tool —
	// see internal/tools/search_knowledge_base.go.
	if ragEnabled && directRetrievalUsed && prefetchedContext != nil && len(prefetchedContext.Sources) > 0 {
		mergeSourcesIntoSession(sessionCtx, prefetchedContext.Sources)

		debugRequested := r.URL.Query().Get("debug") == "1" || r.Header.Get("X-Hygur-Debug") == "1"
		if debugRequested {
			dbg := buildDebugEvent(prefetchedContext, "direct_retrieval")
			if err := h.writeSSEEvent(w, flusher, dbg); err != nil {
				h.logger.Debug().Err(err).Msg("failed to write debug event")
			}
		}

		contextEvent := RAGContextEvent{
			Type:    "rag_context",
			Sources: prefetchedContext.Sources,
			Intent:  prefetchedContext.Intent,
		}
		turnSources = append(turnSources, prefetchedContext.Sources...)
		if err := h.writeSSEEvent(w, flusher, contextEvent); err != nil {
			h.logger.Debug().Err(err).Msg("failed to write rag_context event")
			return
		}

		messages = h.buildMessagesWithContext(req.Messages, prefetchedContext)
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

	// Persistent-memory injection (Phase 3.3): prepend any accepted durable
	// user facts most semantically similar to the current query. Only
	// memories with accepted_at IS NOT NULL are eligible — pending candidates
	// stay out of the LLM context until the user reviews them in the
	// Memories tab. Best-effort — failure here must not break chat.
	if h.memorySearch != nil && latestUserQuery != "" {
		searchCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		hits, err := h.memorySearch.SearchAccepted(searchCtx, latestUserQuery, 5, memoryInjectionTokenBudget)
		cancel()
		if err == nil && len(hits) > 0 {
			messages = injectMemoriesIntoSystem(messages, hits)
		}
	}

	// Brain context (Direction A): Hygur's own signals — the user's standing
	// decisions + the running "story so far" synopsis — so a chat answer can
	// ground in "you decided X" / the ongoing narrative even when document
	// retrieval didn't surface them. Cheap (no LLM), bounded, best-effort.
	if h.chatStore != nil {
		var decisions []*store.Decision
		if ds, err := h.chatStore.ListDecisions(r.Context(), "", "standing"); err == nil {
			if len(ds) > decisionInjectionMax {
				ds = ds[:decisionInjectionMax]
			}
			decisions = ds
		}
		var synopsis string
		if ch, err := h.chatStore.GetChronicleChapter(r.Context(), "life"); err == nil && ch != nil {
			synopsis = ch.Synopsis
		}
		messages = injectBrainContext(messages, decisions, synopsis)
	}

	// Build the LLM request
	llmReq := llm.ChatRequest{
		Model:       req.Model,
		Messages:    messages,
		Stream:      true,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}

	// Wire registered tools into the request so the model can decide to call
	// them. We rebuild the definitions on each request — the registry is
	// safe for concurrent reads, and the cost is negligible compared to the
	// LLM round-trip. Empty registry → Tools stays nil so the field is
	// omitted (some servers reject `tools: []`).
	//
	// When ragEnabled=false (per-request override), drop search_knowledge_base
	// so the LLM can't trigger retrieval the user explicitly disabled.
	// Base tool set, computed once; the per-round set is derived from it inside
	// the loop (tainted-context guard). When ragEnabled=false, drop
	// search_knowledge_base so the LLM can't trigger retrieval the user disabled.
	var baseDefs []map[string]any
	if h.toolRegistry != nil {
		baseDefs = h.toolRegistry.OpenAIDefinitions()
		if !ragEnabled {
			baseDefs = filterToolDef(baseDefs, "search_knowledge_base")
		}
	}

	// During the LLM's prefill phase (loading the full context into KV cache)
	// no tokens are sent, leaving the SSE connection silent for potentially
	// tens of seconds. URLSession and browser EventSource implementations
	// time out on idle connections. Send an SSE comment every 20 s to keep
	// the connection alive; comments are ignored by clients but reset their
	// idle timers. A mutex serialises writes between this goroutine and the
	// stream callback below.
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

	// writeSSE serialises one event onto the response under the shared lock.
	// Callers pass a JSON-marshalable payload; the wire format mirrors the
	// existing convention (`data: {…}\n\n`) so clients don't need to change.
	writeSSE := func(payload any) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal SSE event: %w", err)
		}
		writeMu.Lock()
		_, werr := fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		writeMu.Unlock()
		return werr
	}

	// Accumulate assistant deltas across all tool rounds so the post-stream
	// memory-extraction and session-update steps see the user-visible
	// answer in full. Bounded by Go's slice growth — a typical turn is
	// < 4 KB, well below any worry threshold.
	var assistantBuf strings.Builder

	// Tool-call loop. Each iteration runs one streaming completion. When
	// the model finishes with `finish_reason: "tool_calls"` we execute the
	// requested tools, append the assistant + tool messages to the request,
	// and loop. Otherwise the iteration is final and we emit the `done`
	// SSE event below.
	var streamErr error
	var lastUsage *llm.Usage
	finalRound := false
	tainted := false // set once untrusted web content enters (tainted-context guard)

	for round := 0; round < maxToolRounds && !finalRound; round++ {
		// Tainted-context injection defence: once a web tool has pulled untrusted
		// external content into the conversation, drop the side-effecting tools so
		// an injected page can't trick the model into creating notes/events. The
		// read-only tools (search_knowledge_base, web_search, fetch_url) stay.
		roundDefs := baseDefs
		if tainted {
			for _, name := range untrustedDisabledTools {
				roundDefs = filterToolDef(roundDefs, name)
			}
		}
		if len(roundDefs) > 0 {
			llmReq.Tools = roundDefs
			llmReq.ToolChoice = "auto"
		} else {
			llmReq.Tools = nil
			llmReq.ToolChoice = ""
		}

		var roundContent strings.Builder
		var roundFinishReason string
		assembler := llm.NewToolCallAssembler()

		err := h.llmClient.StreamChatRich(r.Context(), llmReq, func(evt llm.StreamEvent) error {
			// NOTE: do NOT stop the keepalive here. This callback first fires on
			// the tool-call round; the long, SILENT phase is the NEXT round
			// (the model reasoning over tool results before emitting the answer).
			// Stopping now left that gap unguarded → the client idle-timed-out on
			// big/slow answers ("Load failed"). The keepalive runs until the loop
			// ends (final stopOnce below); its 20 s SSE comments interleave
			// harmlessly with token data under the shared write lock.
			select {
			case <-r.Context().Done():
				return r.Context().Err()
			default:
			}

			if evt.Done {
				if evt.Usage != nil {
					lastUsage = evt.Usage
				}
				return nil
			}

			if evt.FinishReason != "" {
				roundFinishReason = evt.FinishReason
			}

			for _, d := range evt.ToolCallDeltas {
				assembler.Add(d)
			}

			if evt.Delta != "" {
				roundContent.WriteString(evt.Delta)
				assistantBuf.WriteString(evt.Delta)
				if err := writeSSE(map[string]any{"delta": evt.Delta, "done": false}); err != nil {
					return err
				}
			}
			return nil
		})

		if err != nil {
			streamErr = err
			break
		}

		// No tool invocation requested — this round produced the final
		// assistant turn. Exit the loop and let the post-loop block emit
		// the terminal `done` event.
		if roundFinishReason != "tool_calls" || assembler.Len() == 0 {
			finalRound = true
			break
		}

		// Otherwise: execute every requested tool, fan results back into
		// the message history, and let the loop run another round.
		toolCalls := assembler.Finalize()
		llmReq.Messages = append(llmReq.Messages, llm.Message{
			Role:      "assistant",
			Content:   roundContent.String(),
			ToolCalls: toolCalls,
		})

		toolErrAbort := false
		for _, tc := range toolCalls {
			argsRaw := json.RawMessage(tc.Function.Arguments)
			h.logger.Info().Str("tool", tc.Function.Name).Str("call_id", tc.ID).RawJSON("args", argsRaw).Msg("executing tool")
			result, execErr := h.toolRegistry.Execute(r.Context(), tc.Function.Name, argsRaw)
			if isUntrustedSourceTool(tc.Function.Name) {
				tainted = true // subsequent rounds lose the side-effecting tools
			}

			evt := map[string]any{
				"type":      "tool_call",
				"id":        tc.ID,
				"name":      tc.Function.Name,
				"arguments": argsRaw,
			}
			var toolMsg llm.Message
			if execErr != nil {
				h.logger.Warn().Err(execErr).Str("tool", tc.Function.Name).Msg("tool execution failed")
				evt["error"] = execErr.Error()
				// Feed the error back to the LLM so it can recover (apologise,
				// retry with different args, etc.) rather than hanging.
				errPayload, _ := json.Marshal(map[string]string{"error": execErr.Error()})
				toolMsg = llm.Message{
					Role:       "tool",
					Content:    string(errPayload),
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
				}
			} else {
				h.logger.Info().Str("tool", tc.Function.Name).Str("call_id", tc.ID).Int("result_bytes", len(result)).Msg("tool execution succeeded")
				evt["result"] = result
				toolMsg = llm.Message{
					Role:       "tool",
					Content:    string(result),
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
				}
				// search_knowledge_base also doubles as a `rag_context` event so
				// the existing UI keeps rendering the sources panel without
				// needing to know about the tool-call shape.
				if tc.Function.Name == "search_knowledge_base" {
					if ragSources := decodeSearchSources(result); ragSources != nil {
						mergeSourcesIntoSession(sessionCtx, ragSources)
						turnSources = append(turnSources, ragSources...)
						_ = writeSSE(RAGContextEvent{
							Type:    "rag_context",
							Sources: ragSources,
						})
					}
				}
			}

			if err := writeSSE(evt); err != nil {
				streamErr = err
				toolErrAbort = true
				break
			}
			llmReq.Messages = append(llmReq.Messages, toolMsg)
		}

		if toolErrAbort {
			break
		}
	}

	// Ensure the keepalive goroutine exits even if no token was received.
	stopOnce.Do(func() { close(stopKeepalive) })

	if streamErr != nil {
		// Client disconnect — don't log as error.
		if r.Context().Err() != nil {
			h.logger.Debug().Msg("stream ended due to client disconnect")
			return
		}

		h.logger.Error().Err(streamErr).Msg("chat stream error")
		writeSSEError(w, "LLM_STUDIO_ERROR", streamErr.Error())
		flusher.Flush()
	} else {
		// Final `done` event with usage, mirroring the pre-tool-loop wire format.
		doneEvent := map[string]any{"done": true}
		if lastUsage != nil {
			doneEvent["usage"] = map[string]int{
				"prompt_tokens":     lastUsage.PromptTokens,
				"completion_tokens": lastUsage.CompletionTokens,
				"total_tokens":      lastUsage.TotalTokens,
			}
		}
		_ = writeSSE(doneEvent)
	}

	// Persist the assistant answer + its citations to the durable transcript.
	// Detached context so it survives a client disconnect after streaming. Even
	// a partial answer (mid-stream failure) is worth keeping.
	if req.SessionID != "" && h.chatStore != nil && assistantBuf.Len() > 0 {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		h.persistAssistantTurn(pctx, req.SessionID, assistantBuf.String(), turnSources)
		pcancel()
	}

	// Post-stream: extract entities from the assistant answer and append a
	// ResolvedQuery so the next turn's direct-answer check has fresh context.
	// Skip when the session is transient (no SessionID) or the answer is empty.
	if req.SessionID != "" && assistantBuf.Len() > 0 {
		updateSessionPostSynthesis(sessionCtx, latestUserQuery, assistantBuf.String(), req.RecentSourceIDs)
	}

	// Fire-and-forget per-turn memory extraction. The extractor calls the LLM
	// (1-3 s), so detach from the request context — we don't want to block
	// returning to the client and we also want extraction to survive the
	// client disconnecting once the stream ends. SessionID, when present,
	// links candidates back to the conversation that produced them.
	//
	// Phase 3.3: PersistExtracted now stores rows as source='extracted' with
	// accepted_at=NULL. They will NOT be injected into future chats until the
	// user reviews and accepts them via the Memories tab.
	if h.memoryStore != nil && assistantBuf.Len() > 0 && latestUserQuery != "" {
		userMsg := latestUserQuery
		assistantMsg := assistantBuf.String()
		sessionID := req.SessionID
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
			stored, persistErr := h.memoryStore.PersistExtracted(extracted, sessionID)
			evt := h.logger.Info()
			if persistErr != nil {
				evt = h.logger.Warn().Err(persistErr)
			}
			evt.Int("extracted", len(extracted)).Int("stored", stored).
				Str("session_id", sessionID).
				Msg("pending memory candidates persisted from turn")
		}()
	}
}

// persistUserTurn ensures the session row exists (creating it with an
// auto-title and, when the chat is focused on a single project, a project link)
// then appends the user message. Best-effort: any error is logged and ignored.
func (h *RAGChatHandler) persistUserTurn(ctx context.Context, req RAGChatRequest, userMsg string) {
	if userMsg == "" {
		return
	}
	exists, err := h.chatStore.ChatSessionExists(ctx, req.SessionID)
	if err != nil {
		h.logger.Debug().Err(err).Msg("chat persist: session exists check failed")
		return
	}
	if !exists {
		var projectID *string
		if req.FocusScope != nil && len(req.FocusScope.ProjectIDs) == 1 {
			pid := req.FocusScope.ProjectIDs[0]
			projectID = &pid
		}
		now := time.Now()
		if err := h.chatStore.CreateChatSession(ctx, &store.ChatSession{
			SessionID: req.SessionID,
			Title:     autoSessionTitle(userMsg),
			ProjectID: projectID,
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			h.logger.Debug().Err(err).Msg("chat persist: create session failed")
			return
		}
	}
	userMsgID := uuid.NewString()
	if err := h.chatStore.AppendChatMessage(ctx, &store.ChatMessage{
		MessageID: userMsgID,
		SessionID: req.SessionID,
		Role:      "user",
		Content:   userMsg,
	}); err != nil {
		h.logger.Debug().Err(err).Msg("chat persist: append user message failed")
		return
	}
	// Persist the image/audio media of this turn so reopening the conversation
	// re-displays the image and replays the audio. Documents are KB references
	// (re-attachable by id), so they're not stored here.
	if atts := latestUserMediaAttachments(req.Messages); len(atts) > 0 {
		if err := h.chatStore.AppendChatMessageAttachments(ctx, userMsgID, atts); err != nil {
			h.logger.Debug().Err(err).Msg("chat persist: append attachments failed")
		}
	}
}

// latestUserMediaAttachments extracts the image/audio attachments of the most
// recent user message, decoding the wire base64 to raw bytes for storage.
func latestUserMediaAttachments(messages []llm.Message) []store.ChatAttachment {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		var out []store.ChatAttachment
		for _, att := range messages[i].Attachments {
			switch att.Type {
			case llm.AttachmentTypeImage:
				data, err := base64.StdEncoding.DecodeString(att.Data)
				if err != nil || len(data) == 0 {
					continue
				}
				out = append(out, store.ChatAttachment{
					Type: "image", Title: att.Title, MimeType: att.MimeType,
					Data: data, ByteSize: len(data),
				})
			case llm.AttachmentTypeAudio:
				data, err := base64.StdEncoding.DecodeString(att.Data)
				if err != nil || len(data) == 0 {
					continue
				}
				out = append(out, store.ChatAttachment{
					Type: "audio", Title: att.Title, Format: att.Format,
					Data: data, ByteSize: len(data),
				})
			}
		}
		return out
	}
	return nil
}

// persistAssistantTurn appends the assistant answer and its (deduplicated)
// cited sources to the transcript.
func (h *RAGChatHandler) persistAssistantTurn(ctx context.Context, sessionID, content string, sources []RAGSource) {
	var sourcesJSON string
	if deduped := dedupSources(sources); len(deduped) > 0 {
		if b, err := json.Marshal(deduped); err == nil {
			sourcesJSON = string(b)
		}
	}
	if err := h.chatStore.AppendChatMessage(ctx, &store.ChatMessage{
		MessageID: uuid.NewString(),
		SessionID: sessionID,
		Role:      "assistant",
		Content:   content,
		Sources:   sourcesJSON,
	}); err != nil {
		h.logger.Debug().Err(err).Msg("chat persist: append assistant message failed")
	}
}

// autoSessionTitle derives a short, single-line title from the first user
// message of a conversation.
func autoSessionTitle(msg string) string {
	msg = strings.TrimSpace(msg)
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = strings.TrimSpace(msg[:i])
	}
	if msg == "" {
		return "Conversation"
	}
	r := []rune(msg)
	const max = 60
	if len(r) > max {
		return strings.TrimSpace(string(r[:max])) + "…"
	}
	return msg
}

// dedupSources removes duplicate citations (same content_id) accumulated across
// tool rounds, preserving first-seen order.
func dedupSources(sources []RAGSource) []RAGSource {
	if len(sources) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(sources))
	out := make([]RAGSource, 0, len(sources))
	for _, s := range sources {
		if _, ok := seen[s.ContentID]; ok {
			continue
		}
		seen[s.ContentID] = struct{}{}
		out = append(out, s)
	}
	return out
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

// injectBrainContext prepends a compact, grounded block of Hygur's own signals —
// the user's standing decisions and the running life synopsis — so the assistant
// can reference "you decided X" / the ongoing story even when document retrieval
// didn't surface them. Cheap lookups (no LLM), bounded, grounded in stored state.
func injectBrainContext(messages []llm.Message, decisions []*store.Decision, synopsis string) []llm.Message {
	if len(decisions) == 0 && strings.TrimSpace(synopsis) == "" {
		return messages
	}
	var b strings.Builder
	if len(decisions) > 0 {
		b.WriteString("## The user's standing decisions\n\n")
		for _, d := range decisions {
			b.WriteString("- ")
			b.WriteString(d.Statement)
			if len(d.DecidedOn) >= 10 {
				b.WriteString(" (")
				b.WriteString(d.DecidedOn[:10])
				b.WriteString(")")
			}
			b.WriteString("\n")
		}
	}
	if s := strings.TrimSpace(synopsis); s != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## The story so far\n\n")
		b.WriteString(s)
		b.WriteString("\n")
	}
	block := b.String()

	hasSystem := len(messages) > 0 && messages[0].Role == "system"
	out := make([]llm.Message, 0, len(messages)+1)
	if hasSystem {
		out = append(out, llm.Message{Role: "system", Content: messages[0].Content + "\n\n" + block})
		out = append(out, messages[1:]...)
	} else {
		out = append(out, llm.Message{Role: "system", Content: block})
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
			Date:        r.Date,
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
	contextBuilder.WriteString("## Relevant context\n\n")

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

		header := fmt.Sprintf("[%s %d] %s", sourceLabel, i+1, source.Title)
		if source.Date != "" {
			header += " (date : " + source.Date + ")"
		}
		contextBuilder.WriteString(header + "\n")
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
			searchedSources = "the notes and documents"
		} else {
			searchedSources = strings.Join(sources, " and ")
		}
	} else {
		searchedSources = "the notes and documents"
	}

	noResultsHint := fmt.Sprintf(
		"## System information\n\nA search was run in %s to answer the user's question, but no relevant result was found. Tell the user that no matching information was found in their knowledge base.",
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

// filterToolDef removes the entry whose function.name matches `name` from the
// list of OpenAI tool definitions. Used to drop the search_knowledge_base tool
// when RAG is disabled per-request.
// untrustedDisabledTools are the side-effecting tools removed from the model's
// toolset once untrusted web content has entered the conversation (tainted
// context). Add any future write/action tool here.
var untrustedDisabledTools = []string{"create_note", "create_calendar_event"}

// isUntrustedSourceTool reports whether a tool pulls UNTRUSTED external content
// into the conversation — which taints the context for the rest of the turn.
func isUntrustedSourceTool(name string) bool {
	return name == "web_search" || name == "fetch_url"
}

func filterToolDef(defs []map[string]any, name string) []map[string]any {
	out := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		fn, _ := d["function"].(map[string]any)
		if fn == nil {
			out = append(out, d)
			continue
		}
		if n, _ := fn["name"].(string); n == name {
			continue
		}
		out = append(out, d)
	}
	return out
}

// decodeSearchSources parses the JSON returned by SearchKnowledgeBaseTool back
// into the wire-shaped RAGSource list the SSE clients already understand.
// Returns nil when the payload doesn't match — callers must tolerate that
// (the chat keeps working, only the sources panel goes dark for the turn).
func decodeSearchSources(raw json.RawMessage) []RAGSource {
	if len(raw) == 0 {
		return nil
	}
	var payload tools.SearchKnowledgeBaseResult
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	if len(payload.Sources) == 0 {
		return nil
	}
	out := make([]RAGSource, 0, len(payload.Sources))
	for _, s := range payload.Sources {
		out = append(out, RAGSource{
			ContentID:   s.ContentID,
			SourceType:  s.SourceType,
			Title:       s.Title,
			Excerpt:     s.Excerpt,
			Score:       s.Score,
			MailFrom:    s.MailFrom,
			MailDate:    s.MailDate,
			MailSubject: s.MailSubject,
			Date:        s.Date,
		})
	}
	return out
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
