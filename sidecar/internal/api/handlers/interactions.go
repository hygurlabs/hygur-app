package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/hygur/sidecar/internal/interactions"
	"github.com/rs/zerolog"
)

// InteractionsHandler accepts append-only interaction events from the macOS
// app and the share extension. POST-only — the log is read internally
// (Phase 2 recap, Phase 3 ranking, Phase 4 prioritisation), never streamed
// back to the client.
type InteractionsHandler struct {
	logger    zerolog.Logger
	appender  *interactions.Logger
}

// NewInteractionsHandler returns a handler backed by the given logger.
func NewInteractionsHandler(appender *interactions.Logger, logger zerolog.Logger) *InteractionsHandler {
	return &InteractionsHandler{
		appender: appender,
		logger:   logger.With().Str("handler", "interactions").Logger(),
	}
}

// interactionRequest is the wire shape for POST /interactions.
type interactionRequest struct {
	Kind      string         `json:"kind"`
	RefKind   string         `json:"ref_kind,omitempty"`
	RefID     string         `json:"ref_id,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
}

// Append handles POST /interactions. Body is a single event. Batch endpoint
// can be added later if traffic warrants it — current expected rate is
// well under 1 req/s per active user.
func (h *InteractionsHandler) Append(w http.ResponseWriter, r *http.Request) {
	if h.appender == nil {
		writeInteractionsError(w, http.StatusServiceUnavailable, "interactions logger not configured")
		return
	}
	var req interactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeInteractionsError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Kind == "" {
		writeInteractionsError(w, http.StatusBadRequest, "kind is required")
		return
	}
	ev := interactions.Event{
		Kind:      interactions.Kind(req.Kind),
		RefKind:   req.RefKind,
		RefID:     req.RefID,
		Payload:   req.Payload,
		SessionID: req.SessionID,
	}
	if err := h.appender.Append(r.Context(), ev); err != nil {
		// Unknown kind is a 400; everything else is 500. The validator inside
		// Logger.Append returns a sentinel-shaped error message starting with
		// "unknown interaction kind" which we surface verbatim to make the
		// macOS app's debug log actionable.
		h.logger.Warn().Err(err).Str("kind", req.Kind).Msg("interaction append failed")
		if isUnknownKindError(err) {
			writeInteractionsError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeInteractionsError(w, http.StatusInternalServerError, "failed to record interaction")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func isUnknownKindError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return len(msg) >= len("unknown interaction kind") && msg[:len("unknown interaction kind")] == "unknown interaction kind"
}

func writeInteractionsError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
