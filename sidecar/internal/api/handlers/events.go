package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hygur/sidecar/internal/events"
	"github.com/rs/zerolog"
)

// sseKeepaliveInterval governs how often we emit a `: keepalive\n\n` comment
// on idle SSE connections. Without it a silently-dropped TCP connection
// (e.g., sidecar restart while the macOS app was running) leaves URLSession
// hanging on `bytes(for:)` indefinitely — no events ever get delivered and
// the user sees a stale activity list. 15 s is well below the macOS default
// per-request timeout but rare enough not to spam the logs.
const sseKeepaliveInterval = 15 * time.Second

// EventsHandler handles SSE (Server-Sent Events) connections.
type EventsHandler struct {
	broker *events.Broker
	logger zerolog.Logger
}

// NewEventsHandler creates a new EventsHandler.
func NewEventsHandler(broker *events.Broker, logger zerolog.Logger) *EventsHandler {
	return &EventsHandler{
		broker: broker,
		logger: logger.With().Str("handler", "events").Logger(),
	}
}

// Handle handles SSE connections.
func (h *EventsHandler) Handle(w http.ResponseWriter, r *http.Request) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Get flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.logger.Error().Msg("ResponseWriter does not support flushing")
		return
	}

	// Create context for subscription
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Subscribe to all events
	ch := h.broker.Subscribe()

	// Send initial connection event
	if err := h.writeSSEEvent(w, flusher, map[string]string{
		"type":    "connection",
		"message": "connected",
	}); err != nil {
		return
	}

	keepalive := time.NewTicker(sseKeepaliveInterval)
	defer keepalive.Stop()

	// Start sending events
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return
			}
			if err := h.writeSSEEvent(w, flusher, evt); err != nil {
				h.logger.Debug().Err(err).Msg("failed to write SSE event")
				return
			}
		case <-keepalive.C:
			// SSE comment line — clients ignore it but the bytes flow
			// keeps the connection warm and trips URLSession's per-byte
			// timeout when the link actually dies.
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

// writeSSEEvent writes a single SSE event.
func (h *EventsHandler) writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, event interface{}) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	if err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
