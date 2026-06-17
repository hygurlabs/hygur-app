package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/hygur/sidecar/internal/ingest"
)

// ReconcileRequest is the body for POST /knowledge/reconcile. The edge agent
// enumerates the messages currently present on the server for one provider and
// posts the FULL set of their refs; the center prunes KB items no longer present.
type ReconcileRequest struct {
	Provider string   `json:"provider"`  // source prefix, e.g. "proton" → "proton:"
	SeenRefs []string `json:"seen_refs"` // every present message ref this pass
	// Complete is the edge's integrity attestation: true only when the enumeration
	// covered every mailbox without error. The destructive path runs ONLY when true.
	Complete bool `json:"complete"`
	// GraceMisses defers a hard purge until an item has been missing this many
	// consecutive passes (0 → default 3). A transient bad pass can't destroy data.
	GraceMisses int `json:"grace_misses,omitempty"`
}

// ReconcileResponse reports a reconcile pass outcome.
type ReconcileResponse struct {
	Status   string `json:"status"`
	Recycled int    `json:"recycled"`
	Restored int    `json:"restored"`
	Purged   int    `json:"purged"`
}

// providerRe bounds the provider token to a short lowercase-alnum string so it is
// safe to use as a source_ref prefix.
var providerRe = regexp.MustCompile(`^[a-z0-9]{1,20}$`)

// Reconcile handles POST /knowledge/reconcile. Items no longer present on the
// server are soft-deleted (moved to the recycle bin — invisible across every read
// surface via the knowledge_items cascade) and hard-purged only after a grace
// period; an item that reappears is re-ingested. Two guards make the destructive
// path safe: it runs only when the edge attests the enumeration is complete, and
// it refuses an empty present-set while the KB still holds items for that source.
func (h *KnowledgeHandler) Reconcile(w http.ResponseWriter, r *http.Request) {
	var req ReconcileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid JSON")
		return
	}
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if !providerRe.MatchString(provider) {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid provider")
		return
	}
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "store not configured")
		return
	}
	prefix := provider + ":"
	ctx := r.Context()

	// Gate 1 — never act on a partial enumeration.
	if !req.Complete {
		writeKnowledgeJSON(w, http.StatusOK, ReconcileResponse{Status: "skipped_incomplete"})
		return
	}
	// Gate 2 — refuse an empty present-set while the KB still holds items (a buggy
	// edge that listed nothing must not wipe the corpus).
	if len(req.SeenRefs) == 0 {
		if n, err := h.store.CountActiveBySourceRefPrefix(ctx, prefix); err == nil && n > 0 {
			h.logger.Warn().Str("provider", provider).Int("active", n).
				Msg("[reconcile] refused: empty present-set vs non-empty KB")
			writeKnowledgeJSON(w, http.StatusOK, ReconcileResponse{Status: "refused_empty"})
			return
		}
	}

	seen := make(map[string]struct{}, len(req.SeenRefs))
	for _, ref := range req.SeenRefs {
		if ref != "" {
			seen[ref] = struct{}{}
		}
	}
	grace := req.GraceMisses
	if grace <= 0 {
		grace = 3
	}

	plan, err := h.store.ReconcileMailRefs(ctx, prefix, seen, grace)
	if err != nil {
		h.logger.Error().Err(err).Str("provider", provider).Msg("[reconcile] failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "reconcile failed")
		return
	}

	// Restore reappeared items by re-ingesting their preserved text (re-embeds).
	// A failed restore keeps the recycle row so the next pass retries.
	restored := 0
	for _, e := range plan.Restore {
		if h.ingestor == nil {
			break
		}
		var meta map[string]any
		if e.Metadata != "" {
			_ = json.Unmarshal([]byte(e.Metadata), &meta)
		}
		author, _ := meta["author"].(string)
		if _, ierr := h.ingestor.IngestText(ctx, ingest.IngestTextInput{
			Title:      e.Title,
			Text:       e.NormalizedText,
			SourceType: e.SourceType,
			SourceRef:  e.SourceRef,
			Author:     author,
			Metadata:   meta,
			CreatedAt:  e.ItemCreatedAt,
		}); ierr != nil {
			h.logger.Warn().Err(ierr).Str("source_ref", e.SourceRef).
				Msg("[reconcile] restore failed; keeping in recycle for retry")
			continue
		}
		if derr := h.store.DeleteRecycle(ctx, e.ContentID); derr != nil {
			h.logger.Warn().Err(derr).Str("content_id", e.ContentID).
				Msg("[reconcile] restore: failed to drop recycle row")
		}
		restored++
	}

	if plan.Recycled+restored+plan.Purged > 0 {
		h.logger.Info().Str("provider", provider).
			Int("recycled", plan.Recycled).Int("restored", restored).Int("purged", plan.Purged).
			Int("seen", len(seen)).Msg("[reconcile] applied")
	}
	writeKnowledgeJSON(w, http.StatusOK, ReconcileResponse{
		Status:   "ok",
		Recycled: plan.Recycled,
		Restored: restored,
		Purged:   plan.Purged,
	})
}
