// Package tools provides AI-powered tools for processing and analyzing content.
package tools

import (
	"fmt"
	"strings"

	"github.com/hygur/sidecar/internal/mail"
)

// summarySystemPrompt is the system prompt for email thread summarization.
const summarySystemPrompt = `You are an assistant that analyzes email conversations.
Extract the key information factually and concisely.

Reply ONLY in valid JSON with this exact format:
{
  "decisions": ["decisions made in the conversation"],
  "actions": ["actions to take or follow up on"],
  "open_questions": ["unresolved questions or points to clarify"]
}

Rules:
- Be factual, do not speculate
- Each item must be a short, clear sentence
- If a category has no item, use an empty array []
- At most 5 items per category`

// maxPromptTextLength is the maximum length of thread content in the prompt.
const maxPromptTextLength = 10000

// buildSummaryPrompt constructs the user prompt for summarizing an email thread.
func buildSummaryPrompt(thread *mail.Thread, normalizedText string) string {
	// Limit text to avoid context overflow
	text := normalizedText
	if len(text) > maxPromptTextLength {
		text = text[:maxPromptTextLength] + "..."
	}

	// Format participants
	participants := strings.Join(thread.Participants, ", ")
	if participants == "" {
		participants = "(no participant)"
	}

	// Format date range
	dateStart := thread.DateRange[0].Format("2006-01-02")
	dateEnd := thread.DateRange[1].Format("2006-01-02")

	return fmt.Sprintf(`Analyze this email conversation:

Subject: %s
Participants: %s
Period: %s to %s
Message count: %d

--- CONTENT ---
%s
--- END ---

Extract the decisions, actions and open questions as JSON.`,
		thread.Subject,
		participants,
		dateStart,
		dateEnd,
		thread.MessageCount,
		text)
}
