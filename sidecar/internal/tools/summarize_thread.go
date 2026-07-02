package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/mail"
	"github.com/hygur/sidecar/internal/store"
)

// SummarizeThreadTool generates structured summaries of email threads using LLM.
type SummarizeThreadTool struct {
	llm          *llm.Client
	store        *store.DB
	normalizer   *mail.ThreadNormalizer
	connectors   map[string]mail.MailConnector // for the read-only LLM tool adapter
	defaultModel string                        // chat model for the tool adapter (cfg.LMStudio.ModelDefault)
}

// NewSummarizeThreadTool creates a new SummarizeThreadTool with the given dependencies.
func NewSummarizeThreadTool(llmClient *llm.Client, db *store.DB) *SummarizeThreadTool {
	return &SummarizeThreadTool{
		llm:        llmClient,
		store:      db,
		normalizer: mail.NewThreadNormalizer(),
	}
}

// SetConnectors wires the mailboxes the LLM tool adapter uses to resolve a
// thread id before summarizing. Optional — Run() works with pre-fetched threads.
func (t *SummarizeThreadTool) SetConnectors(c map[string]mail.MailConnector) {
	t.connectors = c
}

// SetDefaultModel sets the chat model the read-only tool adapter uses — the
// summarize endpoint requires an explicit model, so the tool mirrors the main
// chat path's default (cfg.LMStudio.ModelDefault).
func (t *SummarizeThreadTool) SetDefaultModel(model string) {
	t.defaultModel = model
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
	parsed, err := t.summarize(ctx, thread, messages, model)
	if err != nil {
		return nil, err
	}

	// Create the Summary
	summary := &store.Summary{
		SummaryID:     uuid.New().String(),
		SourceRef:     "email:" + thread.ID,
		ModelUsed:     model,
		Decisions:     parsed.Decisions,
		Actions:       parsed.Actions,
		OpenQuestions: parsed.OpenQuestions,
		CreatedAt:     time.Now(),
	}

	// Save to database (Run persists; the read-only tool path below does not).
	if err := t.store.InsertSummary(ctx, summary); err != nil {
		return nil, fmt.Errorf("failed to save summary: %w", err)
	}

	return summary, nil
}

// summarize runs the read-only half: normalize → LLM → parse, with NO
// persistence. Shared by Run (which then saves) and the LLM tool adapter Execute
// (which returns the structure without writing to the DB).
func (t *SummarizeThreadTool) summarize(ctx context.Context, thread *mail.Thread, messages []mail.Message, model string) (*summaryResponse, error) {
	normalizedText, err := t.normalizer.Normalize(thread, messages)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize thread: %w", err)
	}
	// Cost/DoS guard: a pathologically long thread is truncated head+tail so both
	// the opening and the most-recent messages survive (a pure prefix cut would
	// drop the latest, usually most relevant, replies). Healthy threads are well
	// under the cap and pass through unchanged.
	normalizedText = truncateMiddle(normalizedText, maxSummaryInputRunes)
	prompt := buildSummaryPrompt(thread, normalizedText)
	resp, err := t.llm.Chat(ctx, llm.ChatRequest{
		Model: model,
		// Runs inside the chat tool-loop on behalf of the user's live turn, so it
		// counts against the chat cap like the answer it feeds (WP16a).
		Category: "chat",
		Pass:     "summarize_thread",
		Messages: []llm.Message{
			{Role: "system", Content: summarySystemPrompt},
			{Role: "user", Content: prompt},
		},
		Temperature: llm.Temp(0),
		TopP:        llm.Temp(1),
		Seed:        llm.SeedOf(42),
		MaxTokens:   700,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call LLM: %w", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return nil, fmt.Errorf("empty response from LLM")
	}
	parsed, err := parseSummaryResponse(resp.Choices[0].Message.Content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse LLM response: %w", err)
	}
	return parsed, nil
}

// --- LLM tool adapter (read-only) -------------------------------------------
// Exposes summarize_thread to the chat path so the assistant can distil a full
// thread into decisions / actions / open questions on demand. Read-only: unlike
// Run(), it does NOT persist the summary — a chat tool-call should not write to
// the user's DB as a side effect.

// Name implements tools.Tool.
func (t *SummarizeThreadTool) Name() string { return "summarize_thread" }

// Description implements tools.Tool.
func (t *SummarizeThreadTool) Description() string {
	return "Summarize one of the user's email threads into its decisions, action items and open questions."
}

// ParameterSchema implements tools.Tool.
func (t *SummarizeThreadTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"thread_id": map[string]any{
				"type":        "string",
				"description": "The email thread id (the part after \"email:\" in a source citation).",
			},
			"source": map[string]any{
				"type":        "string",
				"description": "Optional mailbox connector id (e.g. gmail, proton). Omit to search all connected mailboxes.",
			},
		},
		"required": []string{"thread_id"},
	}
}

// Execute implements tools.Tool: resolve the thread (in the named source, or any
// connected mailbox), summarize it, and return the structure WITHOUT saving.
func (t *SummarizeThreadTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var req struct {
		ThreadID string `json:"thread_id"`
		Source   string `json:"source"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if req.ThreadID == "" {
		return nil, fmt.Errorf("thread_id is required")
	}
	if len(t.connectors) == 0 {
		return nil, fmt.Errorf("no mail connectors available")
	}

	var sources []string
	if req.Source != "" {
		sources = []string{req.Source}
	} else {
		for name := range t.connectors {
			sources = append(sources, name)
		}
		sort.Strings(sources) // deterministic across runs
	}

	var lastErr error
	for _, src := range sources {
		conn, ok := t.connectors[src]
		if !ok || !conn.IsConnected() {
			continue
		}
		thread, err := conn.GetThread(ctx, req.ThreadID)
		if err != nil {
			lastErr = err // thread likely not in this mailbox — try the next
			continue
		}
		messages, err := conn.GetMessagesByThread(ctx, thread)
		if err != nil {
			lastErr = err
			continue
		}
		parsed, err := t.summarize(ctx, thread, messages, t.defaultModel) // read-only: not persisted
		if err != nil {
			return nil, err
		}
		return json.Marshal(parsed)
	}
	if lastErr != nil {
		return nil, fmt.Errorf("thread not found in any connected mailbox: %w", lastErr)
	}
	return nil, fmt.Errorf("no connected mailbox to resolve thread %q", req.ThreadID)
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

// maxSummaryInputRunes caps the normalized thread text fed to the summarizer.
// Pure cost/DoS guard: a healthy thread is far under this; only an abusive one
// is clipped. Measured in runes so multibyte content isn't cut mid-character.
const maxSummaryInputRunes = 12000

// summaryTruncationMarker is inserted between the retained head and tail of a
// truncated thread so the model (and any reader) can see the middle was elided.
const summaryTruncationMarker = " […truncated…] "

// truncateMiddle returns s unchanged when it fits within maxRunes; otherwise it
// keeps the head and tail (splitting the budget evenly) with a marker in the
// middle, so both ends of the thread survive. The result is at most maxRunes.
func truncateMiddle(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	marker := []rune(summaryTruncationMarker)
	if maxRunes <= len(marker) {
		// Degenerate budget — no room for both ends; hard-cut the head.
		return string(r[:maxRunes])
	}
	keep := maxRunes - len(marker)
	head := keep / 2
	tail := keep - head
	return string(r[:head]) + summaryTruncationMarker + string(r[len(r)-tail:])
}

// truncateForError truncates a string for inclusion in error messages.
func truncateForError(s string) string {
	const maxLen = 200
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
