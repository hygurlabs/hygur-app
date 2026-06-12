package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/mail"
	"github.com/hygur/sidecar/internal/store"
)

// mockLLMServer creates a test HTTP server that returns the given response.
func mockLLMServer(t *testing.T, responseJSON string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		resp := llm.ChatResponse{
			ID:      "test-id",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "test-model",
			Choices: []llm.Choice{
				{
					Index: 0,
					Message: &llm.Message{
						Role:    "assistant",
						Content: responseJSON,
					},
					FinishReason: "stop",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
}

func TestSummarizeThreadTool_Run(t *testing.T) {
	// Create a valid LLM response
	llmResponse := `{
		"decisions": ["Use Go for the backend", "Deploy to Kubernetes"],
		"actions": ["Set up CI/CD pipeline", "Create API documentation"],
		"open_questions": ["Which database to use?"]
	}`

	server := mockLLMServer(t, llmResponse)
	defer server.Close()

	// Create LLM client pointing to mock server
	llmClient := llm.NewClientWithHTTP(server.URL, 10*time.Second, 0, http.DefaultClient)

	// Create in-memory database
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// Create the tool
	tool := NewSummarizeThreadTool(llmClient, db)

	// Create test thread and messages
	thread := &mail.Thread{
		ID:           "thread-123",
		Subject:      "Project Planning Discussion",
		Participants: []string{"alice@example.com", "bob@example.com"},
		DateRange:    [2]time.Time{time.Now().Add(-24 * time.Hour), time.Now()},
		MessageCount: 3,
	}

	messages := []mail.Message{
		{
			ID:       "msg-1",
			ThreadID: "thread-123",
			From:     "alice@example.com",
			To:       []string{"bob@example.com"},
			Date:     time.Now().Add(-24 * time.Hour),
			Subject:  "Project Planning Discussion",
			Body:     "Let's use Go for the backend. What do you think?",
		},
		{
			ID:       "msg-2",
			ThreadID: "thread-123",
			From:     "bob@example.com",
			To:       []string{"alice@example.com"},
			Date:     time.Now().Add(-12 * time.Hour),
			Subject:  "Re: Project Planning Discussion",
			Body:     "Sounds good! We should deploy to Kubernetes. Which database should we use?",
		},
		{
			ID:       "msg-3",
			ThreadID: "thread-123",
			From:     "alice@example.com",
			To:       []string{"bob@example.com"},
			Date:     time.Now(),
			Subject:  "Re: Project Planning Discussion",
			Body:     "Great! Let's set up CI/CD and create API docs first.",
		},
	}

	// Run the tool
	ctx := context.Background()
	summary, err := tool.Run(ctx, thread, messages, "test-model")
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	// Verify the summary
	if summary.SummaryID == "" {
		t.Error("SummaryID should not be empty")
	}

	if summary.SourceRef != "email:thread-123" {
		t.Errorf("SourceRef = %q, want %q", summary.SourceRef, "email:thread-123")
	}

	if summary.ModelUsed != "test-model" {
		t.Errorf("ModelUsed = %q, want %q", summary.ModelUsed, "test-model")
	}

	if len(summary.Decisions) != 2 {
		t.Errorf("len(Decisions) = %d, want 2", len(summary.Decisions))
	}

	if len(summary.Actions) != 2 {
		t.Errorf("len(Actions) = %d, want 2", len(summary.Actions))
	}

	if len(summary.OpenQuestions) != 1 {
		t.Errorf("len(OpenQuestions) = %d, want 1", len(summary.OpenQuestions))
	}

	// Verify the summary was saved to the database
	savedSummary, err := db.GetSummary(ctx, summary.SummaryID)
	if err != nil {
		t.Fatalf("GetSummary() failed: %v", err)
	}

	if savedSummary == nil {
		t.Fatal("Summary was not saved to database")
	}

	if savedSummary.SourceRef != summary.SourceRef {
		t.Errorf("Saved SourceRef = %q, want %q", savedSummary.SourceRef, summary.SourceRef)
	}
}

func TestParseSummaryResponse_ValidJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantDec  int
		wantAct  int
		wantQues int
	}{
		{
			name: "clean JSON",
			input: `{
				"decisions": ["Decision 1", "Decision 2"],
				"actions": ["Action 1"],
				"open_questions": []
			}`,
			wantDec:  2,
			wantAct:  1,
			wantQues: 0,
		},
		{
			name: "empty arrays",
			input: `{
				"decisions": [],
				"actions": [],
				"open_questions": []
			}`,
			wantDec:  0,
			wantAct:  0,
			wantQues: 0,
		},
		{
			name: "JSON with surrounding text",
			input: `Here is the analysis:
			{
				"decisions": ["Use microservices"],
				"actions": ["Setup monitoring"],
				"open_questions": ["Budget?"]
			}
			Let me know if you need more details.`,
			wantDec:  1,
			wantAct:  1,
			wantQues: 1,
		},
		{
			name:     "JSON with markdown code block",
			input:    "```json\n{\"decisions\": [\"A\"], \"actions\": [\"B\"], \"open_questions\": [\"C\"]}\n```",
			wantDec:  1,
			wantAct:  1,
			wantQues: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := parseSummaryResponse(tt.input)
			if err != nil {
				t.Fatalf("parseSummaryResponse() error = %v", err)
			}

			if len(resp.Decisions) != tt.wantDec {
				t.Errorf("len(Decisions) = %d, want %d", len(resp.Decisions), tt.wantDec)
			}

			if len(resp.Actions) != tt.wantAct {
				t.Errorf("len(Actions) = %d, want %d", len(resp.Actions), tt.wantAct)
			}

			if len(resp.OpenQuestions) != tt.wantQues {
				t.Errorf("len(OpenQuestions) = %d, want %d", len(resp.OpenQuestions), tt.wantQues)
			}
		})
	}
}

func TestParseSummaryResponse_InvalidJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "not JSON at all",
			input: "This is just plain text without any JSON.",
		},
		{
			name:  "malformed JSON",
			input: `{"decisions": ["incomplete`,
		},
		{
			name:  "empty string",
			input: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSummaryResponse(tt.input)
			if err == nil {
				t.Error("parseSummaryResponse() should have returned an error")
			}
		})
	}
}

func TestParseSummaryResponse_WrongStructure(t *testing.T) {
	// JSON with wrong structure is valid JSON but produces empty slices
	input := `{"foo": "bar"}`
	resp, err := parseSummaryResponse(input)
	if err != nil {
		t.Fatalf("parseSummaryResponse() error = %v", err)
	}

	// Empty slices are expected for missing fields
	if len(resp.Decisions) != 0 {
		t.Errorf("len(Decisions) = %d, want 0", len(resp.Decisions))
	}
	if len(resp.Actions) != 0 {
		t.Errorf("len(Actions) = %d, want 0", len(resp.Actions))
	}
	if len(resp.OpenQuestions) != 0 {
		t.Errorf("len(OpenQuestions) = %d, want 0", len(resp.OpenQuestions))
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantLen int // Expected length if we just want to verify it extracted something
	}{
		{
			name:  "clean JSON",
			input: `{"key": "value"}`,
			want:  `{"key": "value"}`,
		},
		{
			name:  "JSON with prefix",
			input: `Some text before {"key": "value"}`,
			want:  `{"key": "value"}`,
		},
		{
			name:  "JSON with suffix",
			input: `{"key": "value"} some text after`,
			want:  `{"key": "value"}`,
		},
		{
			name:  "JSON with both",
			input: `prefix {"key": "value"} suffix`,
			want:  `{"key": "value"}`,
		},
		{
			name:  "nested JSON",
			input: `{"outer": {"inner": "value"}}`,
			want:  `{"outer": {"inner": "value"}}`,
		},
		{
			name:  "no JSON",
			input: "just plain text",
			want:  "",
		},
		{
			name:  "unmatched braces",
			input: "{ not closed",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSON(tt.input)
			if tt.wantLen > 0 {
				if len(got) != tt.wantLen {
					t.Errorf("extractJSON() len = %d, want %d", len(got), tt.wantLen)
				}
			} else if got != tt.want {
				t.Errorf("extractJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildSummaryPrompt(t *testing.T) {
	thread := &mail.Thread{
		ID:           "thread-456",
		Subject:      "Test Subject",
		Participants: []string{"user1@test.com", "user2@test.com"},
		DateRange:    [2]time.Time{time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)},
		MessageCount: 5,
	}

	normalizedText := "This is the normalized thread content."

	prompt := buildSummaryPrompt(thread, normalizedText)

	// Verify prompt contains expected elements
	if !strings.Contains(prompt, "Test Subject") {
		t.Error("Prompt should contain subject")
	}

	if !strings.Contains(prompt, "user1@test.com") {
		t.Error("Prompt should contain participants")
	}

	if !strings.Contains(prompt, "2024-01-01") {
		t.Error("Prompt should contain start date")
	}

	if !strings.Contains(prompt, "2024-01-15") {
		t.Error("Prompt should contain end date")
	}

	if !strings.Contains(prompt, "5") {
		t.Error("Prompt should contain message count")
	}

	if !strings.Contains(prompt, normalizedText) {
		t.Error("Prompt should contain normalized text")
	}
}

func TestBuildSummaryPrompt_LongText(t *testing.T) {
	thread := &mail.Thread{
		ID:           "thread-789",
		Subject:      "Long Thread",
		Participants: []string{"test@test.com"},
		DateRange:    [2]time.Time{time.Now(), time.Now()},
		MessageCount: 1,
	}

	// Create text longer than maxPromptTextLength
	longText := strings.Repeat("a", maxPromptTextLength+1000)

	prompt := buildSummaryPrompt(thread, longText)

	// Verify the text was truncated
	if strings.Contains(prompt, strings.Repeat("a", maxPromptTextLength+1)) {
		t.Error("Prompt should have truncated the long text")
	}

	if !strings.Contains(prompt, "...") {
		t.Error("Truncated text should end with ...")
	}
}

func TestBuildSummaryPrompt_NoParticipants(t *testing.T) {
	thread := &mail.Thread{
		ID:           "thread-empty",
		Subject:      "Empty Thread",
		Participants: []string{},
		DateRange:    [2]time.Time{time.Now(), time.Now()},
		MessageCount: 0,
	}

	prompt := buildSummaryPrompt(thread, "content")

	if !strings.Contains(prompt, "(no participant)") {
		t.Error("Prompt should indicate no participants")
	}
}

func TestSummarizeThreadTool_Run_EmptyMessages(t *testing.T) {
	server := mockLLMServer(t, `{"decisions": [], "actions": [], "open_questions": []}`)
	defer server.Close()

	llmClient := llm.NewClientWithHTTP(server.URL, 10*time.Second, 0, http.DefaultClient)

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := NewSummarizeThreadTool(llmClient, db)

	thread := &mail.Thread{
		ID:      "empty-thread",
		Subject: "Empty",
	}

	// Empty messages should return error from normalizer
	_, err = tool.Run(context.Background(), thread, []mail.Message{}, "test-model")
	if err == nil {
		t.Error("Run() with empty messages should return error")
	}
}

func TestSummarizeThreadTool_Run_LLMError(t *testing.T) {
	// Create a server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	llmClient := llm.NewClientWithHTTP(server.URL, 10*time.Second, 0, http.DefaultClient)

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := NewSummarizeThreadTool(llmClient, db)

	thread := &mail.Thread{
		ID:           "thread-error",
		Subject:      "Error Test",
		Participants: []string{"test@test.com"},
		DateRange:    [2]time.Time{time.Now(), time.Now()},
		MessageCount: 1,
	}

	messages := []mail.Message{
		{
			ID:       "msg-error",
			ThreadID: "thread-error",
			From:     "test@test.com",
			Date:     time.Now(),
			Body:     "Test message",
		},
	}

	_, err = tool.Run(context.Background(), thread, messages, "test-model")
	if err == nil {
		t.Error("Run() should return error when LLM fails")
	}
}

func TestSummarizeThreadTool_Run_InvalidJSONResponse(t *testing.T) {
	server := mockLLMServer(t, "This is not valid JSON at all")
	defer server.Close()

	llmClient := llm.NewClientWithHTTP(server.URL, 10*time.Second, 0, http.DefaultClient)

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := NewSummarizeThreadTool(llmClient, db)

	thread := &mail.Thread{
		ID:           "thread-invalid",
		Subject:      "Invalid JSON Test",
		Participants: []string{"test@test.com"},
		DateRange:    [2]time.Time{time.Now(), time.Now()},
		MessageCount: 1,
	}

	messages := []mail.Message{
		{
			ID:       "msg-invalid",
			ThreadID: "thread-invalid",
			From:     "test@test.com",
			Date:     time.Now(),
			Body:     "Test message",
		},
	}

	_, err = tool.Run(context.Background(), thread, messages, "test-model")
	if err == nil {
		t.Error("Run() should return error when LLM returns invalid JSON")
	}

	if !strings.Contains(err.Error(), "parse LLM response") {
		t.Errorf("Error should mention parsing failure, got: %v", err)
	}
}
