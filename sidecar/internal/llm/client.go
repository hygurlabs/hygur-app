// Package llm provides a client for communicating with LM Studio's OpenAI-compatible API.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/config"
)

// defaultEmbeddingTimeout is used when no explicit embedding timeout is configured.
const defaultEmbeddingTimeout = 5 * time.Minute

// Client handles communication with OpenAI-compatible endpoints.
//
// baseURL is used for chat completions and model listing. embeddingBaseURL
// is used for embedding requests and falls back to baseURL when empty so
// single-endpoint configurations keep working unchanged.
type Client struct {
	baseURL             string
	embeddingBaseURL    string
	timeout             time.Duration
	maxRetries          int
	httpClient          *http.Client
	embeddingHTTPClient *http.Client
	// streamHTTPClient has no Timeout so long streaming responses are never
	// killed mid-stream by the transport. Cancellation comes from the caller's
	// context (i.e. the SSE client disconnecting).
	streamHTTPClient   *http.Client
	embeddingModel     string
	embeddingMaxTokens int
	embeddingBatchSize int
	// apiKey, when non-empty, is sent as `Authorization: Bearer <apiKey>` on
	// every request. Hosted providers (Mistral, OpenAI…) require it; local
	// runtimes (LM Studio, Ollama, vLLM) ignore it.
	apiKey string
	// usageRecorder, when set, receives token counts after each completion and
	// embedding call. chatCategory labels this client's chat completions
	// ("chat" for the main model, "indexing" for the ingestion model);
	// embeddings are always recorded under "embedding".
	usageRecorder UsageRecorder
	chatCategory  string
	// omitChatTemplateKwargs strips chat_template_kwargs from every chat request
	// (set from config for hosted backends that reject the field — e.g. Gemma on
	// Infomaniak). Default false keeps the vLLM/Qwen enable_thinking:false path.
	omitChatTemplateKwargs bool
}

// UsageRecorder receives token usage observed by the client. Implementations
// must be safe for concurrent use and must not block. category is one of
// "chat", "indexing" (chat completions) or "embedding".
type UsageRecorder interface {
	RecordUsage(category string, tokensIn, tokensOut int)
}

// SetUsageRecorder attaches a usage recorder. chatCategory labels chat
// completions from this client (e.g. "chat" or "indexing"); pass "" for the
// default "chat". Passing a nil recorder disables recording.
func (c *Client) SetUsageRecorder(r UsageRecorder, chatCategory string) {
	if chatCategory == "" {
		chatCategory = "chat"
	}
	c.usageRecorder = r
	c.chatCategory = chatCategory
}

// recordChatUsage forwards a completion's token counts to the recorder, if any.
func (c *Client) recordChatUsage(u *Usage) {
	if c.usageRecorder == nil || u == nil {
		return
	}
	cat := c.chatCategory
	if cat == "" {
		cat = "chat"
	}
	c.usageRecorder.RecordUsage(cat, u.PromptTokens, u.CompletionTokens)
}

// setAuthHeader adds the bearer Authorization header when an API key is
// configured. Local runtimes need no key, so the header is omitted when apiKey
// is empty — leaving the loopback/LAN setups byte-for-byte unchanged.
func (c *Client) setAuthHeader(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

// EmbeddingBatchSize returns the number of texts to send per embedding request,
// clamped to [1, MaxBatchSize]. Defaults to DefaultEmbeddingBatchSize.
func (c *Client) EmbeddingBatchSize() int {
	n := c.embeddingBatchSize
	if n <= 0 {
		n = DefaultEmbeddingBatchSize
	}
	if n > MaxBatchSize {
		n = MaxBatchSize
	}
	return n
}

// embeddingURL returns the base URL that should be used for embedding calls.
// When no dedicated embedding endpoint is configured, the inference URL is
// reused so existing single-endpoint setups keep working.
func (c *Client) embeddingURL() string {
	if c.embeddingBaseURL != "" {
		return c.embeddingBaseURL
	}
	return c.baseURL
}

// InferenceBaseURL returns the URL used for chat completions.
func (c *Client) InferenceBaseURL() string { return c.baseURL }

// EmbeddingBaseURL returns the URL used for embeddings (may equal the
// inference URL when no dedicated embedding endpoint is configured).
func (c *Client) EmbeddingBaseURL() string { return c.embeddingURL() }

// Message represents a chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// Attachments carry non-text payloads (images, audio, document refs) that
	// accompany the message. They are part of the Hygur internal API surface
	// only — the LLM sees them flattened into the OpenAI multimodal `content`
	// array via Message.MarshalJSON. Document refs must be resolved to text
	// upstream (in the chat handler) before reaching the LLM client.
	Attachments []Attachment `json:"attachments,omitempty"`
	// Reasoning is populated by reasoning-capable backends (vLLM, LM Studio
	// serving Qwen/Nemotron-super, etc.) when the model emits its scratch
	// thinking in a dedicated field instead of inline `<think>` tags inside
	// `content`. We expose it so callers can detect truncated responses
	// (`content` empty + `reasoning` long => budget exhausted) and surface a
	// useful error rather than persisting an empty body.
	Reasoning string `json:"reasoning,omitempty"`
	// ToolCalls is populated on assistant messages when the model decides to
	// invoke one or more tools. Per the OpenAI spec this is the canonical way
	// to round-trip tool execution: assistant emits tool_calls, the caller
	// runs each call, then echoes back one role="tool" message per call with
	// the matching tool_call_id.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID is set on role="tool" messages and must match the id of the
	// corresponding entry in the assistant's preceding ToolCalls list. The
	// LLM uses this to associate the tool result with its earlier request.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// Name is optional metadata; some providers expect it on tool messages
	// to mirror the function name. Kept for forward compatibility.
	Name string `json:"name,omitempty"`
}

// AttachmentType discriminates the variants of Attachment.
type AttachmentType string

const (
	AttachmentTypeImage    AttachmentType = "image"
	AttachmentTypeAudio    AttachmentType = "audio"
	AttachmentTypeDocument AttachmentType = "document"
)

// Attachment is a non-text payload carried on a Message. The wire shape is
// Hygur-internal: clients send `{type, mime_type, data, ...}` and the LLM
// client translates to whichever runtime-specific format the inference
// backend expects (OpenAI multimodal array today; vLLM/NIM may differ when
// the Phase 1.0 spike confirms the runtime API).
//
// Field semantics by Type:
//   - image:    Data (base64) + MimeType (e.g. "image/png")
//   - audio:    Data (base64) + Format (e.g. "wav", "mp3")
//   - document: ContentID — must be resolved to text by the chat handler
//     before the message reaches the LLM client.
type Attachment struct {
	Type      AttachmentType `json:"type"`
	MimeType  string         `json:"mime_type,omitempty"`
	Data      string         `json:"data,omitempty"`
	Format    string         `json:"format,omitempty"`
	ContentID string         `json:"content_id,omitempty"`
	// Title is an optional human-readable label rendered by the UI on
	// document attachments. It has no meaning for the LLM.
	Title string `json:"title,omitempty"`
}

// MarshalJSON emits the OpenAI multimodal `content` array shape when
// attachments are present and the plain string `content` shape otherwise.
// Document attachments are expected to have been resolved to text upstream;
// any that slip through are emitted as inert text references so the model
// doesn't choke on an unknown block type.
func (m Message) MarshalJSON() ([]byte, error) {
	if len(m.Attachments) == 0 {
		// No attachments → standard OpenAI message with string content.
		// Use a type alias to recurse without triggering this method again.
		type alias Message
		return json.Marshal(alias(m))
	}

	// Ordering matters for multimodal models (Gemma et al.): image content goes
	// BEFORE the text prompt, audio content AFTER it. Document stubs are inert
	// text and ride with the text block. (Mirrors the provider guidance:
	// "image before text, audio after text".)
	parts := make([]map[string]any, 0, 1+len(m.Attachments))

	for _, att := range m.Attachments {
		if att.Type != AttachmentTypeImage {
			continue
		}
		mime := att.MimeType
		if mime == "" {
			mime = "image/png"
		}
		parts = append(parts, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": fmt.Sprintf("data:%s;base64,%s", mime, att.Data),
			},
		})
	}

	if m.Content != "" {
		parts = append(parts, map[string]any{"type": "text", "text": m.Content})
	}
	for _, att := range m.Attachments {
		if att.Type != AttachmentTypeDocument {
			continue
		}
		label := att.Title
		if label == "" {
			label = att.ContentID
		}
		parts = append(parts, map[string]any{
			"type": "text",
			"text": fmt.Sprintf("[document:%s]", label),
		})
	}

	for _, att := range m.Attachments {
		if att.Type != AttachmentTypeAudio {
			continue
		}
		format := att.Format
		if format == "" {
			format = "wav"
		}
		parts = append(parts, map[string]any{
			"type": "input_audio",
			"input_audio": map[string]any{
				"data":   att.Data,
				"format": format,
			},
		})
	}

	out := map[string]any{
		"role":    m.Role,
		"content": parts,
	}
	if m.Name != "" {
		out["name"] = m.Name
	}
	if m.ToolCallID != "" {
		out["tool_call_id"] = m.ToolCallID
	}
	if len(m.ToolCalls) > 0 {
		out["tool_calls"] = m.ToolCalls
	}
	return json.Marshal(out)
}

// ToolCall represents a single tool invocation emitted by the model. OpenAI
// only defines `function` as a Type today but the field is wire-mandatory.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // always "function" today
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction is the function-call payload inside a ToolCall. Arguments
// is a JSON-encoded string (per OpenAI spec) — callers unmarshal it
// themselves before dispatching to a tool.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolCallDelta is the streaming counterpart of ToolCall: a single chunk may
// carry only the id+name on its first appearance and a fragment of arguments
// on subsequent ones. Index correlates fragments across chunks when the model
// emits multiple parallel tool calls.
type ToolCallDelta struct {
	Index    int                    `json:"index"`
	ID       string                 `json:"id,omitempty"`
	Type     string                 `json:"type,omitempty"`
	Function *ToolCallFunctionDelta `json:"function,omitempty"`
}

// ToolCallFunctionDelta carries partial function-call data inside a streaming
// tool_calls fragment. Arguments is concatenated across chunks.
type ToolCallFunctionDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ChatRequest represents a request to the chat completions endpoint.
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	// Tools is the list of tools the LLM may call, shaped as the OpenAI
	// `tools[]` array (`{type: "function", function: {name, description,
	// parameters}}`). Pass nil to disable tool calling — the field is
	// `omitempty` because some servers reject empty arrays.
	Tools []map[string]any `json:"tools,omitempty"`
	// ToolChoice controls invocation behavior: "none", "auto" (default),
	// "required", or `{type:"function", function:{name:"..."}}` to force a
	// specific tool. `any` keeps both string and object forms valid.
	ToolChoice any `json:"tool_choice,omitempty"`
	// ChatTemplateKwargs passes vLLM/SGLang chat-template options through to the
	// backend. The key use is `{"enable_thinking": false}` to stop reasoning
	// models (nemotron, Qwen3) from emitting <think> blocks that waste the token
	// budget and break strict-JSON callers. Omitted when nil.
	ChatTemplateKwargs map[string]any `json:"chat_template_kwargs,omitempty"`
	// StreamOptions asks a streaming completion to emit a terminal usage chunk
	// (token counts). Set automatically by the streaming path when a usage
	// recorder is attached; otherwise omitted. vLLM/Mistral/LM Studio honour it.
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`
}

// StreamOptions mirrors OpenAI's stream_options object.
type StreamOptions struct {
	// IncludeUsage requests a final SSE chunk carrying token usage.
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// ChatResponse represents a response from the chat completions endpoint.
type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Choice represents a single choice in the response.
type Choice struct {
	Index        int      `json:"index"`
	Message      *Message `json:"message,omitempty"`
	Delta        *Delta   `json:"delta,omitempty"`
	FinishReason string   `json:"finish_reason,omitempty"`
}

// Delta represents incremental content in a streaming response.
type Delta struct {
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	ToolCalls []ToolCallDelta `json:"tool_calls,omitempty"`
}

// Usage represents token usage statistics.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ModelInfo represents information about an available model.
type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// ModelsResponse represents the response from the models endpoint.
type ModelsResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

// APIError represents an error response from the LM Studio API.
type APIError struct {
	StatusCode int
	Message    string
	Type       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("LM Studio API error (status %d): %s", e.StatusCode, e.Message)
}

// NewClient creates a new client from configuration.
func NewClient(cfg *config.LMStudioConfig) *Client {
	maxTokens := cfg.EmbeddingMaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultEmbeddingMaxTokens
	}
	embeddingTimeout := cfg.EmbeddingTimeout
	if embeddingTimeout <= 0 {
		embeddingTimeout = defaultEmbeddingTimeout
	}
	return &Client{
		baseURL:                strings.TrimSuffix(cfg.URL, "/"),
		embeddingBaseURL:       strings.TrimSuffix(cfg.EmbeddingURL, "/"),
		timeout:                cfg.Timeout,
		maxRetries:             cfg.MaxRetries,
		embeddingModel:         cfg.EmbeddingModel,
		embeddingMaxTokens:     maxTokens,
		embeddingBatchSize:     cfg.EmbeddingBatchSize,
		apiKey:                 cfg.APIKey,
		omitChatTemplateKwargs: cfg.NoChatTemplateKwargs,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		embeddingHTTPClient: &http.Client{
			Timeout: embeddingTimeout,
		},
		// Timeout: 0 = no client-level deadline. Streaming responses must not
		// be killed by a fixed timer — the caller's context handles cancellation.
		streamHTTPClient: &http.Client{},
	}
}

// NewClientWithHTTP creates a new client with a custom HTTP client (useful for testing).
func NewClientWithHTTP(baseURL string, timeout time.Duration, maxRetries int, httpClient *http.Client) *Client {
	return &Client{
		baseURL:             strings.TrimSuffix(baseURL, "/"),
		embeddingBaseURL:    "",
		timeout:             timeout,
		maxRetries:          maxRetries,
		embeddingModel:      "",
		embeddingMaxTokens:  DefaultEmbeddingMaxTokens,
		httpClient:          httpClient,
		embeddingHTTPClient: httpClient,
		streamHTTPClient:    httpClient,
	}
}

// SetEmbeddingBaseURL overrides the embedding endpoint. Pass an empty string
// to revert to sharing the inference URL.
func (c *Client) SetEmbeddingBaseURL(url string) {
	c.embeddingBaseURL = strings.TrimSuffix(url, "/")
}

// StreamHandler is called for each chunk of a streaming response.
// delta contains the new content, done indicates if the stream is complete,
// and usage contains token counts (only populated when done is true).
type StreamHandler func(delta string, done bool, usage *Usage) error

// StreamChat sends a streaming chat request and calls the handler for each chunk.
func (c *Client) StreamChat(ctx context.Context, req ChatRequest, handler StreamHandler) error {
	return c.streamWith(ctx, req, func(body io.Reader) error {
		return processSSEStream(body, func(delta string, done bool, usage *Usage) error {
			if done {
				c.recordChatUsage(usage)
			}
			return handler(delta, done, usage)
		})
	})
}

// StreamChatRich is the tool-aware variant of StreamChat. The handler
// receives StreamEvent payloads exposing tool_calls and finish_reason in
// addition to text deltas. The plain StreamChat method is preserved for
// callers that don't need tool-call observability.
func (c *Client) StreamChatRich(ctx context.Context, req ChatRequest, handler StreamRichHandler) error {
	return c.streamWith(ctx, req, func(body io.Reader) error {
		return processSSEStreamRich(body, func(evt StreamEvent) error {
			if evt.Done {
				c.recordChatUsage(evt.Usage)
			}
			return handler(evt)
		})
	})
}

// streamWith handles the common request lifecycle (marshal, send, retry,
// dispatch to a stream parser). The supplied parser owns reading the response
// body; this function closes it after the parser returns.
func (c *Client) streamWith(ctx context.Context, req ChatRequest, parse func(io.Reader) error) error {
	req.Stream = true
	// Only opt into the terminal usage chunk when we have somewhere to record
	// it — keeps the request identical to before for callers without a recorder.
	if c.usageRecorder != nil && req.StreamOptions == nil {
		req.StreamOptions = &StreamOptions{IncludeUsage: true}
	}
	if c.omitChatTemplateKwargs {
		req.ChatTemplateKwargs = nil // hosted backend rejects the field
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	newRequest := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")
		c.setAuthHeader(httpReq)
		return httpReq, nil
	}

	httpReq, err := newRequest()
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(100<<(attempt-1)) * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			httpReq, err = newRequest()
			if err != nil {
				return err
			}
		}

		streamClient := c.streamHTTPClient
		if streamClient == nil {
			streamClient = c.httpClient
		}
		resp, err := streamClient.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			if isRetryableError(err) {
				continue
			}
			return lastErr
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			apiErr := parseAPIError(resp)
			if isRetryableStatus(resp.StatusCode) {
				lastErr = apiErr
				continue
			}
			return apiErr
		}

		err = parse(resp.Body)
		resp.Body.Close()
		if err != nil {
			// Stream errors are not retried since we may have already sent partial data
			return err
		}
		return nil
	}

	if lastErr != nil {
		return fmt.Errorf("all retries exhausted: %w", lastErr)
	}
	return fmt.Errorf("all retries exhausted")
}

// Chat sends a non-streaming chat request and returns the complete response.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	req.Stream = false
	if c.omitChatTemplateKwargs {
		req.ChatTemplateKwargs = nil // hosted backend rejects the field
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuthHeader(httpReq)

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(100<<(attempt-1)) * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}

			httpReq, err = http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
			if err != nil {
				return nil, fmt.Errorf("failed to create request: %w", err)
			}
			httpReq.Header.Set("Content-Type", "application/json")
			c.setAuthHeader(httpReq)
		}

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			if isRetryableError(err) {
				continue
			}
			return nil, lastErr
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			apiErr := parseAPIError(resp)
			if isRetryableStatus(resp.StatusCode) {
				lastErr = apiErr
				continue
			}
			return nil, apiErr
		}

		var chatResp ChatResponse
		if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}

		c.recordChatUsage(chatResp.Usage)
		return &chatResp, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all retries exhausted: %w", lastErr)
	}
	return nil, fmt.Errorf("all retries exhausted")
}

// ListModels returns the list of available models.
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	var lastErr error

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(100<<(attempt-1)) * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/models", nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		c.setAuthHeader(httpReq)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			if isRetryableError(err) {
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			apiErr := parseAPIError(resp)
			if isRetryableStatus(resp.StatusCode) {
				lastErr = apiErr
				continue
			}
			return nil, apiErr
		}

		var modelsResp ModelsResponse
		if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}
		resp.Body.Close()

		return modelsResp.Data, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all retries exhausted: %w", lastErr)
	}
	return nil, fmt.Errorf("all retries exhausted")
}

// Ping checks if the inference endpoint is available by making a quick
// request to /v1/models. Returns true if the server responds with 200.
func (c *Client) Ping(ctx context.Context) (bool, error) {
	return pingURL(ctx, c.httpClient, c.baseURL, c.apiKey)
}

// PingEmbedding checks if the embedding endpoint is reachable. It returns the
// same value as Ping when no dedicated embedding endpoint is configured.
func (c *Client) PingEmbedding(ctx context.Context) (bool, error) {
	return pingURL(ctx, c.httpClient, c.embeddingURL(), c.apiKey)
}

// pingURL probes an OpenAI-compatible endpoint via /v1/models. apiKey is sent
// as a bearer token when non-empty, so probes against hosted providers
// (Mistral, OpenAI…) aren't rejected with 401.
func pingURL(ctx context.Context, httpClient *http.Client, baseURL, apiKey string) (bool, error) {
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(pingCtx, http.MethodGet, baseURL+"/v1/models", nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return false, nil
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode == http.StatusOK, nil
}

// parseAPIError extracts error information from an HTTP response.
func parseAPIError(resp *http.Response) *APIError {
	apiErr := &APIError{
		StatusCode: resp.StatusCode,
		Message:    http.StatusText(resp.StatusCode),
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return apiErr
	}

	var errResp struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}

	if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
		apiErr.Message = errResp.Error.Message
		apiErr.Type = errResp.Error.Type
	}

	return apiErr
}

// isRetryableError checks if an error is transient and should be retried.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "temporary failure") ||
		// http.Client.Timeout fires as "context deadline exceeded (Client.Timeout exceeded…)"
		// This is a local transport timeout, not parent-context cancellation — retry it.
		strings.Contains(errStr, "Client.Timeout exceeded")
}

// isRetryableStatus checks if an HTTP status code indicates a retryable error.
func isRetryableStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests ||
		statusCode == http.StatusServiceUnavailable ||
		statusCode == http.StatusGatewayTimeout ||
		statusCode == http.StatusBadGateway
}
