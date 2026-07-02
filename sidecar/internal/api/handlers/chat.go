// Package handlers provides HTTP handlers for the Hygur API.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/rs/zerolog"
)

// ChatRequest represents a request to the /chat endpoint.
type ChatRequest struct {
	Messages    []llm.Message `json:"messages"`
	Model       string        `json:"model,omitempty"`
	Stream      bool          `json:"stream"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
}

// ChatHandler handles the /chat endpoint with SSE streaming.
type ChatHandler struct {
	llmClient *llm.Client
	logger    zerolog.Logger
}

// NewChatHandler creates a new ChatHandler.
func NewChatHandler(llmClient *llm.Client, logger zerolog.Logger) *ChatHandler {
	return &ChatHandler{
		llmClient: llmClient,
		logger:    logger.With().Str("handler", "chat").Logger(),
	}
}

// ServeHTTP implements http.Handler for the chat endpoint.
func (h *ChatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		writeChatError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only POST method is allowed")
		return
	}

	// Parse the request body
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Debug().Err(err).Msg("failed to parse request body")
		writeChatError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	// Validate request - at least one message is required
	if len(req.Messages) == 0 {
		writeChatError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "messages required")
		return
	}

	// Check if LLM client is available
	if h.llmClient == nil {
		writeChatError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "LLM client not configured")
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	// Get the Flusher interface for streaming
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.logger.Error().Msg("ResponseWriter does not support Flusher interface")
		// Headers already set, we need to send error as SSE event
		writeSSEError(w, "INTERNAL_ERROR", "Streaming not supported")
		return
	}

	// Build the LLM request
	llmReq := llm.ChatRequest{
		Model:     req.Model,
		Messages:  req.Messages,
		Stream:    true,
		MaxTokens: req.MaxTokens,
	}
	// Map the client-supplied temperature to a pointer only when non-zero.
	// The old float64+omitempty field already dropped 0 on the wire, so this
	// reproduces today's behavior exactly (0 / absent => backend default) while
	// forwarding an explicit non-zero value. We never force 0 on the user chat
	// path — that determinism is reserved for the extraction passes.
	if req.Temperature != 0 {
		llmReq.Temperature = llm.Temp(req.Temperature)
	}

	// Stream from LLM
	err := h.llmClient.StreamChat(r.Context(), llmReq, func(delta string, done bool, usage *llm.Usage) error {
		// Check if client disconnected
		select {
		case <-r.Context().Done():
			h.logger.Debug().Msg("client disconnected during stream")
			return r.Context().Err()
		default:
		}

		var event map[string]any
		if done {
			event = map[string]any{
				"done": true,
			}
			if usage != nil {
				event["usage"] = map[string]int{
					"prompt_tokens":     usage.PromptTokens,
					"completion_tokens": usage.CompletionTokens,
					"total_tokens":      usage.TotalTokens,
				}
			}
		} else {
			event = map[string]any{
				"delta": delta,
				"done":  false,
			}
		}

		data, err := json.Marshal(event)
		if err != nil {
			h.logger.Error().Err(err).Msg("failed to marshal SSE event")
			return err
		}

		_, writeErr := fmt.Fprintf(w, "data: %s\n\n", data)
		if writeErr != nil {
			h.logger.Debug().Err(writeErr).Msg("failed to write SSE event")
			return writeErr
		}
		flusher.Flush()
		return nil
	})

	if err != nil {
		// Check if it's a client disconnect - don't log as error
		if r.Context().Err() != nil {
			h.logger.Debug().Msg("stream ended due to client disconnect")
			return
		}

		// Log the error
		h.logger.Error().Err(err).Msg("chat stream error")

		// Send error as SSE event (mid-stream error)
		writeSSEError(w, "LLM_STUDIO_ERROR", err.Error())
		flusher.Flush()
	}
}

// writeChatError writes a JSON error response for the chat endpoint.
// This is used before SSE headers are set.
func writeChatError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// writeSSEError writes an error as an SSE event.
// This is used after SSE headers are set (mid-stream errors).
func writeSSEError(w http.ResponseWriter, code, message string) {
	errEvent := map[string]any{
		"type": "error",
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	}
	data, _ := json.Marshal(errEvent)
	fmt.Fprintf(w, "data: %s\n\n", data)
}
