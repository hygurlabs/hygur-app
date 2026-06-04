package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/hygur/sidecar/internal/ingest"
)

// IngestTextRequest is the body for POST /knowledge/ingest-text — pre-extracted
// text pushed by a client (the "Add files" path / future edge agent). No file
// parsing happens server-side; only text crosses the wire.
type IngestTextRequest struct {
	Title      string         `json:"title"`
	Text       string         `json:"text"`
	SourceType string         `json:"source_type"`
	SourceRef  string         `json:"source_ref"`
	URL        string         `json:"url,omitempty"`
	Author     string         `json:"author,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// IngestText handles POST /knowledge/ingest-text. Idempotent by source_ref:
// re-pushing the same ref updates the item; identical text is a no-op.
func (h *KnowledgeHandler) IngestText(w http.ResponseWriter, r *http.Request) {
	var req IngestTextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid JSON")
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "text is required")
		return
	}
	if h.ingestor == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "ingestor not configured")
		return
	}

	res, err := h.ingestor.IngestText(r.Context(), ingest.IngestTextInput{
		Title:      req.Title,
		Text:       req.Text,
		SourceType: req.SourceType,
		SourceRef:  req.SourceRef,
		URL:        req.URL,
		Author:     req.Author,
		Metadata:   req.Metadata,
	})
	if err != nil {
		h.logger.Error().Err(err).Str("source_ref", req.SourceRef).Msg("ingest-text failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "ingestion failed")
		return
	}

	writeKnowledgeJSON(w, http.StatusOK, IngestResponse{
		ContentID:  res.ContentID,
		Status:     res.Status,
		ChunkCount: res.ChunkCount,
		Title:      req.Title,
	})
}
