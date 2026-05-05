package handlers

import (
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/session"
)

// lastUserMessage returns the content of the most recent user-role message.
// Returns "" when there are no user messages.
func lastUserMessage(messages []llm.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

// mergeSourcesIntoSession runs Tier 1 entity extraction over each retrieved
// source's excerpt and merges the results into the session context.
func mergeSourcesIntoSession(ctx *session.SessionContext, sources []RAGSource) {
	if ctx == nil {
		return
	}
	for _, s := range sources {
		if s.Excerpt == "" {
			continue
		}
		for _, e := range session.ExtractEntities(s.Excerpt, s.ContentID) {
			ctx.AddEntity(e)
		}
	}
}

// updateSessionPostSynthesis records the (question, answer) pair, extracts
// entities from the assistant's natural-language answer, and uses the user's
// raw query as the topic seed (cheap heuristic — no LLM call). The entities
// extracted from the assistant answer carry source="" because we cannot
// attribute them to a single content_id post-synthesis.
func updateSessionPostSynthesis(ctx *session.SessionContext, question, answer string, sourceIDs []string) {
	if ctx == nil {
		return
	}
	answerEntities := session.ExtractEntities(answer, "")
	for _, e := range answerEntities {
		ctx.AddEntity(e)
	}
	rq := session.ResolvedQuery{
		Question:  question,
		Answer:    answer,
		SourceIDs: sourceIDs,
		Entities:  answerEntities,
		AskedAt:   time.Now(),
	}
	ctx.AppendResolvedQuery(rq, question)
}

// buildRAGContextFromResults wraps a list of UnifiedResult records (from
// FetchByContentIDs) into a RAGContext shape that the rest of the chat
// handler treats as if it came from vector search. Used by the
// session-context direct-source fast-path: we still let the LLM generate
// the answer, but we ground it on pre-fetched cached sources rather than
// re-running embed → search → rewrite (the slow path).
func buildRAGContextFromResults(query string, results []retrieval.UnifiedResult) *RAGContext {
	sources := make([]RAGSource, 0, len(results))
	totalChars := 0
	for _, r := range results {
		sources = append(sources, RAGSource{
			ContentID:   r.ContentID,
			SourceType:  r.SourceType,
			Title:       r.Title,
			Excerpt:     r.Excerpt,
			Score:       r.Score,
			MailFrom:    r.MailFrom,
			MailDate:    r.MailDate,
			MailSubject: r.MailSubject,
		})
		totalChars += len(r.Excerpt)
	}
	return &RAGContext{
		Query:      query,
		Sources:    sources,
		TotalChars: totalChars,
	}
}

// DebugEvent is the JSON shape streamed as `data: {…}\n\n` when the client
// asks for `?debug=1` or sends `X-Hygur-Debug: 1`. It carries the scoring and
// session telemetry for the current chat turn.
type DebugEvent struct {
	Type               string              `json:"type"` // always "debug"
	OriginalQuery      string              `json:"original_query"`
	RewrittenQuery     string              `json:"rewritten_query,omitempty"`
	SessionContextUsed string              `json:"session_context_used"` // "direct_answer" | "context_injected" | "none"
	Search             *SearchDebugSummary `json:"search,omitempty"`
}

// SearchDebugSummary mirrors retrieval.SearchDebugInfo but uses public types
// so the handler tests don't need to import internal retrieval types just to
// construct expected values.
type SearchDebugSummary struct {
	ScoringMode             string           `json:"scoring_mode"`
	HasTemporalMarker       bool             `json:"has_temporal_marker"`
	PreFilterDays           int              `json:"pre_filter_days"`
	PreFilterApplied        bool             `json:"pre_filter_applied"`
	PreFilterFallback       bool             `json:"pre_filter_fallback"`
	QueryEntityType         string           `json:"query_entity_type,omitempty"`
	CandidatePoolPreFilter  int              `json:"candidate_pool_pre_filter"`
	CandidatePoolPostFilter int              `json:"candidate_pool_post_filter"`
	PerResult               []ResultDebugDTO `json:"per_result"`
}

type ResultDebugDTO struct {
	ContentID     string   `json:"content_id"`
	Title         string   `json:"title"`
	Date          string   `json:"date,omitempty"`
	AgeDays       float64  `json:"age_days"`
	SemanticScore float64  `json:"semantic_score"`
	RecencyScore  float64  `json:"recency_score"`
	FinalScore    float64  `json:"final_score"`
	HighPriority  bool     `json:"high_priority"`
	BoostsApplied []string `json:"boosts_applied,omitempty"`
}

// buildDebugEvent assembles the debug payload from a populated RAGContext.
// Returns nil if no debug info was collected.
func buildDebugEvent(ctx *RAGContext, sessionMode string) DebugEvent {
	evt := DebugEvent{
		Type:               "debug",
		OriginalQuery:      ctx.Query,
		RewrittenQuery:     ctx.Rewritten,
		SessionContextUsed: sessionMode,
	}
	if ctx.Debug == nil {
		return evt
	}
	per := make([]ResultDebugDTO, 0, len(ctx.Debug.PerResult))
	for _, r := range ctx.Debug.PerResult {
		per = append(per, ResultDebugDTO{
			ContentID:     r.ContentID,
			Title:         r.Title,
			Date:          r.Date,
			AgeDays:       r.AgeDays,
			SemanticScore: r.SemanticScore,
			RecencyScore:  r.RecencyScore,
			FinalScore:    r.FinalScore,
			HighPriority:  r.HighPriority,
			BoostsApplied: r.BoostsApplied,
		})
	}
	evt.Search = &SearchDebugSummary{
		ScoringMode:             ctx.Debug.ScoringMode,
		HasTemporalMarker:       ctx.Debug.HasTemporalMarker,
		PreFilterDays:           ctx.Debug.PreFilterDays,
		PreFilterApplied:        ctx.Debug.PreFilterApplied,
		PreFilterFallback:       ctx.Debug.PreFilterFallback,
		QueryEntityType:         ctx.Debug.QueryEntityType,
		CandidatePoolPreFilter:  ctx.Debug.CandidatePoolPreFilter,
		CandidatePoolPostFilter: ctx.Debug.CandidatePoolPostFilter,
		PerResult:               per,
	}
	return evt
}
