// Package handlers provides HTTP handlers for the Hygur API.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
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

// healthPingTTL bounds how long a /health ping result is reused. The Sidebar polls
// /health and k8s liveness/readiness probes hit it too; caching the ping results (30s)
// stops each poll from firing two live network pings at the model host (WP21).
const healthPingTTL = 30 * time.Second

// HealthHandler handles the /health endpoint.
type HealthHandler struct {
	llmClient *llm.Client
	startTime time.Time

	// mu guards the cached ping results below (server-side, healthPingTTL).
	mu              sync.Mutex
	pingedAt        time.Time
	cachedInference string
	cachedEmbedding string
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
	inferenceStatus, embeddingStatus := h.pingStatuses(r.Context())

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

// pingStatuses returns the inference/embedding reachability, cached for healthPingTTL.
// Within the TTL it serves the last result without touching the network; on a miss it
// fires the two pings (each bounded to 2s) and refreshes the cache.
func (h *HealthHandler) pingStatuses(ctx context.Context) (inference, embedding string) {
	h.mu.Lock()
	if !h.pingedAt.IsZero() && time.Since(h.pingedAt) < healthPingTTL {
		inference, embedding = h.cachedInference, h.cachedEmbedding
		h.mu.Unlock()
		return inference, embedding
	}
	h.mu.Unlock()

	inference, embedding = "disconnected", "disconnected"
	if h.llmClient != nil {
		// Bound each probe so a slow model host can't stall /health past ~2s.
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if connected, _ := h.llmClient.Ping(pingCtx); connected {
			inference = "connected"
		}
		if connected, _ := h.llmClient.PingEmbedding(pingCtx); connected {
			embedding = "connected"
		}
	}

	h.mu.Lock()
	h.cachedInference, h.cachedEmbedding, h.pingedAt = inference, embedding, time.Now()
	h.mu.Unlock()
	return inference, embedding
}

// SetStartTime allows setting a custom start time (useful for testing).
func (h *HealthHandler) SetStartTime(t time.Time) {
	h.startTime = t
}
