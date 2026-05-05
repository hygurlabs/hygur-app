// Package handlers provides HTTP handlers for the Hygur API.
package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/rs/zerolog"
)

// ModelsResponse represents the response from the /models endpoint.
type ModelsResponse struct {
	Models []ModelInfo `json:"models"`
}

// ModelInfo represents information about an available model.
type ModelInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CtxWindow int    `json:"ctx_window,omitempty"`
	Type      string `json:"type,omitempty"`
	Loaded    bool   `json:"loaded,omitempty"`
}

// ModelsHandler handles the /models endpoint.
type ModelsHandler struct {
	llmClient *llm.Client
	logger    zerolog.Logger
}

// NewModelsHandler creates a new ModelsHandler.
func NewModelsHandler(llmClient *llm.Client, logger zerolog.Logger) *ModelsHandler {
	return &ModelsHandler{
		llmClient: llmClient,
		logger:    logger.With().Str("handler", "models").Logger(),
	}
}

// ServeHTTP implements http.Handler for the models endpoint.
func (h *ModelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.llmClient == nil {
		h.logger.Error().Msg("LLM client not configured")
		writeModelsError(w, http.StatusServiceUnavailable, "LM_STUDIO_UNREACHABLE", "LLM client not configured")
		return
	}

	models, err := h.llmClient.ListModels(ctx)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list models")
		writeModelsError(w, http.StatusServiceUnavailable, "LM_STUDIO_UNREACHABLE", "Cannot connect to LM Studio")
		return
	}

	// Transform LM Studio models to our response format
	resp := ModelsResponse{
		Models: make([]ModelInfo, len(models)),
	}
	for i, m := range models {
		resp.Models[i] = ModelInfo{
			ID:   m.ID,
			Name: m.ID, // LM Studio returns ID as name; we use it as-is
			// ctx_window, type, loaded can be enriched if LM Studio provides them
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Headers already sent, nothing we can do
		h.logger.Error().Err(err).Msg("failed to encode response")
		return
	}
}

// ErrorResponse represents a standard error response per api-contract.md.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains the error details.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeModelsError writes a JSON error response in the standard format.
func writeModelsError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := ErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: message,
		},
	}

	_ = json.NewEncoder(w).Encode(resp)
}
