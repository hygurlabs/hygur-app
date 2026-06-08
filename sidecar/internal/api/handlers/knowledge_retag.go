package handlers

import "net/http"

// Retag backfills mail auto-tags over already-ingested mail items (sender domain
// + mailbox folder). POST /knowledge/retag. One-shot maintenance: mail ingested
// via the text-push path before tagging existed had no tags; this fills them in.
// Idempotent — safe to call repeatedly.
func (h *KnowledgeHandler) Retag(w http.ResponseWriter, r *http.Request) {
	if h.ingestor == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "ingestor not configured")
		return
	}
	n, err := h.ingestor.RetagMail(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Msg("retag failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "retag failed")
		return
	}
	writeKnowledgeJSON(w, http.StatusOK, map[string]int{"retagged": n})
}
