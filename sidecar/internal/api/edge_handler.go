package api

import (
	"context"
	"encoding/json"
	"net/http"
)

// Edge thin-client routes (cloud mode). These run on the user's device — the only
// place that can reach the local Proton Bridge — and are kept LOCAL by the cloud
// proxy (never forwarded to the tenant). The WebUI's Proton "connector" card uses
// them: list folders, show status (the green dot), and trigger a sync. The actual
// ingestion is the edge push loop, which streams extracted text to the central KB.

func (s *Server) edgeUnavailable(w http.ResponseWriter) bool {
	if s.edgeRunner == nil {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]string{
			"error": "edge sync is only available in the desktop cloud client",
		})
		return true
	}
	return false
}

// handleEdgeStatus returns the last edge-sync summary (drives the connector's
// green dot + last-synced/error display).
func (s *Server) handleEdgeStatus(w http.ResponseWriter, _ *http.Request) {
	if s.edgeUnavailable(w) {
		return
	}
	writeJSONResponse(w, http.StatusOK, s.edgeRunner.Status())
}

// handleEdgeMailboxes lists the local Proton mailboxes/folders ("Load folders").
func (s *Server) handleEdgeMailboxes(w http.ResponseWriter, r *http.Request) {
	if s.edgeUnavailable(w) {
		return
	}
	boxes, err := s.edgeRunner.Mailboxes(r.Context())
	if err != nil {
		writeJSONResponse(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSONResponse(w, http.StatusOK, map[string]any{"mailboxes": boxes})
}

// handleEdgeSync triggers an immediate edge sync (push local sources → central KB)
// in the background, returning 202. Progress/result is read via /edge/status.
func (s *Server) handleEdgeSync(w http.ResponseWriter, _ *http.Request) {
	if s.edgeUnavailable(w) {
		return
	}
	// Detach from the request context so the sync isn't cancelled when the HTTP
	// handler returns; the runner serialises its own state.
	go s.edgeRunner.RunOnce(context.Background())
	w.WriteHeader(http.StatusAccepted)
}

// writeJSONResponse writes v as JSON with the given status. Local helper so the
// edge routes don't depend on handler-package writers.
func writeJSONResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
