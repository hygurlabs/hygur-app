package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGenerateEmbedding_Success(t *testing.T) {
	// Create a mock server
	expectedEmbedding := make([]float32, ExpectedEmbeddingDimension)
	for i := range expectedEmbedding {
		expectedEmbedding[i] = float32(i) * 0.001
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}

		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req EmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if len(req.Input) != 1 {
			t.Errorf("expected 1 input, got %d", len(req.Input))
		}

		resp := EmbeddingResponse{
			Object: "list",
			Data: []EmbeddingData{
				{
					Object:    "embedding",
					Index:     0,
					Embedding: expectedEmbedding,
				},
			},
			Model: req.Model,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClientWithHTTP(server.URL, 5*time.Second, 3, server.Client())
	client.SetEmbeddingModel("test-model")

	ctx := context.Background()
	embedding, err := client.GenerateEmbedding(ctx, "test text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(embedding) != ExpectedEmbeddingDimension {
		t.Errorf("expected %d dimensions, got %d", ExpectedEmbeddingDimension, len(embedding))
	}

	for i, v := range embedding {
		expected := float32(i) * 0.001
		if v != expected {
			t.Errorf("embedding[%d] = %f, expected %f", i, v, expected)
			break
		}
	}
}

func TestGenerateEmbedding_EmptyInput(t *testing.T) {
	client := NewClientWithHTTP("http://localhost", 5*time.Second, 0, http.DefaultClient)

	ctx := context.Background()
	_, err := client.GenerateEmbedding(ctx, "")
	if err != ErrEmptyInput {
		t.Errorf("expected ErrEmptyInput, got %v", err)
	}
}

func TestGenerateEmbeddings_Batch(t *testing.T) {
	expectedDim := 768
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req EmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		data := make([]EmbeddingData, len(req.Input))
		for i := range req.Input {
			embedding := make([]float32, expectedDim)
			for j := range embedding {
				embedding[j] = float32(i*1000 + j)
			}
			data[i] = EmbeddingData{
				Object:    "embedding",
				Index:     i,
				Embedding: embedding,
			}
		}

		resp := EmbeddingResponse{
			Object: "list",
			Data:   data,
			Model:  req.Model,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClientWithHTTP(server.URL, 5*time.Second, 0, server.Client())

	ctx := context.Background()
	texts := []string{"text 1", "text 2", "text 3"}
	embeddings, err := client.GenerateEmbeddings(ctx, texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(embeddings) != len(texts) {
		t.Errorf("expected %d embeddings, got %d", len(texts), len(embeddings))
	}

	for i, emb := range embeddings {
		if len(emb) != expectedDim {
			t.Errorf("embedding %d has %d dimensions, expected %d", i, len(emb), expectedDim)
		}
		// Check first value to verify correct ordering
		expected := float32(i * 1000)
		if emb[0] != expected {
			t.Errorf("embedding[%d][0] = %f, expected %f", i, emb[0], expected)
		}
	}
}

// TestGenerateEmbeddings_ContextOverflowRecovery verifies that when the server
// rejects an input for exceeding its context window, the client shrinks the
// inputs and retries until they fit — instead of failing the whole batch (which
// would roll back the knowledge item).
func TestGenerateEmbeddings_ContextOverflowRecovery(t *testing.T) {
	const fitsAt = 100 // bytes: server accepts once every input is this short
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req EmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// Reject while any input is still too long (mimics the 512-token cap).
		for _, in := range req.Input {
			if len(in) > fitsAt {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":{"message":"input (534 tokens) is larger than the max context size (512 tokens)"}}`))
				return
			}
		}
		data := make([]EmbeddingData, len(req.Input))
		for i := range req.Input {
			emb := make([]float32, ExpectedEmbeddingDimension)
			data[i] = EmbeddingData{Object: "embedding", Index: i, Embedding: emb}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(EmbeddingResponse{Object: "list", Data: data, Model: req.Model})
	}))
	defer server.Close()

	client := NewClientWithHTTP(server.URL, 5*time.Second, 0, server.Client())

	// A long input that overflows on the first attempt and must be shrunk.
	long := strings.Repeat("https://tracking.example.com/AS8PR03MB6855/click?id=", 8)
	embeddings, err := client.GenerateEmbeddings(context.Background(), []string{long})
	if err != nil {
		t.Fatalf("expected recovery via shrink, got error: %v", err)
	}
	if len(embeddings) != 1 || len(embeddings[0]) != ExpectedEmbeddingDimension {
		t.Fatalf("expected 1 embedding of dim %d, got %d", ExpectedEmbeddingDimension, len(embeddings))
	}
	if calls < 2 {
		t.Errorf("expected at least one shrink-retry, server saw %d call(s)", calls)
	}
}

// TestGenerateEmbeddings_ContextOverflowGivesUp verifies termination: if the
// server always rejects for overflow, the client stops after the shrink budget
// rather than looping forever.
func TestGenerateEmbeddings_ContextOverflowGivesUp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"input is larger than the max context size (512 tokens)"}}`))
	}))
	defer server.Close()

	client := NewClientWithHTTP(server.URL, 5*time.Second, 0, server.Client())
	_, err := client.GenerateEmbeddings(context.Background(), []string{"anything"})
	if err == nil {
		t.Fatal("expected an error when the server always rejects for overflow")
	}
}

// Regression: llama.cpp surfaces a ubatch overflow as a 500 "too large to
// process / physical batch size", not a 400. It was previously not recognised
// as an overflow, so the shrink-and-retry never fired and the whole knowledge
// item was rolled back. It must now recover by shrinking, like the 400 case.
func TestGenerateEmbeddings_BatchSizeOverflow500Recovery(t *testing.T) {
	const fitsAt = 100
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req EmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		for _, in := range req.Input {
			if len(in) > fitsAt {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":{"message":"input (548 tokens) is too large to process. increase the physical batch size (current batch size: 512)"}}`))
				return
			}
		}
		data := make([]EmbeddingData, len(req.Input))
		for i := range req.Input {
			data[i] = EmbeddingData{Object: "embedding", Index: i, Embedding: make([]float32, ExpectedEmbeddingDimension)}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(EmbeddingResponse{Object: "list", Data: data, Model: req.Model})
	}))
	defer server.Close()

	client := NewClientWithHTTP(server.URL, 5*time.Second, 0, server.Client())
	long := strings.Repeat("https://tracking.example.com/AS8PR03MB6855/click?id=", 8)
	embeddings, err := client.GenerateEmbeddings(context.Background(), []string{long})
	if err != nil {
		t.Fatalf("expected recovery via shrink on the 500 batch-size overflow, got: %v", err)
	}
	if len(embeddings) != 1 || len(embeddings[0]) != ExpectedEmbeddingDimension {
		t.Fatalf("expected 1 embedding of dim %d, got %d", ExpectedEmbeddingDimension, len(embeddings))
	}
	if calls < 2 {
		t.Errorf("expected a shrink-retry, server saw %d call(s)", calls)
	}
}

func TestIsContextOverflowError(t *testing.T) {
	cases := []struct {
		name string
		code int
		msg  string
		want bool
	}{
		{"400 max context size", http.StatusBadRequest, "input (534 tokens) is larger than the max context size (512 tokens)", true},
		{"500 too large / physical batch", http.StatusInternalServerError, "input (548 tokens) is too large to process. increase the physical batch size (current batch size: 512)", true},
		{"400 context length", http.StatusBadRequest, "this model's maximum context length is 512 tokens", true},
		{"500 generic server error is NOT overflow", http.StatusInternalServerError, "internal server error", false},
		{"400 model not found is NOT overflow", http.StatusBadRequest, "the model `x` does not exist", false},
		{"503 unavailable is NOT overflow", http.StatusServiceUnavailable, "service unavailable", false},
	}
	for _, tc := range cases {
		if got := isContextOverflowError(&APIError{StatusCode: tc.code, Message: tc.msg}); got != tc.want {
			t.Errorf("%s: isContextOverflowError(%d,%q) = %v, want %v", tc.name, tc.code, tc.msg, got, tc.want)
		}
	}
}

func TestGenerateEmbeddings_BatchTooLarge(t *testing.T) {
	client := NewClientWithHTTP("http://localhost", 5*time.Second, 0, http.DefaultClient)

	ctx := context.Background()
	texts := make([]string, MaxBatchSize+1)
	for i := range texts {
		texts[i] = "text"
	}

	_, err := client.GenerateEmbeddings(ctx, texts)
	if err != ErrBatchTooLarge {
		t.Errorf("expected ErrBatchTooLarge, got %v", err)
	}
}

func TestGenerateEmbedding_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClientWithHTTP(server.URL, 5*time.Second, 0, server.Client())

	ctx := context.Background()
	_, err := client.GenerateEmbedding(ctx, "test")
	if err == nil {
		t.Error("expected error, got nil")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Errorf("expected APIError, got %T", err)
	} else if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status %d, got %d", http.StatusInternalServerError, apiErr.StatusCode)
	}
}

func TestGenerateEmbedding_ModelUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClientWithHTTP(server.URL, 5*time.Second, 0, server.Client())

	ctx := context.Background()
	_, err := client.GenerateEmbedding(ctx, "test")
	if err != ErrEmbeddingModelUnavailable {
		t.Errorf("expected ErrEmbeddingModelUnavailable, got %v", err)
	}
}

func TestGenerateEmbedding_ModelUnavailableFromMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{
				"message": "Model not found",
				"type":    "invalid_request_error",
			},
		})
	}))
	defer server.Close()

	client := NewClientWithHTTP(server.URL, 5*time.Second, 0, server.Client())

	ctx := context.Background()
	_, err := client.GenerateEmbedding(ctx, "test")
	if err != ErrEmbeddingModelUnavailable {
		t.Errorf("expected ErrEmbeddingModelUnavailable, got %v", err)
	}
}

func TestGenerateEmbedding_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := EmbeddingResponse{
			Object: "list",
			Data:   []EmbeddingData{},
			Model:  "test-model",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClientWithHTTP(server.URL, 5*time.Second, 0, server.Client())

	ctx := context.Background()
	_, err := client.GenerateEmbedding(ctx, "test")
	if err != ErrEmptyEmbedding {
		t.Errorf("expected ErrEmptyEmbedding, got %v", err)
	}
}

func TestGenerateEmbedding_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClientWithHTTP(server.URL, 5*time.Second, 0, server.Client())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := client.GenerateEmbedding(ctx, "test")
	if err == nil {
		t.Error("expected error, got nil")
	}
	// The error should contain "context canceled"
	if !containsIgnoreCase(err.Error(), "context canceled") {
		t.Errorf("expected context canceled error, got %v", err)
	}
}

func TestGenerateEmbedding_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create client with very short timeout
	httpClient := &http.Client{Timeout: 50 * time.Millisecond}
	client := NewClientWithHTTP(server.URL, 50*time.Millisecond, 0, httpClient)

	ctx := context.Background()
	_, err := client.GenerateEmbedding(ctx, "test")
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

func TestGenerateEmbeddings_WithEmptyTexts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req EmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Only non-empty texts should be sent
		if len(req.Input) != 2 {
			t.Errorf("expected 2 inputs (non-empty), got %d", len(req.Input))
		}

		data := make([]EmbeddingData, len(req.Input))
		for i := range req.Input {
			embedding := make([]float32, 768)
			data[i] = EmbeddingData{
				Object:    "embedding",
				Index:     i,
				Embedding: embedding,
			}
		}

		resp := EmbeddingResponse{
			Object: "list",
			Data:   data,
			Model:  req.Model,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClientWithHTTP(server.URL, 5*time.Second, 0, server.Client())

	ctx := context.Background()
	// Include an empty text in the middle
	texts := []string{"text 1", "", "text 2"}
	embeddings, err := client.GenerateEmbeddings(ctx, texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Result should have same length as input
	if len(embeddings) != 3 {
		t.Errorf("expected 3 embeddings (with nil for empty), got %d", len(embeddings))
	}

	// Check that non-empty texts got embeddings
	if embeddings[0] == nil {
		t.Error("expected embedding for index 0")
	}
	if embeddings[1] != nil {
		t.Error("expected nil for empty text at index 1")
	}
	if embeddings[2] == nil {
		t.Error("expected embedding for index 2")
	}
}

func TestValidateEmbeddingDimension(t *testing.T) {
	tests := []struct {
		name      string
		embedding []float32
		wantErr   error
	}{
		{
			name:      "valid dimension",
			embedding: make([]float32, ExpectedEmbeddingDimension),
			wantErr:   nil,
		},
		{
			name:      "empty embedding",
			embedding: []float32{},
			wantErr:   ErrEmptyEmbedding,
		},
		{
			name:      "nil embedding",
			embedding: nil,
			wantErr:   ErrEmptyEmbedding,
		},
		{
			name:      "wrong dimension",
			embedding: make([]float32, 512),
			wantErr:   ErrDimensionMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEmbeddingDimension(tt.embedding)
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error %v, got nil", tt.wantErr)
				} else if tt.wantErr == ErrDimensionMismatch {
					// Check if error wraps ErrDimensionMismatch
					if !containsIgnoreCase(err.Error(), "dimension mismatch") {
						t.Errorf("expected dimension mismatch error, got %v", err)
					}
				} else if err != tt.wantErr {
					t.Errorf("expected error %v, got %v", tt.wantErr, err)
				}
			}
		})
	}
}

func TestGetEmbeddingModel(t *testing.T) {
	t.Run("default model", func(t *testing.T) {
		client := NewClientWithHTTP("http://localhost", 5*time.Second, 0, http.DefaultClient)
		if model := client.GetEmbeddingModel(); model != DefaultEmbeddingModel {
			t.Errorf("expected %s, got %s", DefaultEmbeddingModel, model)
		}
	})

	t.Run("custom model", func(t *testing.T) {
		client := NewClientWithHTTP("http://localhost", 5*time.Second, 0, http.DefaultClient)
		client.SetEmbeddingModel("custom-model")
		if model := client.GetEmbeddingModel(); model != "custom-model" {
			t.Errorf("expected custom-model, got %s", model)
		}
	})
}

func TestGenerateEmbedding_Retry(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			// Fail first 2 attempts with retryable error
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		// Success on 3rd attempt
		embedding := make([]float32, ExpectedEmbeddingDimension)
		resp := EmbeddingResponse{
			Object: "list",
			Data: []EmbeddingData{
				{
					Object:    "embedding",
					Index:     0,
					Embedding: embedding,
				},
			},
			Model: "test-model",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Override timeAfter to speed up test
	originalTimeAfter := timeAfter
	timeAfter = func(ms int) <-chan time.Time {
		return time.After(1 * time.Millisecond)
	}
	defer func() { timeAfter = originalTimeAfter }()

	client := NewClientWithHTTP(server.URL, 5*time.Second, 3, server.Client())

	ctx := context.Background()
	embedding, err := client.GenerateEmbedding(ctx, "test")
	if err != nil {
		t.Fatalf("unexpected error after retries: %v", err)
	}

	if len(embedding) != ExpectedEmbeddingDimension {
		t.Errorf("expected %d dimensions, got %d", ExpectedEmbeddingDimension, len(embedding))
	}

	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}
