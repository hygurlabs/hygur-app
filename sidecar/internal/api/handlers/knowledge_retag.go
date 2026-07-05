package handlers

import (
	"context"
	"net/http"
	"sync/atomic"
)

// retagInFlight guards against overlapping retag runs (the job is long: one LLM
// call per mail that still needs topic extraction).
var retagInFlight atomic.Bool

// Retag rebuilds mail auto-tags over the whole corpus (purge stale auto-tags →
// mailbox-folder + Tier-2 topic tags). POST /knowledge/retag. The job runs in the
// background and is reported via logs; the request returns immediately. Watch the
// result by polling GET /tags. Idempotent — cached topics are reused on re-runs;
// ?force=1 bypasses the cache and re-classifies every item on the LLM.
func (h *KnowledgeHandler) Retag(w http.ResponseWriter, r *http.Request) {
	if h.ingestor == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "ingestor not configured")
		return
	}
	if !retagInFlight.CompareAndSwap(false, true) {
		writeKnowledgeJSON(w, http.StatusOK, map[string]string{"status": "already_running"})
		return
	}
	// ?force=1 re-classifies every item on the LLM (full re-derive), bypassing the
	// version cache — used after re-extracting text (OCR) to re-tag the whole corpus.
	force := r.URL.Query().Get("force") == "1"
	go func() {
		defer retagInFlight.Store(false)
		// Detached from the request: the backfill outlives the HTTP call.
		n, err := h.ingestor.RetagItems(context.Background(), force)
		if err != nil {
			h.logger.Error().Err(err).Int("processed", n).Msg("mail retag failed")
			return
		}
		h.logger.Info().Int("processed", n).Bool("force", force).Msg("mail retag complete")
	}()
	writeKnowledgeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// tier2InFlight guards the Tier-2 NER backfill (one LLM call per un-stamped item).
var tier2InFlight atomic.Bool

// BackfillTier2NER re-runs Tier-2 NER (persons/orgs/projects/topics) across the corpus
// into item metadata (updated_at preserved). POST /knowledge/backfill-tier2. Runs in the
// background; idempotent (current-version items skipped). Follow with backfill-entity-index
// + backfill-entity-edges to fold the new entities into the index and graph.
func (h *KnowledgeHandler) BackfillTier2NER(w http.ResponseWriter, r *http.Request) {
	if h.ingestor == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "ingestor not configured")
		return
	}
	if !tier2InFlight.CompareAndSwap(false, true) {
		writeKnowledgeJSON(w, http.StatusOK, map[string]string{"status": "already_running"})
		return
	}
	// ?model=main uses the larger generation model (better NER); ?force=1 re-extracts
	// even already-stamped items (to re-run the whole corpus with a different model).
	useMain := r.URL.Query().Get("model") == "main"
	force := r.URL.Query().Get("force") == "1"
	go func() {
		defer tier2InFlight.Store(false)
		n, err := h.ingestor.BackfillTier2NER(context.Background(), useMain, force)
		if err != nil {
			h.logger.Error().Err(err).Int("scanned", n).Msg("tier-2 NER backfill failed")
			return
		}
		h.logger.Info().Int("scanned", n).Bool("main_model", useMain).Bool("force", force).Msg("tier-2 NER backfill complete")
	}()
	writeKnowledgeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// claimsInFlight guards against overlapping claim-backfill runs (one main-model
// LLM call per eligible mail/note that isn't already cached).
var claimsInFlight atomic.Bool

// BackfillClaims extracts + caches semantic claims (W6) across eligible mail +
// notes. POST /knowledge/backfill-claims. Runs in the background (reported via
// logs); returns immediately. Run /knowledge/retag first so categories are cached
// (eligibility check) and the backfill only spends LLM on claim extraction.
// ?force=1 bypasses the version cache and re-extracts claims for every eligible item.
func (h *KnowledgeHandler) BackfillClaims(w http.ResponseWriter, r *http.Request) {
	if h.ingestor == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "ingestor not configured")
		return
	}
	if !claimsInFlight.CompareAndSwap(false, true) {
		writeKnowledgeJSON(w, http.StatusOK, map[string]string{"status": "already_running"})
		return
	}
	// ?force=1 re-extracts claims on the LLM for every eligible item (full re-derive),
	// bypassing the version cache — used after re-extracting text (OCR) / a full rebuild.
	force := r.URL.Query().Get("force") == "1"
	go func() {
		defer claimsInFlight.Store(false)
		n, err := h.ingestor.BackfillClaims(context.Background(), force)
		if err != nil {
			h.logger.Error().Err(err).Int("scanned", n).Msg("claim backfill failed")
			return
		}
		h.logger.Info().Int("scanned", n).Bool("force", force).Msg("claim backfill complete")
	}()
	writeKnowledgeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// entityIndexInFlight guards the entity-index backfill (one run at a time).
var entityIndexInFlight atomic.Bool

// BackfillEntityIndex (re)builds the associative entity index from the claims
// already cached on each item. POST /knowledge/backfill-entity-index. Deterministic
// (no LLM); runs in the background and returns immediately. Run /knowledge/backfill-claims
// first so items actually carry claims to index.
func (h *KnowledgeHandler) BackfillEntityIndex(w http.ResponseWriter, r *http.Request) {
	if h.ingestor == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "ingestor not configured")
		return
	}
	if !entityIndexInFlight.CompareAndSwap(false, true) {
		writeKnowledgeJSON(w, http.StatusOK, map[string]string{"status": "already_running"})
		return
	}
	go func() {
		defer entityIndexInFlight.Store(false)
		n, err := h.ingestor.BackfillEntityIndex(context.Background())
		if err != nil {
			h.logger.Error().Err(err).Int("scanned", n).Msg("entity-index backfill failed")
			return
		}
		h.logger.Info().Int("scanned", n).Msg("entity-index backfill complete")
	}()
	writeKnowledgeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// identifierIndexInFlight guards the identifier-index backfill (one run at a time).
var identifierIndexInFlight atomic.Bool

// BackfillIdentifiers (re)builds the materialized identifier index (item_norm) across the
// corpus. POST /knowledge/backfill-identifiers. Deterministic (no LLM); runs in the
// background. Run once after deploying the exact-identifier lookup; the ingest hook keeps
// new items current thereafter.
func (h *KnowledgeHandler) BackfillIdentifiers(w http.ResponseWriter, r *http.Request) {
	if h.ingestor == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "ingestor not configured")
		return
	}
	if !identifierIndexInFlight.CompareAndSwap(false, true) {
		writeKnowledgeJSON(w, http.StatusOK, map[string]string{"status": "already_running"})
		return
	}
	go func() {
		defer identifierIndexInFlight.Store(false)
		n, err := h.ingestor.BackfillIdentifierIndex(context.Background())
		if err != nil {
			h.logger.Error().Err(err).Int("indexed", n).Msg("identifier-index backfill failed")
			return
		}
		h.logger.Info().Int("indexed", n).Msg("identifier-index backfill complete")
	}()
	writeKnowledgeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// entityEdgesInFlight guards the Hebbian-edge backfill (one run at a time).
var entityEdgesInFlight atomic.Bool

// BackfillEntityEdges rebuilds the Hebbian co-occurrence graph from the claims
// already cached on each item. POST /knowledge/backfill-entity-edges. Deterministic
// (no LLM); runs in the background and returns immediately. Idempotent (clears +
// rebuilds). Run /knowledge/backfill-claims first so items carry claims.
func (h *KnowledgeHandler) BackfillEntityEdges(w http.ResponseWriter, r *http.Request) {
	if h.ingestor == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "ingestor not configured")
		return
	}
	if !entityEdgesInFlight.CompareAndSwap(false, true) {
		writeKnowledgeJSON(w, http.StatusOK, map[string]string{"status": "already_running"})
		return
	}
	go func() {
		defer entityEdgesInFlight.Store(false)
		n, err := h.ingestor.BackfillEntityEdges(context.Background())
		if err != nil {
			h.logger.Error().Err(err).Int("scanned", n).Msg("entity-edges backfill failed")
			return
		}
		h.logger.Info().Int("scanned", n).Msg("entity-edges backfill complete")
	}()
	writeKnowledgeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// entityVectorsInFlight guards the entity-vector backfill (one run at a time).
var entityVectorsInFlight atomic.Bool

// BackfillEntityVectors embeds distinct entity strings for the brick-2 synonymy
// expansion. POST /knowledge/backfill-entity-vectors. Runs in the background
// (uses the embedding endpoint); returns immediately. Run /knowledge/backfill-entity-index
// first so there are entities to embed.
func (h *KnowledgeHandler) BackfillEntityVectors(w http.ResponseWriter, r *http.Request) {
	if h.ingestor == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "ingestor not configured")
		return
	}
	if !entityVectorsInFlight.CompareAndSwap(false, true) {
		writeKnowledgeJSON(w, http.StatusOK, map[string]string{"status": "already_running"})
		return
	}
	go func() {
		defer entityVectorsInFlight.Store(false)
		n, err := h.ingestor.BackfillEntityVectors(context.Background())
		if err != nil {
			h.logger.Error().Err(err).Int("embedded", n).Msg("entity-vector backfill failed")
			return
		}
		h.logger.Info().Int("embedded", n).Msg("entity-vector backfill complete")
	}()
	writeKnowledgeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}
