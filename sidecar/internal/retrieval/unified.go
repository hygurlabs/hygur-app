// Package retrieval provides semantic search across knowledge base and mail.
package retrieval

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/intent"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

// hebbianMinWeight is the minimum recency-decayed NPMI association weight for an
// entity_edges neighbour to be folded into the entity lens. NPMI ∈ [-1,1]; 0.15 keeps
// clearly positively-associated pairs and drops weak/hub links (DREAM Phase D §3.3).
const hebbianMinWeight = 0.15

// UnifiedResult represents a single search result from the unified search.
type UnifiedResult struct {
	ChunkID    string         `json:"chunk_id"`
	ContentID  string         `json:"content_id"`
	SourceType string         `json:"source_type"`
	Score      float64        `json:"score"`
	Excerpt    string         `json:"excerpt"`
	Title      string         `json:"title"`
	Date       string         `json:"date,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	// Authority annotation (M1a) — deterministic tier/validity from the decision
	// graph, set by annotateAuthority just before return. Empty when unannotated.
	Tier        AuthorityTier `json:"tier,omitempty"`
	Validity    Validity      `json:"validity,omitempty"`
	OwnerOrigin OwnerOrigin   `json:"owner_origin,omitempty"` // owner vs external (Porto attribution)
	// Mail-specific
	MailFrom    string `json:"mail_from,omitempty"`
	MailDate    string `json:"mail_date,omitempty"`
	MailSubject string `json:"mail_subject,omitempty"`
	// ParsedDate is used internally for sorting; not JSON-exported.
	ParsedDate time.Time `json:"-"`
}

// FocusScope narrows retrieval to documents linked to specific projects or
// carrying specific tags. When both lists are non-empty, the union is used —
// a document is in scope if it matches any project_id OR any tag_id.
type FocusScope struct {
	ProjectIDs []string `json:"project_ids,omitempty"`
	TagIDs     []string `json:"tag_ids,omitempty"`
}

// IsEmpty reports whether the scope carries no project or tag — in which case
// callers should skip the filter altogether and run a normal unscoped search.
func (f *FocusScope) IsEmpty() bool {
	return f == nil || (len(f.ProjectIDs) == 0 && len(f.TagIDs) == 0)
}

func mapKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// maxSectionExcerpt bounds how much of a section's full_text is returned as an
// excerpt, so a pathologically large block can't blow up the LLM context.
const maxSectionExcerpt = 6000

// clampText truncates s to at most max bytes, stepping back to a UTF-8 rune
// boundary, and appends an ellipsis when it had to cut.
func clampText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

// UnifiedSearchRequest represents a request to the unified search.
type UnifiedSearchRequest struct {
	Query    string                        `json:"query"`
	TopK     int                           `json:"top_k,omitempty"`
	Sources  []intent.SourceType           `json:"sources,omitempty"`
	Weights  map[intent.SourceType]float64 `json:"weights,omitempty"`
	DateFrom *time.Time                    `json:"date_from,omitempty"`
	DateTo   *time.Time                    `json:"date_to,omitempty"`
	// MailAccountID restricts mail search to a specific account.
	MailAccountID string `json:"mail_account_id,omitempty"`
	// MailLabels restricts mail search to threads matching any of these Gmail label IDs.
	MailLabels []string `json:"mail_labels,omitempty"`
	// PriorSourceBoost contains content_ids from recently cited sources.
	// Items in this list receive a ×1.10 boost after cosine scoring, acting as
	// a secondary signal for conversational continuity alongside query rewriting.
	PriorSourceBoost []string `json:"prior_source_boost,omitempty"`

	// FocusScope, when non-empty, restricts the candidate pool to documents
	// linked to at least one of the listed projects OR tagged with at least
	// one of the listed tags. Applied as a hard filter on top of the normal
	// retrieval pipeline (entity branch, temporal branch, vector branch).
	FocusScope *FocusScope `json:"focus_scope,omitempty"`

	// ScoringMode selects how recency is blended with semantic similarity.
	// "additive" (default) → final = sem*(1-Wr) + recency*Wr
	// "multiplicative"     → legacy: final = sem * freshness_factor
	// Empty string falls back to "additive".
	ScoringMode string `json:"scoring_mode,omitempty"`

	// CurrentStateFilterDays restricts the candidate pool to documents from the
	// last N days when the query is detected as a "current state" question.
	// 0 disables the pre-filter. The filter falls back to full history if no
	// document survives, and the response logs `temporal_filter_fallback=true`.
	CurrentStateFilterDays int `json:"current_state_filter_days,omitempty"`

	// Debug toggles SearchDebugInfo population on the response. Off by default
	// to avoid the extra allocations in the hot path.
	Debug bool `json:"debug,omitempty"`
}

// UnifiedSearchStats contains statistics about the search operation.
type UnifiedSearchStats struct {
	TotalResults     int           `json:"total_results"`
	KnowledgeResults int           `json:"knowledge_results"`
	MailResults      int           `json:"mail_results"`
	SearchDuration   time.Duration `json:"search_duration_ms"`
}

// SearchDebugInfo carries per-search diagnostics — populated only when
// UnifiedSearchRequest.Debug is true. Intentionally separate from
// UnifiedSearchStats so that adding fields here doesn't bloat the normal
// response payload.
type SearchDebugInfo struct {
	ScoringMode             string        `json:"scoring_mode"`
	HasTemporalMarker       bool          `json:"has_temporal_marker"`
	PreFilterDays           int           `json:"pre_filter_days"`
	PreFilterApplied        bool          `json:"pre_filter_applied"`
	PreFilterFallback       bool          `json:"pre_filter_fallback"`
	QueryEntityType         string        `json:"query_entity_type,omitempty"`
	CandidatePoolPreFilter  int           `json:"candidate_pool_pre_filter"`
	CandidatePoolPostFilter int           `json:"candidate_pool_post_filter"`
	PerResult               []ResultDebug `json:"per_result"`
}

// ResultDebug captures the scoring trace for a single result, in the order
// boosts/blends were applied.
type ResultDebug struct {
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

// UnifiedSearchResponse represents the response from unified search.
type UnifiedSearchResponse struct {
	Results     []UnifiedResult    `json:"results"`
	Intent      *intent.Intent     `json:"intent,omitempty"`
	SearchStats UnifiedSearchStats `json:"search_stats"`
	Debug       *SearchDebugInfo   `json:"debug,omitempty"`
}

// UnifiedSearcher provides semantic search across knowledge base and mail.
type UnifiedSearcher struct {
	store    *store.DB
	llm      *llm.Client
	detector *intent.Detector

	// LLM-driven retrieval flags. Defaults are off / safe so existing callers
	// that don't opt in keep the legacy vector-only behavior.
	useLLMIntent         bool
	useJudge             bool
	entitySearchFallback bool
	entitySearchMinScore float64

	// M2 authority re-score (off by default → annotate-only, no ranking change).
	useAuthorityRerank bool
	authorityWeights   AuthorityWeights

	// P-2 attention re-score (off by default): a small boost for often/recently-used
	// items, read from the item_access bus.
	useAttentionRerank bool
	// useSalienceRerank boosts by the composite item_signals.salience (recycle).
	useSalienceRerank bool
	// useEntityConsolidation triggers the entity lens deterministically (no LLM
	// classifier) when the query names a known entity — for "about X" consolidation.
	useEntityConsolidation bool

	// P-2 imminence re-score (off by default): a small boost for items tied to an
	// obligation due very soon. The imminent content-id set is supplied by a provider
	// (the prospection scan) and cached with a TTL so the scan never runs on the hot
	// query path more than once per window.
	useImminenceRerank bool
	imminentFn         func(context.Context) map[string]struct{}
	imminentMu         sync.Mutex
	imminentCache      map[string]struct{}
	imminentExpires    time.Time

	// Brick 1 associative entity lens (off by default): EntitySearch also folds in
	// items whose cached claims mention the queried entity. No-op until the
	// entity_mentions index is populated.
	useEntityIndex bool

	// Brick 2 synonymy expansion (off by default): embed the queried entity and
	// fold in entity_norms within entitySynonymyThreshold cosine. Requires
	// useEntityIndex; a no-op until entity_vectors is populated.
	useEntitySynonymy       bool
	entitySynonymyThreshold float64

	// Brick 3 Hebbian expansion (off by default): fold in entity_edges co-occurrence
	// neighbours. Requires useEntityIndex; a no-op until the graph is populated.
	// Kill-switched: OFF leaves retrieval byte-identical.
	useHebbianExpansion bool
}

// SetAttentionRerank enables the P-2 attention re-score (boost often/recently-cited
// items). Off by default; a no-op until the item_access bus has data.
func (us *UnifiedSearcher) SetAttentionRerank(on bool) { us.useAttentionRerank = on }

// SetSalienceRerank toggles the composite-salience rerank lens — the recycle of the
// importance signal into SAIT ranking. Off by default; no-op until items are scored.
func (us *UnifiedSearcher) SetSalienceRerank(on bool) { us.useSalienceRerank = on }

// SetEntityConsolidation toggles deterministic entity-anchored retrieval (the dormant
// entity lens, triggered by the index instead of the LLM classifier). Off by default.
func (us *UnifiedSearcher) SetEntityConsolidation(on bool) { us.useEntityConsolidation = on }

// SetHebbianExpansion enables Phase D associative expansion (fold entity_edges
// co-occurrence neighbours into the entity lens). Off by default; a no-op until the
// graph is populated. OFF ⇒ retrieval is byte-identical to before.
func (us *UnifiedSearcher) SetHebbianExpansion(on bool) { us.useHebbianExpansion = on }

// SetImminenceRerank enables the P-2 imminence re-score (boost items tied to a soon-due
// obligation). Off by default; a no-op until an imminent-ids provider is wired and
// returns a non-empty set.
func (us *UnifiedSearcher) SetImminenceRerank(on bool) { us.useImminenceRerank = on }

// SetImminentIDsFunc wires the provider that computes the set of content_ids tied to
// an imminent obligation (the prospection scan). Its result is cached with a TTL.
func (us *UnifiedSearcher) SetImminentIDsFunc(fn func(context.Context) map[string]struct{}) {
	us.imminentMu.Lock()
	us.imminentFn = fn
	us.imminentCache = nil // force a refresh on next use
	us.imminentMu.Unlock()
}

// SetAuthorityRerank enables the M2 authority re-score (boost what "fait foi",
// demote the superseded loser, surface unresolved conflicts). Off by default so
// existing callers keep pure-relevance order; uses DefaultAuthorityWeights.
func (us *UnifiedSearcher) SetAuthorityRerank(on bool) {
	us.useAuthorityRerank = on
	if on && (us.authorityWeights == AuthorityWeights{}) {
		us.authorityWeights = DefaultAuthorityWeights()
	}
}

// NewUnifiedSearcher creates a new UnifiedSearcher instance.
func NewUnifiedSearcher(s *store.DB, l *llm.Client) *UnifiedSearcher {
	return &UnifiedSearcher{
		store:                s,
		llm:                  l,
		detector:             intent.NewDetector(),
		entitySearchFallback: true,
		entitySearchMinScore: 0.5,
	}
}

// NewUnifiedSearcherWithDetector creates a new UnifiedSearcher with a custom detector.
func NewUnifiedSearcherWithDetector(s *store.DB, l *llm.Client, detector *intent.Detector) *UnifiedSearcher {
	return &UnifiedSearcher{
		store:                s,
		llm:                  l,
		detector:             detector,
		entitySearchFallback: true,
		entitySearchMinScore: 0.5,
	}
}

// RetrievalOptions tunes the LLM-assisted branches of the searcher. Use
// SetRetrievalOptions to install a non-default configuration after construction
// (typically wired from config.RetrievalConfig in main.go).
type RetrievalOptions struct {
	UseLLMIntent            bool
	UseJudge                bool
	EntitySearchFallback    bool
	EntitySearchMinScore    float64
	AuthorityRerank         bool    // M2: re-score by authority (boost what "fait foi")
	AttentionRerank         bool    // P-2: re-score by attention (boost often/recently-used)
	ImminenceRerank         bool    // P-2: re-score by imminence (boost soon-due obligations)
	EntityIndex             bool    // brick 1: associative entity lens in EntitySearch
	EntitySynonymy          bool    // brick 2: embedding synonymy expansion
	EntitySynonymyThreshold float64 // brick 2: min cosine (default 0.80 if <= 0)
	HebbianExpansion        bool    // brick 3 (Phase D): fold entity_edges neighbours (default off)
	SalienceRerank          bool    // recycle: boost results by composite item_signals.salience (default off)
	EntityConsolidation     bool    // deterministic entity-anchored retrieval for "about X" queries (default off)
}

// SetRetrievalOptions installs LLM-driven retrieval flags. Pass values from
// the loaded YAML/env config so the flags can be flipped without rebuild.
// EntitySearchMinScore ≤ 0 keeps the current value (defaults to 0.5).
func (us *UnifiedSearcher) SetRetrievalOptions(opts RetrievalOptions) {
	us.useLLMIntent = opts.UseLLMIntent
	us.useJudge = opts.UseJudge
	us.entitySearchFallback = opts.EntitySearchFallback
	if opts.EntitySearchMinScore > 0 {
		us.entitySearchMinScore = opts.EntitySearchMinScore
	}
	us.SetAuthorityRerank(opts.AuthorityRerank)
	us.SetAttentionRerank(opts.AttentionRerank)
	us.SetImminenceRerank(opts.ImminenceRerank)
	us.useEntityIndex = opts.EntityIndex
	us.useEntitySynonymy = opts.EntitySynonymy
	us.entitySynonymyThreshold = opts.EntitySynonymyThreshold
	us.useHebbianExpansion = opts.HebbianExpansion
	us.useSalienceRerank = opts.SalienceRerank
	us.useEntityConsolidation = opts.EntityConsolidation
	if us.entitySynonymyThreshold <= 0 {
		us.entitySynonymyThreshold = 0.80
	}
}

// Search performs a semantic search across knowledge base and mail.
// Freshness is a first-class signal applied to all result types by default.
func (us *UnifiedSearcher) Search(ctx context.Context, req UnifiedSearchRequest) (*UnifiedSearchResponse, error) {
	startTime := time.Now()

	if req.Query == "" {
		return &UnifiedSearchResponse{
			Results:     []UnifiedResult{},
			SearchStats: UnifiedSearchStats{},
		}, nil
	}

	if req.TopK <= 0 {
		req.TopK = DefaultTopK
	}

	log.Printf("[UnifiedSearch] query=%q topK=%d", req.Query, req.TopK)

	// Detect intent
	var detectedIntent *intent.Intent
	weights := req.Weights
	sources := req.Sources

	if len(sources) == 0 {
		detected := us.detector.Detect(req.Query)
		detectedIntent = &detected
		sources = detected.Sources
		if weights == nil {
			weights = detected.Weights
		}
	}

	// NOTE: we deliberately do NOT auto-apply a hard date range from a temporal
	// intent (e.g. "avril", "2026"). A year/month in a query usually refers to
	// the SUBJECT (fiscal period, deadline) rather than the document's received
	// date, so a hard mail-date filter wrongly drops relevant docs (e.g. a TVA
	// 2026 notice received in 2025, or a deadline mail dated outside the month).
	// Temporal relevance is handled by lexical/semantic matching + additive
	// recency. Explicit req.DateFrom/DateTo from an API caller are still honored.

	if weights == nil {
		weights = intent.DefaultWeights
	}

	searchKnowledge := false
	searchMail := false
	for _, s := range sources {
		switch s {
		case intent.SourceKnowledge:
			searchKnowledge = true
		case intent.SourceMail:
			searchMail = true
		case intent.SourceAll:
			searchKnowledge = true
			searchMail = true
		}
	}
	if !searchKnowledge && !searchMail {
		searchKnowledge = true
		searchMail = true
	}
	// The lexical (FTS) signal spans every source, so when the request narrows
	// to a single family we must drop out-of-family hits it surfaces.
	mailOnly := searchMail && !searchKnowledge
	knowledgeOnly := searchKnowledge && !searchMail

	if us.llm == nil {
		return nil, ErrLLMClientRequired
	}

	// FocusScope resolution: when set, compute the allow-list of content_ids
	// upfront. An empty allow-list (scope set but matched zero docs) shortcuts
	// to an empty response — there's nothing to retrieve in this project/tag.
	// `focusAllowList == nil` means the filter is disabled (normal search).
	var focusAllowList map[string]struct{}
	focusActive := !req.FocusScope.IsEmpty()
	if focusActive {
		ids, err := us.store.ResolveFocusContentIDs(ctx, req.FocusScope.ProjectIDs, req.FocusScope.TagIDs)
		if err != nil {
			return nil, fmt.Errorf("resolve focus scope: %w", err)
		}
		log.Printf("[UnifiedSearch] focus_scope active: projects=%v tags=%v matched_docs=%d",
			req.FocusScope.ProjectIDs, req.FocusScope.TagIDs, len(ids))
		if len(ids) == 0 {
			return &UnifiedSearchResponse{
				Results:     []UnifiedResult{},
				Intent:      detectedIntent,
				SearchStats: UnifiedSearchStats{SearchDuration: time.Since(startTime)},
			}, nil
		}
		focusAllowList = make(map[string]struct{}, len(ids))
		for _, id := range ids {
			focusAllowList[id] = struct{}{}
		}
	}

	// Launch the LLM intent classifier in parallel with embedding generation.
	// On classifier timeout/error we fall back to the vector-only path so the
	// search never breaks because of a transient LLM hiccup. Disabled by
	// default; enable via retrieval.use_llm_intent.
	var (
		llmIntent     *QueryIntent
		llmIntentDone = make(chan struct{})
	)
	if us.useLLMIntent {
		go func() {
			defer close(llmIntentDone)
			qi, err := ClassifyQuery(ctx, us.llm, req.Query)
			if err != nil {
				log.Printf("[UnifiedSearch] classify error (falling back to vector): %v", err)
				return
			}
			llmIntent = qi
			log.Printf("[UnifiedSearch] classified: category=%s entity=%q attribute=%q",
				qi.Category, qi.Entity, qi.Attribute)
		}()
	} else {
		close(llmIntentDone)
	}

	// NOTE: LLM query expansion was removed. The hybrid retriever now handles
	// short/exact queries via the BM25/FTS lexical side, and on reasoning models
	// the rewrite leaked the model's entire chain-of-thought into the embedding
	// input (garbage vector + ~18s latency). Embed the raw query directly.
	embedding, err := us.llm.GenerateEmbedding(ctx, req.Query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Wait for the classifier (parallel). Worst case its 20s timeout has
	// already elapsed; the channel close lets us continue immediately.
	<-llmIntentDone

	// Deterministic entity consolidation (brick 2a): detect a known subject entity in
	// the query (index match, NO LLM). We do NOT take the entity early-return — we let
	// the semantic fusion run, then union the subject's literal items afterward, so the
	// full connected set (semantic + literal) surfaces. Off by default (kill-switch).
	var consolidationEntity string
	if us.useEntityConsolidation && us.store != nil {
		if ent, derr := detectQuerySubject(ctx, us.store, req.Query); derr == nil && ent != "" {
			consolidationEntity = ent
			log.Printf("[entity-consolidation] deterministic subject=%q", ent)
		}
	}

	// Routing branch 1 — FactualEntity with a non-empty entity name.
	// EntitySearch is a structured lookup (no similarity blending) — it's the
	// right answer for "numéro national d'Jean", "IBAN du fournisseur X".
	// If it returns nothing or only weak hits, behavior depends on
	// entitySearchFallback: true → fall through to vector; false → abstain.
	if llmIntent != nil && llmIntent.Category == IntentFactualEntity && strings.TrimSpace(llmIntent.Entity) != "" {
		log.Printf("[UnifiedSearch] routed to entity_search (entity=%q attribute=%q)",
			llmIntent.Entity, llmIntent.Attribute)
		entityOpts := EntitySearchOptions{TopK: req.TopK, UseEntityIndex: us.useEntityIndex}
		if focusActive {
			entityOpts.AllowedContentIDs = mapKeys(focusAllowList)
		}
		// Brick 2 — embed the queried entity and fold its cosine-near entities into
		// the index lookup (FR↔EN / surface-different mentions of the same thing).
		if us.useEntityIndex && us.useEntitySynonymy && us.llm != nil {
			if qvec, eerr := us.llm.GenerateEmbedding(ctx, llmIntent.Entity); eerr == nil {
				if norms, nerr := us.store.SimilarEntityNorms(ctx, qvec, us.llm.GetEmbeddingModel(), us.entitySynonymyThreshold, 10); nerr == nil && len(norms) > 0 {
					entityOpts.EntityNorms = norms
					log.Printf("[UnifiedSearch] entity synonymy: %d related entities for %q (τ=%.2f)", len(norms), llmIntent.Entity, us.entitySynonymyThreshold)
				}
			}
		}
		// Brick 3 — Phase D Hebbian expansion (kill-switched, default OFF): fold the
		// queried entity's strongest co-occurrence neighbours into the lens. No-op
		// (byte-identical) when off or the graph is empty; fail-open on a store error.
		if us.useEntityIndex && us.useHebbianExpansion {
			if neigh, herr := us.store.HebbianNeighbors(ctx, contradict.NormKey(llmIntent.Entity), time.Now(), hebbianMinWeight, 10); herr == nil && len(neigh) > 0 {
				entityOpts.EntityNorms = append(entityOpts.EntityNorms, neigh...)
				log.Printf("[UnifiedSearch] hebbian expansion: +%d neighbours for %q", len(neigh), llmIntent.Entity)
			}
		}
		eResults, eErr := EntitySearch(ctx, us.store, llmIntent, entityOpts)
		if eErr != nil {
			log.Printf("[UnifiedSearch] entity_search error: %v — falling back to vector", eErr)
		}
		topScore := 0.0
		if len(eResults) > 0 {
			topScore = eResults[0].Score
		}
		if len(eResults) > 0 && topScore >= us.entitySearchMinScore {
			finalResults := us.applyJudge(ctx, req.Query, eResults)
			return us.buildEntityResponse(finalResults, detectedIntent, startTime), nil
		}
		if !us.entitySearchFallback {
			log.Printf("[UnifiedSearch] entity_search yielded %d results (top=%.3f) — abstaining (fallback disabled)",
				len(eResults), topScore)
			return us.buildEntityResponse(nil, detectedIntent, startTime), nil
		}
		log.Printf("[UnifiedSearch] entity_search yielded %d results (top=%.3f) — falling back to vector",
			len(eResults), topScore)
	}

	// Routing branch 2 — Temporal with a structured attribute. Run the normal
	// vector pipeline but hard-filter docs that don't carry the requested
	// extracted_<attribute> key. This is the "dernière facture TVA avec son
	// montant" case: the candidate must actually contain a montant.
	var hardFilterAttrKeys []string
	if llmIntent != nil && llmIntent.Category == IntentTemporal && llmIntent.Attribute != "" {
		if keys, ok := attributeMetadataKeys[llmIntent.Attribute]; ok {
			hardFilterAttrKeys = keys
			log.Printf("[UnifiedSearch] hard-filter enabled for attribute=%q (keys=%v)",
				llmIntent.Attribute, keys)
		}
	}

	// Use a larger pool when temporal filtering narrows the result set post-retrieval.
	multiplier := DefaultMultiplier
	if detectedIntent != nil && (detectedIntent.TemporalMode == intent.TemporalRecent || detectedIntent.TemporalMode == intent.TemporalRange) {
		multiplier = 20
		log.Printf("[UnifiedSearch] Temporal intent detected (%s), fetch multiplier: %d", detectedIntent.TemporalMode, multiplier)
	}
	// When focus is active, post-filter drops everything outside scope. A
	// small project may produce only a handful of in-scope hits even at the
	// top of the cosine ranking, so widen the candidate pool before filtering.
	if focusActive && multiplier < 20 {
		multiplier = 20
		log.Printf("[UnifiedSearch] focus_scope active, fetch multiplier: %d", multiplier)
	}
	fetchLimit := req.TopK * multiplier

	var knowledgeVecResults []store.VecResult
	var mailVecResults []store.VecResult

	g, gCtx := errgroup.WithContext(ctx)

	if searchKnowledge {
		g.Go(func() error {
			var err error
			// Includes decisions so "ce qui fait foi" can actually surface (and be
			// boosted by the M2 re-score); without it, decision items are never in
			// the candidate pool. (task/event are still excluded — separate gap.)
			knowledgeTypes := []string{"file", "note", "markdown", "pdf", "txt", "docx", store.SourceTypeDecision}
			knowledgeVecResults, err = us.store.SearchChunksVecBySourceType(gCtx, embedding, fetchLimit, knowledgeTypes)
			return err
		})
	}

	if searchMail {
		mailFilter := store.MailFilter{AccountID: req.MailAccountID, LabelIDs: req.MailLabels}
		hasMailFilter := mailFilter.AccountID != "" || len(mailFilter.LabelIDs) > 0
		g.Go(func() error {
			var err error
			if hasMailFilter {
				mailVecResults, err = us.store.SearchChunksVecByMail(gCtx, embedding, fetchLimit, mailFilter)
			} else {
				mailTypes := store.MailAndSourceTypes("thread")
				mailVecResults, err = us.store.SearchChunksVecBySourceType(gCtx, embedding, fetchLimit, mailTypes)
			}
			return err
		})
	}

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Hybrid fusion. Blend the vector lists (knowledge + mail) with a lexical
	// BM25/FTS list using Reciprocal Rank Fusion. Lexical recall is what rescues
	// exact terms/codes/dates ("TVA", "avril", invoice numbers) that cosine
	// similarity alone misses — the core fix for factual/temporal questions.
	knowledgeWeight := weights[intent.SourceKnowledge]
	mailWeight := weights[intent.SourceMail]
	if knowledgeWeight == 0 {
		knowledgeWeight = 0.5
	}
	if mailWeight == 0 {
		mailWeight = 0.5
	}

	// Lexical BM25 over the RAW query (exact terms, no embedding expansion).
	// Fail-soft: a lexical error must never sink the whole search.
	ftsResults, ftsErr := us.store.SearchChunksFTS(ctx, req.Query, fetchLimit)
	if ftsErr != nil {
		log.Printf("[UnifiedSearch] FTS lexical search failed (fail-soft, vector only): %v", ftsErr)
	}

	vecToRanked := func(vs []store.VecResult) []rankedChunk {
		out := make([]rankedChunk, len(vs))
		for i, v := range vs {
			out[i] = rankedChunk{ChunkID: v.ChunkID, ContentID: v.ContentID}
		}
		return out
	}
	ftsRanked := make([]rankedChunk, len(ftsResults))
	for i, f := range ftsResults {
		ftsRanked[i] = rankedChunk{ChunkID: f.ChunkID, ContentID: f.ContentID}
	}

	fused := rrfFuse(
		rankedList{Weight: knowledgeWeight, Hits: vecToRanked(knowledgeVecResults)},
		rankedList{Weight: mailWeight, Hits: vecToRanked(mailVecResults)},
		rankedList{Weight: ftsWeight, Hits: ftsRanked},
	)
	// Normalize to [0,1] (top = 1.0) so the downstream recency blend and boosts,
	// which assume a cosine-like scale, behave as designed.
	if len(fused) > 0 && fused[0].Score > 0 {
		top := fused[0].Score
		for i := range fused {
			fused[i].Score /= top
		}
	}

	type scoredChunk struct {
		chunkID   string
		contentID string
		score     float64
	}
	combined := make([]scoredChunk, len(fused))
	for i, f := range fused {
		combined[i] = scoredChunk{f.ChunkID, f.ContentID, f.Score}
	}

	// Deduplicate by content_id, keep best score per document.
	docScores := make(map[string]float64)
	docChunks := make(map[string]string)
	for _, c := range combined {
		if existing, ok := docScores[c.contentID]; !ok || c.score > existing {
			docScores[c.contentID] = c.score
			docChunks[c.contentID] = c.chunkID
		}
	}

	// Title-keyword boost: short or acronym queries (e.g. "TVA") produce low cosine
	// scores even when the item is exactly right. Items whose title contains any
	// query token get a boost; items found only by title are injected near the top.
	us.applyTitleBoost(ctx, req.Query, docScores, docChunks)

	// Prior source boost: items cited in recent assistant turns get a small bump
	// to reinforce conversational continuity alongside the query rewrite.
	applyPriorSourceBoost(req.PriorSourceBoost, docScores)

	// Focus scope post-filter: drop documents not in the allow-list. Applied
	// AFTER applyTitleBoost so that title-injected docs are also subject to
	// the scope, and AFTER prior-source boost so the small bump doesn't matter
	// for out-of-scope items.
	if focusAllowList != nil {
		for cid := range docScores {
			if _, ok := focusAllowList[cid]; !ok {
				delete(docScores, cid)
				delete(docChunks, cid)
			}
		}
		log.Printf("[UnifiedSearch] focus_scope post-filter: %d docs survived", len(docScores))
	}

	type docEntry struct {
		contentID string
		chunkID   string
		score     float64
	}
	sortedDocs := make([]docEntry, 0, len(docScores))
	for cid, score := range docScores {
		sortedDocs = append(sortedDocs, docEntry{cid, docChunks[cid], score})
	}
	sort.Slice(sortedDocs, func(i, j int) bool { return sortedDocs[i].score > sortedDocs[j].score })

	// Cap the enrichment pool. With query expansion the embedding is now much
	// richer, so the pre-freshness top-N is a reliable proxy for the final ranking.
	// We use 5× TopK to give freshness headroom without enriching hundreds of docs.
	earlyLimit := req.TopK * 5
	if len(sortedDocs) > earlyLimit {
		sortedDocs = sortedDocs[:earlyLimit]
	}

	// Enrich results.
	enrichCtx, enrichCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer enrichCancel()

	now := time.Now()

	var fromTime, toTime time.Time
	if req.DateFrom != nil {
		fromTime = *req.DateFrom
	}
	if req.DateTo != nil {
		toTime = *req.DateTo
	}

	// Decide scoring path and pre-filter.
	scoringMode := req.ScoringMode
	if scoringMode == "" {
		scoringMode = ScoringModeAdditive
	}
	hasTemporalMarker := IsCurrentStateQuery(req.Query)
	// An explicit year/month in the query (e.g. "recharges 2026", "facture avril")
	// names a SUBJECT period, not the document's received date. In that case we
	// must NOT hard-drop by received date and we soften recency weighting, or we
	// silently lose older-but-relevant mail (a March invoice asked about in June).
	hasExplicitPeriod := QueryHasExplicitPeriod(req.Query)
	recencyMarker := hasTemporalMarker && !hasExplicitPeriod
	preFilterDays := 0
	if hasTemporalMarker && !hasExplicitPeriod && req.CurrentStateFilterDays > 0 {
		preFilterDays = req.CurrentStateFilterDays
	}
	queryEntityType := detectQueryEntityType(req.Query)

	log.Printf("[UnifiedSearch] mode=%s temporal_marker=%v explicit_period=%v pre_filter_days=%d entity_type=%q",
		scoringMode, hasTemporalMarker, hasExplicitPeriod, preFilterDays, queryEntityType)

	// perResultDebug accumulates the scoring trace when req.Debug is on.
	// Allocated lazily so the non-debug hot path stays cheap.
	var perResultDebug []ResultDebug

	enrich := func(applyPreFilter bool) ([]UnifiedResult, int, int) {
		results := make([]UnifiedResult, 0, len(sortedDocs))
		knowledgeCount := 0
		mailCount := 0
		if req.Debug {
			perResultDebug = perResultDebug[:0]
		}

		for _, doc := range sortedDocs {
			result := UnifiedResult{
				ChunkID:   doc.chunkID,
				ContentID: doc.contentID,
				Score:     doc.score,
			}

			item, err := us.store.GetKnowledgeItemWithMailData(enrichCtx, doc.contentID)
			if err != nil {
				log.Printf("[UnifiedSearch] GetKnowledgeItemWithMailData error for %s: %v", doc.contentID, err)
				continue
			}
			if item == nil {
				continue
			}

			result.Title = item.Title
			result.SourceType = item.SourceType
			result.Metadata = item.Metadata

			// Source-family filter: the lexical signal is source-agnostic, so a
			// single-family request must drop out-of-family docs it surfaced.
			isMailDoc := result.SourceType == "mail" || result.SourceType == "email" || result.SourceType == "thread"
			if (mailOnly && !isMailDoc) || (knowledgeOnly && isMailDoc) {
				continue
			}

			// Small-to-big: hand the model the matched chunk's COMPLETE section
			// block rather than the whole document or an arbitrary fixed slice.
			// Fall back to the chunk text, then a document prefix.
			result.Excerpt = ""
			if sec, secErr := us.store.GetSectionByChunkID(enrichCtx, doc.chunkID); secErr == nil && sec != nil && strings.TrimSpace(sec.FullText) != "" {
				result.Excerpt = clampText(sec.FullText, maxSectionExcerpt)
			}
			if result.Excerpt == "" {
				const maxDocLength = 2000
				if ch, chErr := us.store.GetChunk(enrichCtx, doc.chunkID); chErr == nil && ch != nil && ch.Text != "" {
					result.Excerpt = clampText(ch.Text, maxSectionExcerpt)
				} else if len(item.NormalizedText) > maxDocLength {
					result.Excerpt = item.NormalizedText[:maxDocLength] + "..."
				} else {
					result.Excerpt = item.NormalizedText
				}
			}

			if item.MailFrom != "" || item.MailDate != "" || item.MailSubject != "" {
				result.MailFrom = item.MailFrom
				result.MailDate = item.MailDate
				result.MailSubject = item.MailSubject
			}

			// Read canonical_date from metadata (set at ingestion).
			result.ParsedDate = store.GetCanonicalDate(item.KnowledgeItem)
			if !result.ParsedDate.IsZero() {
				result.Date = result.ParsedDate.UTC().Format(time.RFC3339)
			}

			// Date range filter (explicit DateFrom/DateTo from intent).
			if req.DateFrom != nil || req.DateTo != nil {
				if !result.ParsedDate.IsZero() {
					if !fromTime.IsZero() && result.ParsedDate.Before(fromTime) {
						continue
					}
					if !toTime.IsZero() && result.ParsedDate.After(toTime) {
						continue
					}
				}
			}

			// Current-state pre-filter: drop docs older than N days when the query
			// looks like a present-time question. Items with no canonical_date
			// survive (we don't know their age).
			if applyPreFilter && !result.ParsedDate.IsZero() {
				ageDays := now.Sub(result.ParsedDate).Hours() / 24
				if ageDays > float64(preFilterDays) {
					continue
				}
			}

			// Hard attribute filter (Temporal + Attribute branch): the doc
			// MUST carry at least one of the requested extracted_* keys.
			// "Dernière facture avec son montant" → drop docs without any
			// extracted_amounts.
			if len(hardFilterAttrKeys) > 0 {
				kept := false
				for _, k := range hardFilterAttrKeys {
					if hasNonEmptyList(item.Metadata, k) {
						kept = true
						break
					}
				}
				if !kept {
					continue
				}
			}

			// High-priority boost (×1.5): emails flagged as accounting/legal/HR
			// at indexing time get a multiplicative bump to reflect their
			// disproportionate importance for follow-up questions.
			boosts := []string{}
			if hp, ok := item.Metadata["high_priority"].(bool); ok && hp {
				result.Score *= 1.5
				boosts = append(boosts, "high_priority×1.5")
			}

			// Entity-type boost (×1.3): when the query explicitly targets a
			// structured entity (IBAN, amount, communication) and the item
			// carries that entity in its metadata, give it a tiebreaker.
			if queryEntityType != "" && metadataHasEntity(item.Metadata, queryEntityType) {
				result.Score *= 1.3
				boosts = append(boosts, "entity:"+queryEntityType+"×1.3")
			}

			// Apply scoring blend (additive or legacy multiplicative).
			semScore := result.Score
			switch scoringMode {
			case ScoringModeMultiplicative:
				result.Score = freshnessFactor(result.Score, result.ParsedDate, now, detectedIntent)
			default:
				result.Score = ApplyAdditiveScore(result.Score, result.ParsedDate, now, recencyMarker)
			}

			log.Printf("[UnifiedSearch] doc=%s title=%q date=%s sem=%.3f final=%.3f boosts=%v",
				doc.contentID, result.Title, result.ParsedDate.Format("2006-01-02"), semScore, result.Score, boosts)

			if req.Debug {
				ageDays := 0.0
				if !result.ParsedDate.IsZero() {
					ageDays = now.Sub(result.ParsedDate).Hours() / 24
				}
				hp, _ := item.Metadata["high_priority"].(bool)
				perResultDebug = append(perResultDebug, ResultDebug{
					ContentID:     result.ContentID,
					Title:         result.Title,
					Date:          result.Date,
					AgeDays:       ageDays,
					SemanticScore: semScore,
					RecencyScore:  recencyScore(result.ParsedDate, now),
					FinalScore:    result.Score,
					HighPriority:  hp,
					BoostsApplied: append([]string(nil), boosts...),
				})
			}

			switch result.SourceType {
			case "mail", "email", "thread":
				mailCount++
			default:
				knowledgeCount++
			}

			results = append(results, result)
		}

		return results, knowledgeCount, mailCount
	}

	candidatePoolPre := len(sortedDocs)
	results, knowledgeCount, mailCount := enrich(preFilterDays > 0)
	preFilterFallback := false
	if len(results) == 0 && preFilterDays > 0 {
		log.Printf("[UnifiedSearch] temporal_filter_fallback=true (no docs in last %dd)", preFilterDays)
		preFilterFallback = true
		results, knowledgeCount, mailCount = enrich(false)
	}

	// Entity consolidation (brick 2a): union the subject's connected items into the
	// semantic results before ranking/cap, so the full "about X" set surfaces.
	if us.useEntityConsolidation && consolidationEntity != "" {
		us.applyEntityConsolidation(ctx, &results, consolidationEntity)
	}

	// Re-sort by final score (scoring may have reordered results).
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })

	// Authority: annotate (M1a/M1b) then re-score (M2) BEFORE TopK so a
	// high-authority item can be promoted into the top-K, not merely reordered
	// within it. The re-score is a no-op unless authority rerank is enabled.
	us.annotateAuthority(ctx, results)
	us.applyAuthorityRescore(results)
	us.applyAttentionRescore(ctx, results) // P-2: attention nudges within the authority band
	us.applyImminenceRescore(ctx, results) // P-2: imminent obligations nudge within the band
	us.applySalienceRescore(ctx, results)  // recycle: composite-salience nudge (off by default)

	// Apply TopK after freshness re-ranking — widen for entity consolidation so the
	// connected set isn't truncated below the query's own top_k.
	topKEff := req.TopK
	if consolidationEntity != "" && topKEff < 20 {
		topKEff = 20
	}
	if len(results) > topKEff {
		results = results[:topKEff]
	}

	// Normalize top → 1.0 for interpretable scores.
	if len(results) > 0 {
		maxScore := results[0].Score
		if maxScore > 0 {
			for i := range results {
				results[i].Score /= maxScore
			}
		}
	}

	// Final stage: optional LLM judge as a post-filter. Drops results below
	// JudgeKeepThreshold; an all-empty verdict means abstention. Fail-soft on
	// LLM error: original results stay through.
	results = us.applyJudge(ctx, req.Query, results)
	knowledgeCount, mailCount = recountSources(results)

	resp := &UnifiedSearchResponse{
		Results: results,
		Intent:  detectedIntent,
		SearchStats: UnifiedSearchStats{
			TotalResults:     len(results),
			KnowledgeResults: knowledgeCount,
			MailResults:      mailCount,
			SearchDuration:   time.Since(startTime),
		},
	}
	if req.Debug {
		resp.Debug = &SearchDebugInfo{
			ScoringMode:             scoringMode,
			HasTemporalMarker:       hasTemporalMarker,
			PreFilterDays:           preFilterDays,
			PreFilterApplied:        preFilterDays > 0 && !preFilterFallback,
			PreFilterFallback:       preFilterFallback,
			QueryEntityType:         queryEntityType,
			CandidatePoolPreFilter:  candidatePoolPre,
			CandidatePoolPostFilter: len(results),
			PerResult:               perResultDebug,
		}
	}
	return resp, nil
}

// applyJudge runs the LLM relevance judge on `results` when useJudge is on,
// returning the filtered subset. Fail-soft: on judge error the original
// results pass through so a transient backend hiccup doesn't black-hole an
// otherwise-good search. A no-op when useJudge is false or results is empty.
func (us *UnifiedSearcher) applyJudge(ctx context.Context, query string, results []UnifiedResult) []UnifiedResult {
	if !us.useJudge || len(results) == 0 || us.llm == nil {
		return results
	}
	filtered, err := JudgeAndFilter(ctx, us.llm, query, results, JudgeKeepThreshold)
	if err != nil {
		log.Printf("[UnifiedSearch] judge failed (fail-soft): %v", err)
		return results
	}
	if len(filtered) == 0 {
		log.Printf("[UnifiedSearch] judge dropped all results — explicit abstention")
	} else {
		log.Printf("[UnifiedSearch] judge kept %d/%d results", len(filtered), len(results))
	}
	return filtered
}

// recountSources returns the (knowledge, mail) counts for the given results.
// Used after applyJudge potentially trims the result set so the response
// stats reflect the post-filter shape.
func recountSources(results []UnifiedResult) (int, int) {
	k, m := 0, 0
	for _, r := range results {
		switch r.SourceType {
		case "mail", "email", "thread":
			m++
		default:
			k++
		}
	}
	return k, m
}

// buildEntityResponse wraps EntitySearch results into the canonical
// UnifiedSearchResponse shape used by the FactualEntity routing branch.
func (us *UnifiedSearcher) buildEntityResponse(results []UnifiedResult, det *intent.Intent, startTime time.Time) *UnifiedSearchResponse {
	knowledge, mail := recountSources(results)
	return &UnifiedSearchResponse{
		Results: results,
		Intent:  det,
		SearchStats: UnifiedSearchStats{
			TotalResults:     len(results),
			KnowledgeResults: knowledge,
			MailResults:      mail,
			SearchDuration:   time.Since(startTime),
		},
	}
}

// FetchByContentIDs hydrates a list of UnifiedResult records directly from the
// store, bypassing the vector search entirely. Used by the session-context
// direct-source path: when a follow-up question targets an entity we've
// already cached, we skip retrieval and just re-fetch the original sources so
// the LLM can ground its answer on them.
//
// IDs that don't resolve are silently skipped. Score is set to 1.0 for each
// returned result so they all reach the LLM.
func (us *UnifiedSearcher) FetchByContentIDs(ctx context.Context, ids []string) ([]UnifiedResult, error) {
	if us.store == nil {
		return nil, fmt.Errorf("store unavailable")
	}
	out := make([]UnifiedResult, 0, len(ids))
	for _, id := range ids {
		item, err := us.store.GetKnowledgeItemWithMailData(ctx, id)
		if err != nil || item == nil {
			continue
		}
		r := UnifiedResult{
			ChunkID:     "",
			ContentID:   id,
			SourceType:  item.SourceType,
			Title:       item.Title,
			Score:       1.0,
			Metadata:    item.Metadata,
			MailFrom:    item.MailFrom,
			MailDate:    item.MailDate,
			MailSubject: item.MailSubject,
		}
		// Generous cap: these records hydrate attachment/source context fed to the
		// LLM, so we want (nearly) the full document — an audio transcript or an
		// OCR'd image must not lose its address/details to a short excerpt.
		// Bounded to avoid blowing the context on huge PDFs.
		const maxDocLength = 16000
		if len(item.NormalizedText) > maxDocLength {
			r.Excerpt = item.NormalizedText[:maxDocLength] + "..."
		} else {
			r.Excerpt = item.NormalizedText
		}
		r.ParsedDate = store.GetCanonicalDate(item.KnowledgeItem)
		if !r.ParsedDate.IsZero() {
			r.Date = r.ParsedDate.UTC().Format(time.RFC3339)
		}
		out = append(out, r)
	}
	return out, nil
}

// applyTitleBoost compensates for short/acronym queries that produce low cosine
// scores even when the document is a direct title match.
//
// Strategy:
//  1. Extract ALL-CAPS acronyms from the query (e.g. "TVA", "VAT", "URSSAF").
//     These are highly specific and work for both short ("TVA") and long natural
//     language queries ("Retrouve moi le dernier mail concernant la TVA ...").
//  2. If no acronyms found, fall back to the full query — only useful for very
//     short lowercase queries ("vat", "urssaf").
//
// For each search term, items already in docScores get a 2× boost; items absent
// from docScores are injected at 70% of the top vector score.
func (us *UnifiedSearcher) applyTitleBoost(ctx context.Context, query string, docScores map[string]float64, docChunks map[string]string) {
	if us.store == nil {
		return
	}

	// Determine what to search for in titles.
	terms := extractAcronyms(query)
	if len(terms) == 0 {
		// Fallback: use full query only for short queries (acronym in lowercase).
		if len(splitQueryTokens(query)) <= 2 {
			terms = []string{query}
		}
	}
	if len(terms) == 0 {
		return
	}

	var topScore float64
	for _, s := range docScores {
		if s > topScore {
			topScore = s
		}
	}

	boostCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	injectedScore := topScore * 0.7
	if injectedScore == 0 {
		injectedScore = 0.1
	}

	// Expand each term with known cross-language synonyms so that an English
	// acronym like "VAT" also boosts French titles containing "TVA".
	expandedTerms := make([]string, 0, len(terms)*2)
	seen := make(map[string]bool)
	for _, t := range terms {
		if !seen[t] {
			seen[t] = true
			expandedTerms = append(expandedTerms, t)
		}
		for _, syn := range titleBoostSynonyms[t] {
			if !seen[syn] {
				seen[syn] = true
				expandedTerms = append(expandedTerms, syn)
			}
		}
	}

	boosted := make(map[string]bool)
	for _, term := range expandedTerms {
		matches, err := us.store.SearchKnowledgeItemsByTitle(boostCtx, term, 50)
		if err != nil {
			log.Printf("[UnifiedSearch] title boost lookup failed for %q: %v", term, err)
			continue
		}
		for _, item := range matches {
			if boosted[item.ContentID] {
				continue
			}
			boosted[item.ContentID] = true
			if existing, ok := docScores[item.ContentID]; ok {
				docScores[item.ContentID] = existing * 2.0
				log.Printf("[UnifiedSearch] title boost ×2 for %s (%q): %.3f → %.3f",
					item.ContentID, item.Title, existing, existing*2.0)
			} else {
				docScores[item.ContentID] = injectedScore
				docChunks[item.ContentID] = ""
				log.Printf("[UnifiedSearch] title inject %s (%q) score=%.3f",
					item.ContentID, item.Title, injectedScore)
			}
		}
	}
}

// applyPriorSourceBoost reinforces conversational continuity by boosting items
// that were cited in recent assistant turns.
//
// Items already in docScores (present in vector results) receive a ×1.15 boost.
// Items NOT in docScores (missed by the vector search for the follow-up query)
// are injected at topScore×0.55 — below a strong semantic match (×0.7 for
// title injects) but high enough to appear in the results so the LLM can
// use them for follow-up answers on the same topic.
func applyPriorSourceBoost(contentIDs []string, docScores map[string]float64) {
	if len(contentIDs) == 0 {
		return
	}
	boostSet := make(map[string]bool, len(contentIDs))
	for _, id := range contentIDs {
		boostSet[id] = true
	}

	var topScore float64
	for _, s := range docScores {
		if s > topScore {
			topScore = s
		}
	}
	injectScore := topScore * 0.55
	if injectScore == 0 {
		injectScore = 0.1
	}

	for id := range boostSet {
		if existing, ok := docScores[id]; ok {
			docScores[id] = existing * 1.15
			log.Printf("[UnifiedSearch] prior-source boost ×1.15 for %s: %.3f → %.3f", id, existing, existing*1.15)
		} else {
			docScores[id] = injectScore
			log.Printf("[UnifiedSearch] prior-source inject %s score=%.3f", id, injectScore)
		}
	}
}

// titleBoostSynonyms maps known cross-language acronym equivalents so that a
// query in one language triggers title matches in the other (e.g. VAT ↔ TVA).
var titleBoostSynonyms = map[string][]string{
	"VAT":  {"TVA", "BTW"},
	"TVA":  {"VAT", "BTW"},
	"BTW":  {"TVA", "VAT"},
	"IBAN": {"IBAN"},
}

// extractAcronyms returns the ALL-CAPS words from a query (length 2–10).
// These are typically acronyms (TVA, VAT, URSSAF, SRL, RC) that are distinctive
// enough to drive title matching without producing false positives.
func extractAcronyms(query string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, word := range strings.Fields(query) {
		w := strings.Trim(word, "?!.,;:'\"()[]{}«» ")
		if len(w) < 2 || len(w) > 10 {
			continue
		}
		if w != strings.ToUpper(w) {
			continue // not all-caps
		}
		// Must contain at least one ASCII letter (not a pure number like "2026")
		hasLetter := false
		for _, r := range w {
			if r >= 'A' && r <= 'Z' {
				hasLetter = true
				break
			}
		}
		if hasLetter && !seen[w] {
			seen[w] = true
			result = append(result, w)
		}
	}
	return result
}

// splitQueryTokens returns the distinct lowercased tokens from a query,
// filtering out words shorter than 3 characters and common stop words.
func splitQueryTokens(query string) []string {
	stopWords := map[string]bool{
		"the": true, "and": true, "for": true, "are": true, "but": true,
		"not": true, "you": true, "all": true, "can": true, "her": true,
		"was": true, "one": true, "our": true, "out": true, "day": true,
		"get": true, "has": true, "him": true, "his": true, "how": true,
		"man": true, "new": true, "now": true, "old": true, "see": true,
		"two": true, "way": true, "who": true, "its": true, "let": true,
		"put": true, "say": true, "she": true, "too": true, "use": true,
		"les": true, "des": true, "une": true, "est": true, "par": true,
		"sur": true, "que": true, "qui": true, "mon": true, "mes": true,
	}

	raw := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '?' || r == '!' ||
			r == ':' || r == ';' || r == '"' || r == '\'' || r == '/'
	})

	seen := make(map[string]bool)
	var tokens []string
	for _, w := range raw {
		if len(w) < 3 || stopWords[w] || seen[w] {
			continue
		}
		seen[w] = true
		tokens = append(tokens, w)
	}
	return tokens
}

// freshnessFactor computes the score multiplier based on document age.
//
// Default (no temporal intent):
//   - half-life 60 days, floor 0.4 → freshness ∈ [0.4, 1.0]
//
// TemporalRecent (user said "latest / recently"):
//   - half-life 30 days, floor 0.1 → stronger recency bias
//
// TemporalRange (user specified a date window):
//   - date filter already applied; return score unchanged.
func freshnessFactor(score float64, date time.Time, now time.Time, det *intent.Intent) float64 {
	if det != nil && det.TemporalMode == intent.TemporalRange {
		return score
	}
	if date.IsZero() {
		// No date available — apply a mild penalty to avoid unknowns topping the list.
		return score * 0.7
	}

	halfLife := 60 * 24 * time.Hour
	floor := 0.4
	if det != nil && det.TemporalMode == intent.TemporalRecent {
		halfLife = 30 * 24 * time.Hour
		floor = 0.1
	}

	age := now.Sub(date)
	if age < 0 {
		age = 0
	}
	decay := math.Exp(-float64(age) / float64(halfLife) * math.Log(2))
	freshness := floor + (1.0-floor)*decay
	return score * freshness
}
