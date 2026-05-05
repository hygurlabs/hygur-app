// Package handlers provides HTTP handlers for the Hygur API.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/version"
)

// HealthResponse represents the response from the /health endpoint.
//
// LMStudio reflects the inference endpoint reachability and is kept for
// backwards compatibility with existing clients. Inference and Embedding
// mirror it when a single endpoint is configured; they diverge once users
// point embedding at a separate host.
type HealthResponse struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	LMStudio      string `json:"lm_studio"`
	Inference     string `json:"inference"`
	Embedding     string `json:"embedding"`
	UptimeSeconds int64  `json:"uptime_seconds,omitempty"`
}

// HealthHandler handles the /health endpoint.
type HealthHandler struct {
	llmClient *llm.Client
	startTime time.Time
}

// NewHealthHandler creates a new HealthHandler.
func NewHealthHandler(llmClient *llm.Client) *HealthHandler {
	return &HealthHandler{
		llmClient: llmClient,
		startTime: time.Now(),
	}
}

// ServeHTTP implements http.Handler for the health endpoint.
func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Use a 2-second timeout for the LM Studio ping
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	inferenceStatus := "disconnected"
	embeddingStatus := "disconnected"
	if h.llmClient != nil {
		if connected, _ := h.llmClient.Ping(ctx); connected {
			inferenceStatus = "connected"
		}
		if connected, _ := h.llmClient.PingEmbedding(ctx); connected {
			embeddingStatus = "connected"
		}
	}

	status := "ok"
	if inferenceStatus == "disconnected" || embeddingStatus == "disconnected" {
		status = "degraded"
	}

	resp := HealthResponse{
		Status:        status,
		Version:       version.Version,
		LMStudio:      inferenceStatus,
		Inference:     inferenceStatus,
		Embedding:     embeddingStatus,
		UptimeSeconds: int64(time.Since(h.startTime).Seconds()),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Headers already sent, nothing we can do
		return
	}
}

// SetStartTime allows setting a custom start time (useful for testing).
func (h *HealthHandler) SetStartTime(t time.Time) {
	h.startTime = t
}
