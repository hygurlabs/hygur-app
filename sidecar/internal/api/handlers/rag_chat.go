// Package handlers provides HTTP handlers for the Hygur API.
package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/agenda"
	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/fact"
	"github.com/hygur/sidecar/internal/identity"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/session"
	"github.com/hygur/sidecar/internal/store"
	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
)

// Tool-loop robustness backstops. These are generous safety nets against a
// goroutine parked forever inside a blocking call (a cold/contended local model,
// a tool that never returns) — NOT tight SLAs. When one fires we emit an honest
// SSE error and end the stream cleanly rather than letting the wire sit idle
// until an intervening layer silently kills the socket ("Can't load", nothing
// logged). They are package vars (not consts) purely so tests can shrink them;
// production never reassigns them.
var (
	// synthesisRoundTimeout backstops a single streaming LLM round — the initial
	// completion and every post-tool synthesis round. This is the long SILENT
	// phase (the model reasoning over tool results before emitting a token).
	synthesisRoundTimeout = 90 * time.Second
	// toolExecTimeout backstops one tool execution. Runs the tool on a detached
	// goroutine so a tool that ignores its context can never hang the request.
	toolExecTimeout = 30 * time.Second
	// keepAliveInterval is how often the heartbeat fires during a silent wait.
	// Kept well under any known intervening idle-timeout and under the client's
	// idle timer so the connection never sits fully idle.
	keepAliveInterval = 10 * time.Second
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
	// OwnerOrigin: "owner" (the user's own content) vs "external" (a third party).
	// Drives attribution in synthesis so a third party's claim never reads as the
	// user's own position/decision (the Porto case).
	OwnerOrigin string `json:"owner_origin,omitempty"`
	// Tier/Validity — the authority stratum (A-1 multi-lens): confirmed/candidate/
	// capture · current/superseded/conflicted. Labels each source by its lens.
	Tier     string `json:"tier,omitempty"`
	Validity string `json:"validity,omitempty"`
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

// MemoryWriteEvent surfaces an autonomous memory write to the client so the user
// sees what the turn saved inline in the chat, instead of only later in the Mind
// review queue. One event is emitted per persisted memory. Status is "pending"
// (awaiting the user's review, the current path for extracted memories) or
// "accepted" (already eligible for injection).
type MemoryWriteEvent struct {
	Type       string `json:"type"` // "memory_write"
	MemoryID   string `json:"memory_id"`
	Content    string `json:"content"`
	MemoryType string `json:"memory_type"` // "fact" | "preference" | "action"
	Status     string `json:"status"`      // "pending" | "accepted"
}

// PendingActionEvent surfaces a gated side-effect action to the client so the UI
// can render a Confirm/Cancel card (WP3, Décision 2). The registry has already
// registered the action and withheld execution; confirming hits
// POST /actions/{action_id}/confirm, which then runs the tool exactly once.
type PendingActionEvent struct {
	Type     string `json:"type"` // "pending_action"
	ActionID string `json:"action_id"`
	Tool     string `json:"tool"`
	Preview  string `json:"preview"`
}

// DeterminedAnswerSource is a document that carries a determined identifier value, surfaced so
// the user can verify it independently of the LLM's prose.
type DeterminedAnswerSource struct {
	ContentID string `json:"content_id"`
	Title     string `json:"title,omitempty"`
}

// DeterminedAnswerEvent is the CUT-LLM-SAFE render of a factual-identifier answer. The engine
// (the lookup_identifier tool, driven by the model's language understanding) PRODUCES the value;
// the handler emits this event so the client renders the answer (value + label + confidence +
// sources) from the ENGINE, not from the streamed LLM text. The LLM may add warm framing but can
// no longer substitute, hedge, or decline the value — the value is on the wire before its prose.
// On an engine DECLINE (Confidence "none") Value is empty and Message carries an honest decline,
// so the client shows "no verified value" rather than letting the model fabricate one.
type DeterminedAnswerEvent struct {
	Type       string `json:"type"` // "determined_answer"
	Label      string `json:"label,omitempty"`
	Subject    string `json:"subject,omitempty"`
	Value      string `json:"value,omitempty"`
	Confidence string `json:"confidence"` // "high" | "medium" | "none"
	Message    string `json:"message,omitempty"`
	// Note carries a cross-document supersession contradiction ("Previously 5 mg — updated.") shown
	// alongside a determined figure. Empty when nothing was superseded.
	Note string `json:"note,omitempty"`
	// Offer carries a gated follow-up ACTION offer ("Want me to draft a confirmation email and update
	// your calendar?") appended to the answer. Empty when no action is offered. The action itself is
	// only executed through the pending_action confirmation gate — the offer is just the invitation.
	Offer   string                   `json:"offer,omitempty"`
	Sources []DeterminedAnswerSource `json:"sources,omitempty"`
}

// determinedAnswerFromToolResult turns a lookup_identifier tool result into the authoritative
// render event, or (nil,false) for any other tool or an unparseable result. This is the SINGLE
// bridge from the deterministic engine to the client render: the value shown to the user is the
// engine's, so the LLM path cannot change it. High/medium carry the value; "none" carries an
// honest decline message and NO value (the engine has nothing → no fabrication).
func determinedAnswerFromToolResult(toolName string, result json.RawMessage) (*DeterminedAnswerEvent, bool) {
	if len(result) == 0 {
		return nil, false
	}
	switch toolName {
	case "lookup_identifier":
		var lr tools.LookupResponse
		if err := json.Unmarshal(result, &lr); err != nil {
			return nil, false
		}
		evt := &DeterminedAnswerEvent{
			Type:       "determined_answer",
			Label:      strings.TrimSpace(lr.Label),
			Subject:    strings.TrimSpace(lr.Subject),
			Confidence: string(lr.Tier),
		}
		for _, s := range lr.Sources {
			evt.Sources = append(evt.Sources, DeterminedAnswerSource{ContentID: s.ContentID, Title: s.Title})
		}
		switch lr.Tier {
		case fact.TierHigh, fact.TierMed:
			evt.Value = lr.Value
		default:
			evt.Confidence = "none"
			evt.Message = "No verified value — I don't have a confirmed one on record for you."
		}
		return evt, true
	case "lookup_figure":
		// A labelled MONETARY figure (FIGURES_TRUTH_PLAN F1). The engine determined the value +
		// context; the tool pre-composed the display Value ("7 421,85 €") and Label ("VAT to pay ·
		// Q1 2026"). Rendered by the SAME cut-LLM-safe card — value on the wire before the prose, so
		// the LLM cannot substitute, hedge, or decline the amount.
		var fr tools.FigureResponse
		if err := json.Unmarshal(result, &fr); err != nil {
			return nil, false
		}
		evt := &DeterminedAnswerEvent{
			Type:       "determined_answer",
			Label:      strings.TrimSpace(fr.Label),
			Subject:    strings.TrimSpace(fr.Subject),
			Confidence: string(fr.Tier),
			Note:       strings.TrimSpace(fr.Note),
		}
		for _, s := range fr.Sources {
			evt.Sources = append(evt.Sources, DeterminedAnswerSource{ContentID: s.ContentID, Title: s.Title})
		}
		switch fr.Tier {
		case fact.TierHigh, fact.TierMed:
			evt.Value = fr.Value
		default:
			evt.Confidence = "none"
			evt.Message = "No verified figure — I don't have a confirmed value for that."
		}
		return evt, true
	case "lookup_meeting":
		// A meeting TIME reconciled across email + calendar (contradiction-aware rendez-vous). The
		// engine determined the current time and any cross-source contradiction; the tool pre-composed
		// the display Value + Label + the contradiction Note + the gated action Offer. Rendered by the
		// SAME cut-LLM-safe card — the time is on the wire before the prose, so the LLM cannot move it.
		var mr tools.MeetingResponse
		if err := json.Unmarshal(result, &mr); err != nil {
			return nil, false
		}
		evt := &DeterminedAnswerEvent{
			Type:       "determined_answer",
			Label:      strings.TrimSpace(mr.Label),
			Subject:    strings.TrimSpace(mr.Subject),
			Confidence: string(mr.Tier),
			Note:       strings.TrimSpace(mr.Note),
			Offer:      strings.TrimSpace(mr.Offer),
		}
		for _, s := range mr.Sources {
			evt.Sources = append(evt.Sources, DeterminedAnswerSource{ContentID: s.ContentID, Title: s.Title})
		}
		switch mr.Tier {
		case fact.TierHigh, fact.TierMed:
			evt.Value = mr.Value
		default:
			evt.Confidence = "none"
			evt.Message = "No verified meeting time — I don't have a confirmed one for that meeting."
		}
		return evt, true
	default:
		return nil, false
	}
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

// contradictionInjectionMax caps how many open contradictions are injected as
// brain context per turn. Read from the durable cache (never computed in the chat
// path), so injection stays cheap and non-blocking.
const contradictionInjectionMax = 5

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
	pendingActions  *tools.PendingActionStore // WP3 confirmation gate for side-effect tools
	// Authoritative determined-facts layer (CORE thesis): the store + owner matcher used to
	// resolve the query's subjects and assemble their DETERMINED identifiers/claims in the
	// pipeline. ownerSubject is a representative configured owner name for first-person framing.
	// nil/"" leaves the layer off (the handler then behaves exactly as before).
	factsDB      *store.DB
	ownerMatcher *identity.Matcher
	ownerSubject string
	// Voie A pre-match resolvers (slot-filling socle): the SAME deterministic engine tools the
	// LLM can call, held so the handler can call them DIRECTLY at query time — before any LLM
	// round — and compose the factual answer itself (the LLM never writes the value). nil leaves
	// Voie A off (the handler behaves exactly as the LLM-driven tool path did).
	idTool           valueLookup
	figTool          valueLookup
	meetTool         valueLookup   // contradiction-aware rendez-vous (lookup_meeting)
	chatTokenCap     int           // monthly chat-token cap; 0 = unlimited (local default)
	chatTokenCapDay  int           // daily chat-token cap; 0 = unlimited (the fast fuse)
	rpmLimiter       *rateLimiter  // per-tenant request-rate fuse; nil = off
	chatSem          chan struct{} // per-tenant concurrency cap; nil = off
	maxTokensCeiling int           // server-side ceiling on client-supplied MaxTokens
	config           RAGConfig
	logger           zerolog.Logger
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
		llmClient:        llmClient,
		unifiedSearcher:  unifiedSearcher,
		sessionStore:     sessionStore,
		maxTokensCeiling: resolveChatMaxTokensCeiling(),
		config:           config.Validate(),
		logger:           logger.With().Str("handler", "rag_chat").Logger(),
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

// SetPendingActionStore wires the WP3 confirmation gate. The same store is set
// on the tool registry (which withholds SideEffect tools and records them here);
// the handler reads it to execute a confirmed action from
// POST /actions/{action_id}/confirm.
func (h *RAGChatHandler) SetPendingActionStore(p *tools.PendingActionStore) {
	h.pendingActions = p
}

// SetDeterminedFacts wires the authoritative determined-facts layer. On each turn the handler
// resolves the query's subject(s) deterministically — the owner (first-person framing) plus the
// single named subject the query mentions — assembles their DETERMINED identifiers/claims from
// the WP36.a dossier bricks, and injects them as the VERIFIED layer so the LLM voices factual
// identifier VALUES from there, never from raw untrusted excerpts. db + owner must both be
// non-nil to enable it. ownerSubject is derived from ownerNames (the first configured name the
// matcher recognizes as the owner); empty simply omits the first-person subject.
func (h *RAGChatHandler) SetDeterminedFacts(db *store.DB, owner *identity.Matcher, ownerNames []string) {
	h.factsDB = db
	h.ownerMatcher = owner
	for _, n := range ownerNames {
		if owner.IsOwnerNorm(contradict.NormKey(n)) {
			h.ownerSubject = n
			break
		}
	}
	// Build the Voie A resolvers over the SAME store/owner so the pre-match reaches the identical
	// determined values the LLM-driven tools would — the difference is only WHO calls them.
	h.idTool = tools.NewLookupIdentifierTool(db, owner, h.ownerSubject)
	h.figTool = tools.NewLookupFigureTool(db, owner, h.ownerSubject)
	h.meetTool = tools.NewLookupMeetingTool(db)
}

// SetVoieATools overrides the Voie A resolvers (test seam). Production wiring builds them in
// SetDeterminedFacts; tests inject stubs to drive the pre-match without a populated store.
func (h *RAGChatHandler) SetVoieATools(identifier, figure valueLookup) {
	h.idTool = identifier
	h.figTool = figure
}

// SetVoieAMeetingTool overrides the meeting resolver (test seam), so a handler test can drive the
// rendez-vous lane with a stub without a populated store.
func (h *RAGChatHandler) SetVoieAMeetingTool(meeting valueLookup) {
	h.meetTool = meeting
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
	`the unit items, and never add an aggregate to the items it already summarises.` +
	"\n\n" +
	`Some retrieved sources carry an authority tag (its "stratum" — e.g. "your decision", "external", ` +
	`"superseded", "contested"). When tagged sources differ on the same point, keep them distinct and ` +
	`attribute each to its tag — what you decided versus what an external or unconfirmed source asserts — ` +
	`rather than blending them into one answer.`

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

// degradedChatMessage is the user-facing text sent when the inference backend is
// unavailable (B2 fail-soft). It names the limit plainly and points to the sources
// Hygur retrieved, or asks to retry when there were none.
func degradedChatMessage(hasSources bool) string {
	if hasSources {
		return "AI synthesis is temporarily unavailable. Here are the most relevant sources I found for your question."
	}
	return "AI synthesis is temporarily unavailable. Please try again in a moment."
}

func injectFormatGuidance(messages []llm.Message) []llm.Message {
	// Couche A: the shared prose-voice block rides on the base persona. Chat is
	// streamed, so it gets the voice guidance only (no deterministic post-pass).
	// WP3 Décision 1: state the UNTRUSTED-content rule ONCE here — it applies to
	// every retrieval-bearing turn (both the RAG fast path and the
	// search_knowledge_base tool wrap their excerpts in the matching envelope).
	guidance := todayGuidance() + "\n\n" + retrieval.UntrustedContentRule +
		"\n\n" + baseFormatGuidance + "\n\n" + llm.ProseVoiceGuidance
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

		// Augment the ALREADY-seeded messages (persona + resolved attachments), not
		// req.Messages — else the fast-path would drop injectFormatGuidance and the
		// resolved document text for this turn (#4).
		messages = h.buildMessagesWithContext(messages, prefetchedContext)
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
		// Open contradictions from the DURABLE cache only — never computed here, so
		// the chat path stays cheap + non-blocking. Empty until the digest /
		// Contradictions view has warmed it. Dismissed ones are filtered out.
		var contradictions []contradict.ReconciledConflict
		if js, _, _, found, err := h.chatStore.GetContradictionCache(r.Context(), ""); err == nil && found {
			var all []contradict.ReconciledConflict
			if json.Unmarshal([]byte(js), &all) == nil && len(all) > 0 {
				dismissed, _ := h.chatStore.DismissedContradictions(r.Context())
				for _, c := range all {
					if dismissed[c.Key] {
						continue
					}
					contradictions = append(contradictions, c)
					if len(contradictions) >= contradictionInjectionMax {
						break
					}
				}
			}
		}
		// The user's standing positions (Angle A-2b), read cheaply from the cache the
		// digest warms — no LLM on the chat path; empty until first warmed.
		var positions string
		if text, _, found, err := h.chatStore.GetPositionsSynopsis(r.Context()); err == nil && found {
			positions = text
		}
		messages = injectBrainContext(messages, decisions, positions, synopsis, time.Now().UTC().Format("2006-01-02"), contradictions)
	}

	// Authoritative determined-facts layer (CORE thesis — "take the determined data where it
	// is"). Resolve the query's subjects DETERMINISTICALLY (the owner for first-person framing +
	// the single named subject the query mentions — reused resolution, NO classifier, NO keyword
	// list), assemble their DETERMINED identifiers/claims from the WP36.a dossier IN THE PIPELINE,
	// and inject them as the VERIFIED fact layer. The value-source rule (see determinedFactsRule)
	// then binds identifier VALUES to this layer only — raw excerpts stay untrusted prose. This is
	// ADDITIVE: it never removes retrieval, so non-identifier Q&A is unchanged. Best-effort: any
	// error is logged and the turn proceeds without the layer.
	// determinedValues is the set of engine-verified identifier values for this query's subjects.
	// It is the ONLY membership oracle the output guard trusts: any identifier-grade value the LLM
	// writes that is NOT in this set is unverified and must never be shown (P=0). Stays empty when
	// the engine determined nothing (e.g. "my phone number" with no determined phone) — which is
	// exactly when an invented number in the prose must be caught.
	var determinedValues provenanceValueSet = newProvenanceValueSet()
	if h.factsDB != nil && h.ownerMatcher != nil && latestUserQuery != "" {
		fctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		facts, err := retrieval.AssembleQueryFacts(fctx, h.factsDB, latestUserQuery, time.Now().UTC(), h.ownerMatcher, h.ownerSubject)
		cancel()
		if err != nil {
			h.logger.Debug().Err(err).Msg("determined-facts assembly failed")
		}
		if len(facts) > 0 {
			messages = injectDeterminedFacts(messages, facts)
			determinedValues = determinedValueSet(facts)
		}
	}

	// Server-side ceiling on client-supplied MaxTokens: clip only pathological
	// requests. 0 (backend default) and normal values pass through untouched.
	req.MaxTokens = clampMaxTokens(req.MaxTokens, h.maxTokensCeiling)

	// Build the LLM request
	llmReq := llm.ChatRequest{
		Model:     req.Model,
		Messages:  messages,
		Stream:    true,
		MaxTokens: req.MaxTokens,
		// The user's live Ask answer — the work the chat cap exists to meter (WP16a).
		Category: "chat",
		Pass:     "ask",
	}
	// Map the client-supplied temperature to a pointer only when non-zero.
	// The old float64+omitempty field already dropped 0 on the wire, so this
	// reproduces today's behavior exactly (0 / absent => backend default) while
	// forwarding an explicit non-zero value. We never force 0 on the user chat
	// path — that determinism is reserved for the extraction passes.
	if req.Temperature != 0 {
		llmReq.Temperature = llm.Temp(req.Temperature)
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
	// and the post-tool synthesis wait, no tokens are sent — the SSE connection
	// can sit fully idle for tens of seconds. URLSession/browser EventSource and
	// intervening proxy idle-timeouts then kill the socket → silent "Can't load".
	// The heartbeat below keeps the wire warm. It emits TWO things every
	// keepAliveInterval, both delta-safe:
	//   1. an SSE comment (": ping") — raw-socket liveness; ignored by clients.
	//   2. a `{"type":"working"}` data event — fetch-event-source does NOT deliver
	//      comment lines to onmessage, so a real DATA event is what lets the client
	//      reset its idle timer and show "still working…". It carries no
	//      delta/done/error, so it never touches the token-delta or done contract.
	// A mutex serialises writes between this goroutine and the stream callback.
	var writeMu sync.Mutex
	stopKeepalive := make(chan struct{})
	var stopOnce sync.Once

	go func() {
		ticker := time.NewTicker(keepAliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-stopKeepalive:
				return
			case <-ticker.C:
				writeMu.Lock()
				_, _ = fmt.Fprint(w, ": ping\n\ndata: {\"type\":\"working\"}\n\n")
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

	// VOIE A — the slot-filling socle (SLOT_FILLING_PLAN §1). Before spending an LLM round, try to
	// pre-match the query to a DETERMINED fact the engine has (identifier or F1 figure) via the
	// generic label normalizer. On a match the engine COMPOSES the answer itself and simulated-
	// streams it — the LLM is skipped, so P(the LLM writes the value) = 0 by construction. This is
	// the fix for "the chat ignores the engine" (the 357 € RAG hallucination). No match → the turn
	// falls straight through to voie B (the RAG path below) unchanged.
	if (h.idTool != nil || h.figTool != nil || h.meetTool != nil) && latestUserQuery != "" {
		subjectFn := func(q string) string {
			if h.factsDB == nil {
				return ""
			}
			s, _ := retrieval.DetectQuerySubject(r.Context(), h.factsDB, q)
			return s
		}
		if plan, ok := planVoieA(latestUserQuery, subjectFn); ok {
			if h.serveVoieA(r.Context(), plan, writeSSE, req, latestUserQuery, sessionCtx) {
				stopOnce.Do(func() { close(stopKeepalive) })
				return
			}
		}
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
		// Strip Harmony control framing (e.g. a leaked "<|channel|>final<|message|>") from
		// the streamed answer so it never reaches the UI. One filter per completion.
		harmony := &llm.HarmonyFilter{}

		// Backstop the round with an INDEPENDENT timeout derived from the request
		// context. r.Context() alone is cancelled only on client disconnect — so a
		// model parked mid-round would keep this goroutine (and the socket) alive
		// with zero bytes until an intervening layer silently killed it. On expiry
		// StreamChatRich's context is cancelled, it returns context.DeadlineExceeded,
		// and the post-loop error block emits an honest SSE `error` + a server log.
		roundCtx, cancelRound := context.WithTimeout(r.Context(), synthesisRoundTimeout)
		err := h.llmClient.StreamChatRich(roundCtx, llmReq, func(evt llm.StreamEvent) error {
			// NOTE: do NOT stop the keepalive here. This callback first fires on
			// the tool-call round; the long, SILENT phase is the NEXT round
			// (the model reasoning over tool results before emitting the answer).
			// Stopping now left that gap unguarded → the client idle-timed-out on
			// big/slow answers ("Load failed"). The keepalive runs until the loop
			// ends (final stopOnce below); its SSE heartbeats interleave
			// harmlessly with token data under the shared write lock.
			select {
			case <-roundCtx.Done():
				return roundCtx.Err()
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
				clean := harmony.Feed(evt.Delta)
				if clean != "" {
					roundContent.WriteString(clean)
					assistantBuf.WriteString(clean)
					// P=0 output guard: the user-facing answer is BUFFERED, never streamed
					// token-by-token, so the deterministic guard can scan the whole text for an
					// unverified identifier-grade value BEFORE anything is shown. (The engine's
					// verified value already streams instantly as the determined_answer card, so
					// the prose can afford to arrive as one guarded block.) Emitted after the loop.
				}
			}
			return nil
		})
		cancelRound() // release the round's timeout context (no-op once fired)
		if tail := harmony.Flush(); tail != "" {
			roundContent.WriteString(tail)
			assistantBuf.WriteString(tail)
		}

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
			result, execErr := h.executeToolGuarded(r.Context(), tc.Function.Name, argsRaw)
			if errors.Is(execErr, context.DeadlineExceeded) {
				// Honest server-side trace for a real tool timeout. The error is also
				// fed back to the model (below) so the turn recovers instead of hanging.
				h.logger.Error().Str("tool", tc.Function.Name).Dur("timeout", toolExecTimeout).Msg("tool execution timed out (backstop)")
			}
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
				// WP3 Décision 2: a SideEffect tool was NOT executed — the registry
				// returned a pending-confirmation envelope. Surface it to the client
				// as a Confirm/Cancel card. The `{pending:true}` result also flows
				// back to the model so it stops and asks the user to confirm.
				if h.toolRegistry.IsSideEffect(tc.Function.Name) {
					var pa tools.PendingResult
					if json.Unmarshal(result, &pa) == nil && pa.Pending {
						_ = writeSSE(PendingActionEvent{
							Type:     "pending_action",
							ActionID: pa.ActionID,
							Tool:     tc.Function.Name,
							Preview:  pa.Preview,
						})
					}
				}
				// lookup_identifier is the LANGUAGE-triggered engine path: the model called it
				// because it understood this as a factual-identifier question. Emit the engine's
				// verdict as a `determined_answer` render — value + confidence + sources — BEFORE
				// the model's next round produces prose, so the answer the user sees comes from
				// the deterministic engine and the LLM cannot substitute, hedge, or decline it
				// (cut-LLM-safe). On decline, the render is an honest "no verified value".
				if evt, ok := determinedAnswerFromToolResult(tc.Function.Name, result); ok {
					_ = writeSSE(evt)
					// The engine determined this value via the TOOL (the recall-floor path):
					// it is verified even when the determined-facts LAYER's type-discovery does
					// NOT surface it (e.g. a DUNS the lookup resolves but AssembleQueryFacts does
					// not enumerate). Union the tool verdict into the guard's membership set so a
					// tool-verified value voiced in the prose is ALLOWED, not declined. evt.Value
					// is set ONLY for tier high/med (empty on a "none" decline), so this adds
					// verified values only — genuinely-unverified values still fail closed.
					if evt.Value != "" {
						determinedValues = addToolDeterminedValue(determinedValues, evt.Value)
					}
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

	// P=0 OUTPUT GUARD. The full user-facing answer is now buffered (nothing was streamed live).
	// Scan it deterministically for identifier-grade values and, on any value the engine did NOT
	// determine, replace the whole answer with an honest decline — so no unverified identifier can
	// EVER be shown. Verified values (also on the card) and ordinary numbers pass through. This is
	// the text we emit, persist, and feed to session/memory below.
	// R = the values present in this turn's UNTRUSTED retrieved excerpts. A value the LLM voiced
	// that is NOT determined but IS in R is RETROUVÉ (from a document) → kept but marked; a value in
	// neither is INVENTÉ → stripped. Built here so the firewall has both membership oracles.
	retrievedValues := retrievedValueSet(turnSources)
	answer, guardDeclined := guardAnswer(assistantBuf.String(), determinedValues, retrievedValues)
	if guardDeclined {
		h.logger.Warn().Msg("provenance firewall: invented value (in no source) in answer → honest decline")
	}

	if streamErr != nil {
		// Client disconnect — don't log as error.
		if r.Context().Err() != nil {
			h.logger.Debug().Msg("stream ended due to client disconnect")
			return
		}

		// Graceful degradation (algo-first): when the inference backend is down,
		// don't surface a hard error. The retrieval layer already ran — any
		// sources found this turn were streamed as rag_context events — so send
		// a plain message + a `degraded` marker and let the UI keep the sources
		// panel. No prose, but the facts Hygur found stay visible.
		if errors.Is(streamErr, llm.ErrLLMUnavailable) {
			h.logger.Warn().Err(streamErr).Int("sources", len(turnSources)).
				Msg("chat degraded: LLM unavailable, returning retrieved sources only")
			msg := degradedChatMessage(len(turnSources) > 0)
			answer = msg // canned fail-soft text (no identifier) supersedes any partial buffer
			_ = writeSSE(map[string]any{"delta": msg, "done": false})
			_ = writeSSE(map[string]any{"degraded": true, "done": true})
		} else if errors.Is(streamErr, context.DeadlineExceeded) {
			// The synthesis-round backstop fired: the model sat silent past
			// synthesisRoundTimeout. Leave an honest server trace and emit the SAME
			// SSE `error` shape the client already consumes — never a hang.
			h.logger.Error().Err(streamErr).Dur("timeout", synthesisRoundTimeout).
				Msg("chat synthesis round timed out (backstop) — emitting honest SSE error")
			writeSSEError(w, "TIMEOUT", "The request timed out — please retry.")
			flusher.Flush()
		} else {
			h.logger.Error().Err(streamErr).Msg("chat stream error")
			writeSSEError(w, "LLM_STUDIO_ERROR", streamErr.Error())
			flusher.Flush()
		}
	} else {
		// Emit the guarded answer as the answer delta, then the terminal `done` (with usage,
		// mirroring the pre-tool-loop wire format). The client concatenates `delta` events, so a
		// single guarded block is wire-compatible with the previous token-by-token stream.
		if answer != "" {
			_ = writeSSE(map[string]any{"delta": answer, "done": false})
		}
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
	if req.SessionID != "" && h.chatStore != nil && answer != "" {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		h.persistAssistantTurn(pctx, req.SessionID, answer, turnSources)
		pcancel()
	}

	// "Quand Hygur rêve" Phase 0: stamp access on the items that were CITED this
	// turn — the "useful" signal that feeds future consolidation (docs/DREAM_PLAN.md).
	// Detached + best-effort; never blocks the response. Observe-only: nothing reads
	// this yet. Stamps regardless of SessionID (a transient chat still uses items).
	if h.chatStore != nil && len(turnSources) > 0 {
		ids := make([]string, 0, len(turnSources))
		for _, s := range turnSources {
			if s.ContentID != "" {
				ids = append(ids, s.ContentID)
			}
		}
		if len(ids) > 0 {
			go func() {
				actx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := h.chatStore.BumpItemAccess(actx, ids); err != nil {
					h.logger.Debug().Err(err).Msg("bump item access")
				}
			}()
		}
	}

	// Post-stream: extract entities from the assistant answer and append a
	// ResolvedQuery so the next turn's direct-answer check has fresh context.
	// Skip when the session is transient (no SessionID) or the answer is empty.
	if req.SessionID != "" && answer != "" {
		updateSessionPostSynthesis(sessionCtx, latestUserQuery, answer, req.RecentSourceIDs)
	}

	// Per-turn memory extraction, run synchronously so each autonomous write can
	// be surfaced inline on the just-finished turn (a `memory_write` SSE event)
	// rather than staying buried in the Mind review queue. The extractor calls the
	// LLM (1-3 s); we hold the stream open that bit longer, after the terminal
	// `done`, and emit before returning. Storage semantics are unchanged (rows land
	// pending, source='extracted') — this only surfaces the write.
	h.emitMemoryWrites(writeSSE, latestUserQuery, answer, req.SessionID)
}

// emitMemoryWrites extracts durable memories from the just-finished turn, persists
// them (pending review, as before), and streams one `memory_write` SSE event per
// stored row so the user sees the autonomous write inline in the chat. Uses a
// detached context so the persist still lands if the client drops during the short
// extraction — writeSSE then simply no-ops. Best-effort throughout: any failure is
// logged and never breaks the turn.
func (h *RAGChatHandler) emitMemoryWrites(writeSSE func(any) error, userMsg, assistantMsg, sessionID string) {
	if h.memoryStore == nil || assistantMsg == "" || userMsg == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// WP3 Décision 3: extract from the USER's message only — never the assistant
	// reply, tool results, or document excerpts. The assistantMsg guard above
	// just confirms the turn actually completed before we bother extracting.
	extracted, err := h.memoryStore.ExtractMemoriesFromTurn(ctx, userMsg)
	if err != nil {
		h.logger.Debug().Err(err).Msg("memory extraction failed")
		return
	}
	if len(extracted) == 0 {
		return
	}
	stored, persistErr := h.memoryStore.PersistExtractedReturning(extracted, sessionID)
	logEvt := h.logger.Info()
	if persistErr != nil {
		logEvt = h.logger.Warn().Err(persistErr)
	}
	logEvt.Int("extracted", len(extracted)).Int("stored", len(stored)).
		Str("session_id", sessionID).
		Msg("pending memory candidates persisted from turn")

	for _, m := range stored {
		status := "accepted"
		if m.AcceptedAt == nil {
			status = "pending"
		}
		_ = writeSSE(MemoryWriteEvent{
			Type:       "memory_write",
			MemoryID:   m.MemoryID,
			Content:    m.Content,
			MemoryType: string(m.Type),
			Status:     status,
		})
	}
}

// HandleActionConfirm executes a gated side-effect action after the user
// approved it (WP3, Décision 2). POST /actions/{action_id}/confirm. It takes the
// pending entry (removing it — a confirmation can never be replayed), then runs
// the tool via ExecuteConfirmed. An unknown or expired action_id fail-closes with
// 404/410 and nothing executes. The action_audit log is WP4 — out of scope here.
func (h *RAGChatHandler) HandleActionConfirm(w http.ResponseWriter, r *http.Request) {
	if h.pendingActions == nil || h.toolRegistry == nil {
		http.Error(w, `{"error":"confirmation gate not configured"}`, http.StatusServiceUnavailable)
		return
	}
	actionID := chi.URLParam(r, "action_id")
	if actionID == "" {
		http.Error(w, `{"error":"action_id is required"}`, http.StatusBadRequest)
		return
	}
	pa, ok := h.pendingActions.Take(actionID)
	if !ok {
		// Unknown or expired — fail-closed, nothing runs.
		http.Error(w, `{"error":"pending action not found or expired"}`, http.StatusGone)
		return
	}
	result, err := h.toolRegistry.ExecuteConfirmed(r.Context(), pa.ToolName, pa.Args)
	if err != nil {
		h.logger.Warn().Err(err).Str("tool", pa.ToolName).Str("action_id", actionID).Msg("confirmed action failed")
		http.Error(w, `{"error":"action execution failed"}`, http.StatusInternalServerError)
		return
	}
	h.logger.Info().Str("tool", pa.ToolName).Str("action_id", actionID).Msg("confirmed action executed")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result)
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

// determinedFactsRule is the value-source rule that makes the "Verified facts" block
// authoritative. It is IDENTIFIER-scoped on purpose: it binds reference/identifier VALUES to
// the deterministic layer (closing the hole where the model lifted a mislabeled number off a
// document) WITHOUT touching ordinary Q&A — amounts, quotes, dates of events and summaries are
// still drawn from the retrieved content, so non-identifier answers are unchanged.
const determinedFactsRule = "These values are DETERMINED by Hygur's deterministic resolver — not read from documents. " +
	"Identifier and reference values that belong to a person or organization — a national or registration " +
	"number, a VAT or enterprise number, an IBAN, a DUNS, a SIRET or EIN, a client or reference number, and " +
	"the like — must come ONLY from this \"Verified facts\" block, matched to the user's wording by meaning " +
	"(for example \"TVA\"/\"VAT\" is the enterprise/VAT number). Cite the listed source(s).\n" +
	"You must NEVER state such a number that is not in this block — not one you find in a retrieved document, " +
	"a search result, an email or an attachment. Documents reprint other parties' references and mislabel " +
	"numbers, so any identifier read out of retrieved content is untrustworthy. If the identifier the user " +
	"asks for is not listed above: say plainly you don't have a verified value for it, do NOT substitute a " +
	"different identifier from the documents, and do NOT read or quote any number out of the retrieved content " +
	"— at most name the source document so the user can check it themselves.\n" +
	"This rule is about identifiers and reference numbers only; ordinary prose, monetary amounts, quotes, " +
	"dates of events and summaries are still drawn from the retrieved content as usual."

// keyedClosedWorldRule closes the world over a keyed entity's (a vehicle's) attributes: the attributes
// listed for it in the Verified-facts block are the COMPLETE determined set, so any OTHER attribute of
// that specific vehicle must be honestly declined rather than lifted from retrieved documents. This is
// the anti-conflation guarantee for the vehicle cluster (anchor-or-decline), scoped to the keyed subject
// so ordinary Q&A elsewhere is untouched.
const keyedClosedWorldRule = "  ↳ For THIS vehicle, the attribute(s) listed above are the ONLY verified ones. " +
	"If asked about any other attribute of it — insurance/assurance, lease/loyer/LOA, financing, omnium — " +
	"that is NOT listed here, say plainly you have no verified value for it. Do NOT infer it from retrieved " +
	"documents: a quote/cotation is not a policy, and another vehicle's or the company car's data must never " +
	"be attributed to this one.\n"

// injectDeterminedFacts prepends the authoritative "Verified facts" block (the query's
// subjects' determined identifiers + active claims) plus the value-source rule to the system
// prompt. Values are shown from the deterministic resolver; the LLM only voices them. Merges
// into an existing system message (same pattern as injectMemoriesIntoSystem). No-op on empty.
func injectDeterminedFacts(messages []llm.Message, subjects []retrieval.DeterminedFacts) []llm.Message {
	if len(subjects) == 0 {
		return messages
	}
	var b strings.Builder
	b.WriteString("## Verified facts (authoritative — the ONLY source for identifier values)\n\n")
	b.WriteString(determinedFactsRule)
	b.WriteString("\n")
	for _, s := range subjects {
		if !s.HasFacts() {
			continue
		}
		// PII: never spell the owner's own raw name into the prompt. The model does not need it to
		// voice "your VAT is X" — the values below are already scoped to the owner. A named
		// non-owner subject keeps its label (the user asked about them, so it is not new PII).
		header := s.Subject.Norm
		if s.IsOwner {
			header = "the user (owner)"
		}
		b.WriteString("\n### ")
		b.WriteString(header)
		b.WriteString("\n")
		for _, id := range s.Identity {
			val := id.Raw
			if strings.TrimSpace(val) == "" {
				val = id.Value
			}
			b.WriteString(fmt.Sprintf("- %s: %s (%s confidence", id.Label, val, id.Tier))
			if titles := sourceTitles(id.Sources); titles != "" {
				b.WriteString("; sources: ")
				b.WriteString(titles)
			}
			b.WriteString(")\n")
		}
		for _, c := range s.Claims {
			b.WriteString(fmt.Sprintf("- %s: %s (%s, %d source(s))\n", c.Attribute, c.Value, c.State, c.Corroboration))
		}
		// CLOSED-WORLD for a keyed entity (a vehicle by its plate): its verified attributes above are the
		// COMPLETE determined set for THIS specific entity. Any other attribute of it — insurance, lease
		// / loyer, financing — that is NOT listed must be DECLINED, never inferred from retrieved content,
		// which conflates distinct vehicles (a price QUOTE, the company car, another plate). This is what
		// stops « votre assurance » being answered with a different vehicle's or a mere offer. (voie A owns
		// this keyed entity; RAG must not fill its gaps.)
		if s.Subject.Type == "vehicle" {
			b.WriteString(keyedClosedWorldRule)
		}
		for _, f := range s.Figures {
			amt := f.Raw
			if strings.TrimSpace(amt) == "" {
				amt = f.Value
			}
			ctx := make([]string, 0, 2)
			if f.Direction != "" {
				ctx = append(ctx, f.Direction)
			}
			if f.Period != "" {
				ctx = append(ctx, f.Period)
			}
			line := fmt.Sprintf("- figure %s", f.Label)
			if len(ctx) > 0 {
				line += " (" + strings.Join(ctx, ", ") + ")"
			}
			line += fmt.Sprintf(": %s %s", amt, f.Unit)
			if titles := sourceTitles(f.Sources); titles != "" {
				line += "; sources: " + titles
			}
			b.WriteString(strings.TrimRight(line, " ") + "\n")
		}
	}
	factsBlock := strings.TrimRight(b.String(), "\n")

	hasSystem := len(messages) > 0 && messages[0].Role == "system"
	out := make([]llm.Message, 0, len(messages)+1)
	if hasSystem {
		out = append(out, llm.Message{
			Role:    "system",
			Content: messages[0].Content + "\n\n" + factsBlock,
		})
		out = append(out, messages[1:]...)
	} else {
		out = append(out, llm.Message{Role: "system", Content: factsBlock})
		out = append(out, messages...)
	}
	return out
}

// sourceTitles renders up to three source titles for a determined identifier, so the model
// can cite where the value comes from. Empty titles fall back to the content id.
func sourceTitles(sources []fact.Source) string {
	if len(sources) == 0 {
		return ""
	}
	const max = 3
	parts := make([]string, 0, max)
	for _, s := range sources {
		if len(parts) >= max {
			break
		}
		t := strings.TrimSpace(s.Title)
		if t == "" {
			t = s.ContentID
		}
		if t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "; ")
}

// brainContextCharBudget backstops the size of the injected brain block (~700 tokens
// at ~4 chars/token). Sections are added in priority order — what's TRUE first, the
// running narrative last — so a tight budget drops the least-critical section, never
// the standing facts (#2: keeps the system prompt from ballooning on the 12B model).
const brainContextCharBudget = 2800

// injectBrainContext prepends a compact, grounded block of Hygur's own signals so the
// assistant can reference "you decided X" / the ongoing story even when document
// retrieval didn't surface them. Cheap (no LLM), grounded in stored state. asOf stamps
// the cached signals as a snapshot (#5). Sections are budget-bounded and the standing
// facts are shown ONCE — the synthesized positions when present, else the raw decision
// list, never both (#3).
func injectBrainContext(messages []llm.Message, decisions []*store.Decision, positions, synopsis, asOf string, contradictions []contradict.ReconciledConflict) []llm.Message {
	positions = strings.TrimSpace(positions)
	synopsis = strings.TrimSpace(synopsis)
	if len(decisions) == 0 && positions == "" && synopsis == "" && len(contradictions) == 0 {
		return messages
	}
	stamp := ""
	if asOf != "" {
		stamp = " (as of " + asOf + ")"
	}

	// Sections in priority order: standing facts → active tensions → narrative.
	var sections []string

	// Standing facts: the synthesized positions (A-2b, cached → stamped) when present,
	// otherwise the raw standing-decision list (live) — never both (#3).
	if positions != "" {
		sections = append(sections, "## Where the user stands (from their confirmed decisions"+stamp+")\n\n"+positions+"\n")
	} else if len(decisions) > 0 {
		var d strings.Builder
		d.WriteString("## The user's standing decisions\n\n")
		for _, dec := range decisions {
			d.WriteString("- ")
			d.WriteString(dec.Statement)
			if len(dec.DecidedOn) >= 10 {
				d.WriteString(" (")
				d.WriteString(dec.DecidedOn[:10])
				d.WriteString(")")
			}
			d.WriteString("\n")
		}
		sections = append(sections, d.String())
	}

	// Open contradictions (durable cache → stamped).
	if len(contradictions) > 0 {
		var c strings.Builder
		c.WriteString("## Open contradictions in the user's records" + stamp + "\n\n")
		for _, cf := range contradictions {
			c.WriteString("- ")
			if cf.Entity != "" {
				c.WriteString(cf.Entity)
				c.WriteString(" — ")
			}
			c.WriteString(cf.Attribute)
			vals := make([]string, 0, len(cf.Members))
			for _, m := range cf.Members {
				if m.Value != "" {
					vals = append(vals, m.Value)
				}
			}
			if len(vals) > 0 {
				c.WriteString(": ")
				c.WriteString(strings.Join(vals, " vs "))
			}
			if cf.Verdict.Reason != "" {
				c.WriteString(" — ")
				c.WriteString(cf.Verdict.Reason)
			}
			c.WriteString("\n")
		}
		sections = append(sections, c.String())
	}

	// The rolling life synopsis (nightly cache → stamped). Narrative context — lowest
	// priority, so the first to be dropped under budget.
	if synopsis != "" {
		sections = append(sections, "## The story so far"+stamp+"\n\n"+synopsis+"\n")
	}

	// Assemble within the budget: always keep the first (highest-priority) section;
	// append the rest only while they fit (#2).
	var b strings.Builder
	for _, s := range sections {
		if b.Len() > 0 && b.Len()+len(s) > brainContextCharBudget {
			break
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(s)
	}
	block := b.String()
	if block == "" {
		return messages
	}

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

// buildMessagesWithContext injects RAG context into the message list.
// sourceStratum returns the authority lens label for a RAG source (A-1 multi-lens),
// or "" for the baseline (the user's own current capture). Validity (superseded /
// contested) takes precedence over tier, so a stale or contested item is never shown
// as authoritative.
func sourceStratum(s RAGSource) string {
	return retrieval.StratumLabel(
		retrieval.AuthorityTier(s.Tier), retrieval.Validity(s.Validity), retrieval.OwnerOrigin(s.OwnerOrigin),
	)
}

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
		if tag := sourceStratum(source); tag != "" {
			header += " [" + tag + "]"
		}
		contextBuilder.WriteString(header + "\n")
		// WP3 Décision 1: each excerpt is attacker-controllable content (a mail,
		// a document). Wrap it in the uniform UNTRUSTED envelope so the model
		// treats it as data, not instructions. The rule is stated once in the
		// system prompt (see injectFormatGuidance).
		contextBuilder.WriteString(retrieval.WrapUntrusted(source.Excerpt))
		contextBuilder.WriteString("\n\n")
	}

	contextBuilder.WriteString("---\nCite les sources avec [Document N], [Email N] ou [Note N] quand tu utilises ces informations.")
	contextBuilder.WriteString("\nEach source is tagged by authority: a decision you confirmed [your decision] outranks your own captures, which outrank [external] third-party sources; [superseded] and [contested] sources are weaker. When tagged sources disagree on the same point, keep them distinct — state what you decided, what external sources assert, and what is unconfirmed — and attribute each to its tag rather than blending them into one answer.")

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
			ContentID:  s.ContentID,
			SourceType: s.SourceType,
			Title:      s.Title,
			// Strip the WP3 UNTRUSTED envelope for the UI sources panel — the
			// markers belong in the prompt copy, not on screen.
			Excerpt:     retrieval.UnwrapUntrusted(s.Excerpt),
			Score:       s.Score,
			MailFrom:    s.MailFrom,
			MailDate:    s.MailDate,
			MailSubject: s.MailSubject,
			Date:        s.Date,
		})
	}
	return out
}

// executeToolGuarded runs a tool with an independent timeout backstop. The tool
// runs on a detached goroutine and we select on the deadline, so even a tool that
// ignores its context can never hang the request: on expiry we return
// context.DeadlineExceeded immediately (the tool goroutine, if truly stuck, is
// left to finish and be GC'd — a leaked goroutine is strictly better than a
// wedged chat turn). The returned error flows back to the model as a tool error
// so the turn recovers rather than stalling.
func (h *RAGChatHandler) executeToolGuarded(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	toolCtx, cancel := context.WithTimeout(ctx, toolExecTimeout)
	defer cancel()

	type toolResult struct {
		out json.RawMessage
		err error
	}
	done := make(chan toolResult, 1) // buffered so a late finish never blocks
	go func() {
		out, err := h.toolRegistry.Execute(toolCtx, name, args)
		done <- toolResult{out, err}
	}()

	select {
	case <-toolCtx.Done():
		return nil, toolCtx.Err()
	case res := <-done:
		return res.out, res.err
	}
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
