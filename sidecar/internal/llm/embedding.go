package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Default embedding configuration constants.
const (
	// DefaultEmbeddingModel is the fallback model if none is configured.
	DefaultEmbeddingModel = "text-embedding-nomic-embed-text-v1.5"

	// MaxBatchSize is the absolute ceiling of texts per embedding request,
	// regardless of the configured batch size.
	MaxBatchSize = 128

	// DefaultEmbeddingBatchSize is the number of chunks sent per /v1/embeddings
	// request when no override is configured. Larger batches mean far fewer HTTP
	// round-trips during bulk indexing (and big wins when the embedding server
	// batches on GPU). Tunable via lm_studio.embedding_batch_size.
	DefaultEmbeddingBatchSize = 32

	// ExpectedEmbeddingDimension is the expected dimension for nomic-embed-text-v1.5.
	ExpectedEmbeddingDimension = 768

	// DefaultEmbeddingMaxTokens matches the typical physical batch size of
	// local OpenAI-compatible embedding servers (llama.cpp, LM Studio) when
	// no override is provided via config. Inputs larger than this are
	// truncated before being sent.
	DefaultEmbeddingMaxTokens = 512

	// tokenEstimateCharsPerToken is a conservative char-to-token ratio used
	// to cap inputs client-side. It under-estimates token density (i.e.
	// over-estimates the budget in characters) on purpose: real tokenisers
	// emit ~3.5 chars/token on dense European text, so 3 is a safe floor.
	tokenEstimateCharsPerToken = 3
)

// Embedding-related errors.
var (
	// ErrEmbeddingModelUnavailable is returned when the embedding model is not loaded.
	ErrEmbeddingModelUnavailable = errors.New("embedding model is unavailable")

	// ErrEmptyEmbedding is returned when the API returns an empty embedding.
	ErrEmptyEmbedding = errors.New("empty embedding returned from API")

	// ErrEmptyInput is returned when no text is provided for embedding.
	ErrEmptyInput = errors.New("empty input text for embedding")

	// ErrBatchTooLarge is returned when the batch size exceeds MaxBatchSize.
	ErrBatchTooLarge = fmt.Errorf("batch size exceeds maximum of %d", MaxBatchSize)

	// ErrDimensionMismatch is returned when embedding dimension doesn't match expected.
	ErrDimensionMismatch = errors.New("embedding dimension mismatch")
)

// EmbeddingRequest represents a request to the embeddings endpoint.
type EmbeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// EmbeddingResponse represents a response from the embeddings endpoint.
type EmbeddingResponse struct {
	Object string          `json:"object"`
	Data   []EmbeddingData `json:"data"`
	Model  string          `json:"model"`
	Usage  *EmbeddingUsage `json:"usage,omitempty"`
}

// EmbeddingData represents a single embedding in the response.
type EmbeddingData struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

// EmbeddingUsage represents token usage for embedding requests.
type EmbeddingUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// SetEmbeddingModel sets the embedding model for a client.
// This should be called after client creation if using a custom model.
func (c *Client) SetEmbeddingModel(model string) {
	c.embeddingModel = model
}

// SetEmbeddingMaxTokens overrides the per-input token cap at runtime. Pass 0
// to restore the default.
func (c *Client) SetEmbeddingMaxTokens(n int) {
	if n <= 0 {
		c.embeddingMaxTokens = DefaultEmbeddingMaxTokens
		return
	}
	c.embeddingMaxTokens = n
}

// EmbeddingMaxTokens returns the active per-input token cap.
func (c *Client) EmbeddingMaxTokens() int {
	if c.embeddingMaxTokens <= 0 {
		return DefaultEmbeddingMaxTokens
	}
	return c.embeddingMaxTokens
}

// capInputToBudget truncates text so its estimated token count stays under
// the configured cap. Since we do not ship a real tokeniser, we use a
// conservative chars/token ratio (see tokenEstimateCharsPerToken) and trim
// at a UTF-8 boundary to avoid corrupt characters.
func (c *Client) capInputToBudget(text string) string {
	budget := c.EmbeddingMaxTokens()
	// Reserve ~20% headroom: the chars/token estimate undercounts for dense
	// content (URLs, tracking links, code), where the real tokenizer can emit
	// noticeably more tokens than estimated. Without this margin the server
	// rejected inputs (400, "larger than max context size") and the whole item
	// was rolled back. This is the hard guarantee; chunk sizing keeps most
	// inputs well below it.
	safeBudget := budget * 4 / 5
	if safeBudget < 1 {
		safeBudget = budget
	}
	maxBytes := safeBudget * tokenEstimateCharsPerToken
	if len(text) <= maxBytes {
		return text
	}
	// Step back to a rune boundary — len() is byte-based, slicing mid-rune
	// would corrupt the last character.
	cut := maxBytes
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut]
}

// isContextOverflowError reports whether an API error is the server rejecting
// an input for exceeding the embedding model's context window. The wording
// varies across servers (llama.cpp, LM Studio), so we match on stable phrases.
func isContextOverflowError(apiErr *APIError) bool {
	if apiErr == nil || apiErr.StatusCode != http.StatusBadRequest {
		return false
	}
	m := strings.ToLower(apiErr.Message)
	return strings.Contains(m, "context size") ||
		strings.Contains(m, "max context") ||
		strings.Contains(m, "context length") ||
		(strings.Contains(m, "larger than") && strings.Contains(m, "token"))
}

// shrinkTexts trims each text to `factor` of its current byte length, stepping
// back to a UTF-8 rune boundary. Used to recover from a context-overflow
// rejection without dropping the whole batch.
func shrinkTexts(texts []string, factor float64) []string {
	out := make([]string, len(texts))
	for i, t := range texts {
		cut := int(float64(len(t)) * factor)
		if cut >= len(t) {
			out[i] = t
			continue
		}
		for cut > 0 && (t[cut]&0xC0) == 0x80 {
			cut--
		}
		out[i] = t[:cut]
	}
	return out
}

// GetEmbeddingModel returns the configured embedding model.
// Falls back to DefaultEmbeddingModel if none is configured.
func (c *Client) GetEmbeddingModel() string {
	if c.embeddingModel != "" {
		return c.embeddingModel
	}
	return DefaultEmbeddingModel
}

// GenerateEmbedding generates an embedding for a single text.
func (c *Client) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, ErrEmptyInput
	}

	embeddings, err := c.GenerateEmbeddings(ctx, []string{text})
	if err != nil {
		return nil, err
	}

	if len(embeddings) == 0 {
		return nil, ErrEmptyEmbedding
	}

	return embeddings[0], nil
}

// GenerateEmbeddings generates embeddings for multiple texts (batch).
// Max 10 texts per request to avoid timeouts.
func (c *Client) GenerateEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, ErrEmptyInput
	}

	if len(texts) > MaxBatchSize {
		return nil, ErrBatchTooLarge
	}

	// Filter empty texts and cap each input to the configured token budget so
	// the embedding endpoint never rejects a request for exceeding its
	// physical batch size. Truncation preserves the prefix, which is the most
	// information-dense part of mail bodies, file openings, and markdown
	// sections.
	var validTexts []string
	var validIndices []int
	for i, t := range texts {
		if t == "" {
			continue
		}
		validTexts = append(validTexts, c.capInputToBudget(t))
		validIndices = append(validIndices, i)
	}

	if len(validTexts) == 0 {
		return nil, ErrEmptyInput
	}

	embURL := c.embeddingURL() + "/v1/embeddings"
	embHTTP := c.embeddingHTTPClient
	if embHTTP == nil {
		embHTTP = c.httpClient
	}

	// currentInput starts at the budget-capped texts. If the server rejects the
	// batch for exceeding its context window (its tokeniser counts more tokens
	// than our char-based estimate — common for dense mail: long Message-IDs,
	// tracking URLs), we shrink every input and retry. This is the hard
	// guarantee that one oversized chunk can't roll back the whole item.
	currentInput := validTexts

	var lastErr error
	attempt := 0          // transient-error retries (capped at maxRetries, with backoff)
	overflowShrinks := 0  // context-overflow shrinks (own budget, no backoff)
	const maxOverflowShrinks = 6

	for {
		body, err := json.Marshal(EmbeddingRequest{Model: c.GetEmbeddingModel(), Input: currentInput})
		if err != nil {
			return nil, fmt.Errorf("failed to marshal embedding request: %w", err)
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, embURL, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create embedding request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		c.setAuthHeader(httpReq)

		resp, err := embHTTP.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("embedding request failed: %w", err)
			if isRetryableError(err) && attempt < c.maxRetries {
				attempt++
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-timeAfter(100 << (attempt - 1)):
				}
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return nil, ErrEmbeddingModelUnavailable
		}

		if resp.StatusCode != http.StatusOK {
			apiErr := parseAPIError(resp)
			resp.Body.Close()

			// Model unavailable surfaced as a 400 message.
			if apiErr.StatusCode == http.StatusBadRequest && containsModelError(apiErr.Message) {
				return nil, ErrEmbeddingModelUnavailable
			}

			// Context overflow: shrink each input ~30% and retry. Converges fast
			// and the floor guarantees termination; no backoff needed.
			if isContextOverflowError(apiErr) && overflowShrinks < maxOverflowShrinks {
				overflowShrinks++
				currentInput = shrinkTexts(currentInput, 0.7)
				continue
			}

			if isRetryableStatus(resp.StatusCode) && attempt < c.maxRetries {
				lastErr = apiErr
				attempt++
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-timeAfter(100 << (attempt - 1)):
				}
				continue
			}
			return nil, apiErr
		}

		var embResp EmbeddingResponse
		if err := json.NewDecoder(resp.Body).Decode(&embResp); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode embedding response: %w", err)
		}
		resp.Body.Close()

		if len(embResp.Data) == 0 {
			return nil, ErrEmptyEmbedding
		}

		// Sort by index and extract embeddings, mapping back to original indices.
		result := make([][]float32, len(texts))
		for _, data := range embResp.Data {
			if data.Index < 0 || data.Index >= len(validIndices) {
				continue
			}
			if len(data.Embedding) == 0 {
				return nil, ErrEmptyEmbedding
			}
			result[validIndices[data.Index]] = data.Embedding
		}

		// Check that we got all expected embeddings.
		for i, idx := range validIndices {
			if result[idx] == nil {
				return nil, fmt.Errorf("missing embedding for text at index %d", i)
			}
		}

		return result, nil
	}
}

// ValidateEmbeddingDimension checks if the embedding has the expected dimension.
func ValidateEmbeddingDimension(embedding []float32) error {
	if len(embedding) == 0 {
		return ErrEmptyEmbedding
	}
	if len(embedding) != ExpectedEmbeddingDimension {
		return fmt.Errorf("%w: got %d, expected %d", ErrDimensionMismatch, len(embedding), ExpectedEmbeddingDimension)
	}
	return nil
}

// containsModelError checks if an error message indicates a model issue.
func containsModelError(msg string) bool {
	modelErrors := []string{
		"model not found",
		"model not loaded",
		"no model loaded",
		"invalid model",
	}
	for _, e := range modelErrors {
		if containsIgnoreCase(msg, e) {
			return true
		}
	}
	return false
}

// containsIgnoreCase checks if s contains substr (case-insensitive).
func containsIgnoreCase(s, substr string) bool {
	sLower := toLower(s)
	substrLower := toLower(substr)
	return bytesContains([]byte(sLower), []byte(substrLower))
}

// toLower converts a string to lowercase without importing strings.
func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// bytesContains checks if b contains subslice.
func bytesContains(b, subslice []byte) bool {
	for i := 0; i <= len(b)-len(subslice); i++ {
		if bytesEqual(b[i:i+len(subslice)], subslice) {
			return true
		}
	}
	return false
}

// bytesEqual checks if two byte slices are equal.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// timeAfter is a wrapper for time.After to allow testing.
// It's defined as a variable so it can be replaced in tests.
var timeAfter = func(ms int) <-chan time.Time {
	return time.After(time.Duration(ms) * time.Millisecond)
}
