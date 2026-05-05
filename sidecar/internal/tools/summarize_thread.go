package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/mail"
	"github.com/hygur/sidecar/internal/store"
)

// SummarizeThreadTool generates structured summaries of email threads using LLM.
type SummarizeThreadTool struct {
	llm        *llm.Client
	store      *store.DB
	normalizer *mail.ThreadNormalizer
}

// NewSummarizeThreadTool creates a new SummarizeThreadTool with the given dependencies.
func NewSummarizeThreadTool(llmClient *llm.Client, db *store.DB) *SummarizeThreadTool {
	return &SummarizeThreadTool{
		llm:        llmClient,
		store:      db,
		normalizer: mail.NewThreadNormalizer(),
	}
}

// summaryResponse represents the expected JSON structure from the LLM response.
type summaryResponse struct {
	Decisions     []string `json:"decisions"`
	Actions       []string `json:"actions"`
	OpenQuestions []string `json:"open_questions"`
}

// Run generates a summary for the given email thread and messages.
// It normalizes the thread content, sends it to the LLM for analysis,
// parses the structured response, and saves the summary to the database.
func (t *SummarizeThreadTool) Run(ctx context.Context, thread *mail.Thread, messages []mail.Message, model string) (*store.Summary, error) {
	// Step 1: Normalize the thread content
	normalizedText, err := t.normalizer.Normalize(thread, messages)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize thread: %w", err)
	}

	// Step 2: Build the prompt
	prompt := buildSummaryPrompt(thread, normalizedText)

	// Step 3: Call the LLM
	resp, err := t.llm.Chat(ctx, llm.ChatRequest{
		Model: model,
		Messages: []llm.Message{
			{Role: "system", Content: summarySystemPrompt},
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call LLM: %w", err)
	}

	// Extract the response content
	if len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return nil, fmt.Errorf("empty response from LLM")
	}
	responseContent := resp.Choices[0].Message.Content

	// Step 4: Parse the JSON response
	parsed, err := parseSummaryResponse(responseContent)
	if err != nil {
		return nil, fmt.Errorf("failed to parse LLM response: %w", err)
	}

	// Step 5: Create the Summary
	summary := &store.Summary{
		SummaryID:     uuid.New().String(),
		SourceRef:     "email:" + thread.ID,
		ModelUsed:     model,
		Decisions:     parsed.Decisions,
		Actions:       parsed.Actions,
		OpenQuestions: parsed.OpenQuestions,
		CreatedAt:     time.Now(),
	}

	// Step 6: Save to database
	if err := t.store.InsertSummary(ctx, summary); err != nil {
		return nil, fmt.Errorf("failed to save summary: %w", err)
	}

	return summary, nil
}

// parseSummaryResponse parses the JSON response from the LLM.
// It handles cases where the response may contain extra text around the JSON.
func parseSummaryResponse(content string) (*summaryResponse, error) {
	// Try direct unmarshal first
	var resp summaryResponse
	if err := json.Unmarshal([]byte(content), &resp); err == nil {
		return &resp, nil
	}

	// Try to extract JSON from the response (in case LLM added extra text)
	jsonContent := extractJSON(content)
	if jsonContent == "" {
		return nil, fmt.Errorf("no valid JSON found in response: %s", truncateForError(content))
	}

	if err := json.Unmarshal([]byte(jsonContent), &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON in response: %w", err)
	}

	return &resp, nil
}

// extractJSON attempts to extract a JSON object from a string that may contain
// surrounding text.
func extractJSON(content string) string {
	// Find the first { and last } to extract the JSON object
	start := -1
	end := -1
	braceCount := 0

	for i, r := range content {
		if r == '{' {
			if start == -1 {
				start = i
			}
			braceCount++
		} else if r == '}' {
			braceCount--
			if braceCount == 0 && start != -1 {
				end = i + 1
				break
			}
		}
	}

	if start != -1 && end != -1 && start < end {
		return content[start:end]
	}

	return ""
}

// truncateForError truncates a string for inclusion in error messages.
func truncateForError(s string) string {
	const maxLen = 200
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
