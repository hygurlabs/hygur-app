package retrieval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLLMServer returns an httptest.Server that replies with a fixed chat response.
func fakeLLMServer(t *testing.T, responseContent string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"id":      "test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]string{
						"role":    "assistant",
						"content": responseContent,
					},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// errorLLMServer returns an httptest.Server that always replies with 500.
func errorLLMServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
}

func newTestLLMClient(t *testing.T, server *httptest.Server) *llm.Client {
	t.Helper()
	return llm.NewClientWithHTTP(server.URL, 5*time.Second, 1, server.Client())
}

func TestRewrite_NoHistory_ReturnsLatestUnchanged(t *testing.T) {
	// With no history, the function returns immediately without any LLM call.
	srv := errorLLMServer(t)
	defer srv.Close()
	client := newTestLLMClient(t, srv)

	result, err := RewriteStandaloneQuery(context.Background(), client, nil, "What is the TVA amount?")

	require.NoError(t, err)
	assert.Equal(t, "What is the TVA amount?", result)
}

func TestRewrite_FollowUp_ReturnsLLMRewrite(t *testing.T) {
	// LLM returns a rewrite that incorporates prior topic.
	rewrite := "What is the IBAN and structured communication reference for the TVA précompte payment?"
	srv := fakeLLMServer(t, rewrite)
	defer srv.Close()
	client := newTestLLMClient(t, srv)

	history := []llm.Message{
		{Role: "user", Content: "What is the VAT I should send to?"},
		{Role: "assistant", Content: "The VAT précompte amount is €1,234. You should pay it to the Belgian tax authority."},
	}

	result, err := RewriteStandaloneQuery(context.Background(), client, history, "Okay, and can you show me the account number and communication I should use?")

	require.NoError(t, err)
	assert.Equal(t, rewrite, result)
}

func TestRewrite_LLMError_FallbackToLatest(t *testing.T) {
	srv := errorLLMServer(t)
	defer srv.Close()
	client := newTestLLMClient(t, srv)

	history := []llm.Message{
		{Role: "user", Content: "Tell me about TVA"},
		{Role: "assistant", Content: "TVA is a Belgian value-added tax."},
	}
	latest := "And the IBAN?"

	result, err := RewriteStandaloneQuery(context.Background(), client, history, latest)

	// Should return the original message, with a non-nil error.
	require.Error(t, err)
	assert.Equal(t, latest, result)
}

func TestRewrite_TopicShift_LLMReturnsUnchanged(t *testing.T) {
	// When LLM judges the question is self-contained (topic shift), it returns it unchanged.
	newQuery := "Show me my latest Stripe invoices"
	srv := fakeLLMServer(t, newQuery)
	defer srv.Close()
	client := newTestLLMClient(t, srv)

	history := []llm.Message{
		{Role: "user", Content: "What is the TVA amount?"},
		{Role: "assistant", Content: "The TVA amount is €1,234."},
	}

	result, err := RewriteStandaloneQuery(context.Background(), client, history, newQuery)

	require.NoError(t, err)
	assert.Equal(t, newQuery, result)
}

func TestRewrite_HistoryTruncation(t *testing.T) {
	// Verify that only the last rewriteMaxHistoryMsgs messages are included in the prompt.
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if msgs, ok := req["messages"].([]any); ok {
			// Find the user message (second one = the user prompt with conversation)
			for _, m := range msgs {
				msg := m.(map[string]any)
				if msg["role"] == "user" {
					capturedBody = msg["content"].(string)
				}
			}
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{"index": 0, "message": map[string]string{"role": "assistant", "content": "rewritten query"}, "finish_reason": "stop"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	client := newTestLLMClient(t, srv)

	// Build a history with more messages than rewriteMaxHistoryMsgs (4).
	history := []llm.Message{
		{Role: "user", Content: "msg-a"}, // should be excluded
		{Role: "assistant", Content: "msg-b"},
		{Role: "user", Content: "msg-c"},
		{Role: "assistant", Content: "msg-d"},
		{Role: "user", Content: "msg-e"}, // last 4 start here
		{Role: "assistant", Content: "msg-f"},
		{Role: "user", Content: "msg-g"},
		{Role: "assistant", Content: "msg-h"},
	}

	_, _ = RewriteStandaloneQuery(context.Background(), client, history, "follow-up")

	// The oldest messages should not appear in the prompt.
	assert.NotContains(t, capturedBody, "msg-a")
	assert.NotContains(t, capturedBody, "msg-b")
	assert.NotContains(t, capturedBody, "msg-c")
	assert.NotContains(t, capturedBody, "msg-d")
	// The last 4 should be present.
	assert.True(t, strings.Contains(capturedBody, "msg-e") || strings.Contains(capturedBody, "msg-f") ||
		strings.Contains(capturedBody, "msg-g") || strings.Contains(capturedBody, "msg-h"),
		"expected at least one of the last 4 messages to appear in the prompt")
}

// fakeLLMServerReasoning replies with content="" and the answer in reasoning,
// mimicking Nemotron-omni / reasoning-capable backends.
func fakeLLMServerReasoning(t *testing.T, reasoning string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":      "assistant",
						"content":   "",
						"reasoning": reasoning,
					},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestRewrite_FallsBackToReasoningWhenContentEmpty(t *testing.T) {
	rewrite := "TVA précompte IBAN paiement Q1"
	srv := fakeLLMServerReasoning(t, rewrite)
	defer srv.Close()
	client := newTestLLMClient(t, srv)

	history := []llm.Message{
		{Role: "user", Content: "What is the VAT IBAN?"},
		{Role: "assistant", Content: "BE22..."},
	}

	result, err := RewriteStandaloneQuery(context.Background(), client, history, "and the communication?")

	require.NoError(t, err)
	assert.Equal(t, rewrite, result)
}

func TestRewrite_StripsThinkBlockLeak(t *testing.T) {
	// Some reasoning backends leak a stray </think> marker around the answer.
	leaked := "<think>let me think about this</think>\nTVA IBAN compte"
	srv := fakeLLMServer(t, leaked)
	defer srv.Close()
	client := newTestLLMClient(t, srv)

	history := []llm.Message{
		{Role: "user", Content: "Tell me about TVA"},
		{Role: "assistant", Content: "TVA is..."},
	}

	result, err := RewriteStandaloneQuery(context.Background(), client, history, "and the IBAN?")

	require.NoError(t, err)
	assert.Equal(t, "TVA IBAN compte", result)
}

func TestRewrite_BothContentAndReasoningEmpty_ReturnsLatestWithError(t *testing.T) {
	srv := fakeLLMServerReasoning(t, "")
	defer srv.Close()
	client := newTestLLMClient(t, srv)

	history := []llm.Message{
		{Role: "user", Content: "What is X?"},
		{Role: "assistant", Content: "X is Y."},
	}
	latest := "and Z?"

	result, err := RewriteStandaloneQuery(context.Background(), client, history, latest)

	require.Error(t, err)
	assert.Equal(t, latest, result)
}

func TestRewrite_LongMessageTruncation(t *testing.T) {
	// Messages exceeding rewriteMaxMsgChars should be truncated.
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if msgs, ok := req["messages"].([]any); ok {
			for _, m := range msgs {
				msg := m.(map[string]any)
				if msg["role"] == "user" {
					capturedBody = msg["content"].(string)
				}
			}
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{"index": 0, "message": map[string]string{"role": "assistant", "content": "ok"}, "finish_reason": "stop"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	client := newTestLLMClient(t, srv)

	longMessage := strings.Repeat("x", rewriteMaxMsgChars+100)
	history := []llm.Message{
		{Role: "user", Content: longMessage},
		{Role: "assistant", Content: "short reply"},
	}

	_, _ = RewriteStandaloneQuery(context.Background(), client, history, "follow-up")

	// The truncation marker should be present; the full long message should not.
	assert.Contains(t, capturedBody, "…")
	assert.NotContains(t, capturedBody, strings.Repeat("x", rewriteMaxMsgChars+1))
}
