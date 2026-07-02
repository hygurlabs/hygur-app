package retrieval

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/llm"
)

const (
	rewriteMaxHistoryMsgs = 4    // keep last N messages from history
	rewriteMaxMsgChars    = 500  // truncate each history message to this length
	rewriteMaxTokens      = 2048 // reasoning models need headroom for <think> tokens
	rewriteTimeout        = 25 * time.Second
)

// /no_think disables chain-of-thought on Qwen / Nemotron-style reasoning models
// served through LM Studio so the rewritten query arrives in the `content` field
// instead of being consumed by an unbounded `reasoning` block. Models that don't
// recognise the directive treat it as plain text.
var rewriteSystemPrompt = `/no_think
You are a search assistant. Output ONLY a short search query. No explanation.`

// RewriteStandaloneQuery rewrites a follow-up user message into a self-contained
// search query using the conversation history. Returns (latest, nil) with no LLM
// call when history is empty. Returns (latest, err) on failure so the caller
// can fall back gracefully.
func RewriteStandaloneQuery(
	ctx context.Context,
	client *llm.Client,
	history []llm.Message,
	latest string,
) (string, error) {
	if len(history) == 0 {
		return latest, nil
	}

	// Keep only the last rewriteMaxHistoryMsgs messages.
	start := 0
	if len(history) > rewriteMaxHistoryMsgs {
		start = len(history) - rewriteMaxHistoryMsgs
	}

	var histLines strings.Builder
	for _, msg := range history[start:] {
		role := "User"
		if msg.Role == "assistant" {
			role = "Assistant"
		}
		content := msg.Content
		if len(content) > rewriteMaxMsgChars {
			content = content[:rewriteMaxMsgChars] + "…"
		}
		histLines.WriteString(role)
		histLines.WriteString(": ")
		histLines.WriteString(content)
		histLines.WriteString("\n")
	}

	userPrompt := fmt.Sprintf(`Given a conversation and a follow-up question, write a keyword search query that captures the topic.
The query must include the subject from the conversation context. Output ONLY the query words, nothing else.

Example 1:
Conversation:
User: what is the VAT I should send to?
Assistant: IBAN BE68 5390 0754 7034 for TVA EXMPL Q1 2026
Follow-up: "Okay, and can you show me the account number?"
Query: TVA EXMPL 1er trimestre 2026 IBAN numéro compte paiement

Example 2:
Conversation:
User: show me my last Stripe invoice
Assistant: Invoice INV-2024-001 for 120€
Follow-up: "and the due date?"
Query: Stripe invoice due date 2024

Now do this:
Conversation:
%sFollow-up: %q
Query:`, histLines.String(), latest)

	rewriteCtx, cancel := context.WithTimeout(ctx, rewriteTimeout)
	defer cancel()

	resp, err := client.Chat(rewriteCtx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: rewriteSystemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: llm.Temp(0),
		TopP:        llm.Temp(1),
		Seed:        llm.SeedOf(42),
		MaxTokens:   rewriteMaxTokens,
	})
	if err != nil {
		return latest, fmt.Errorf("rewrite LLM call failed: %w", err)
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return latest, fmt.Errorf("rewrite returned empty response")
	}

	// Reasoning-capable backends (Nemotron-omni, some Qwen builds) put the
	// answer in `message.reasoning` when they treat the whole turn as a thinking
	// block. Fall back to reasoning when content is empty so the rewrite still
	// works on those models.
	rewritten := strings.TrimSpace(resp.Choices[0].Message.Content)
	if rewritten == "" {
		rewritten = strings.TrimSpace(resp.Choices[0].Message.Reasoning)
	}
	// Some reasoning models leak a stray `</think>` marker even with /no_think.
	if i := strings.LastIndex(rewritten, "</think>"); i >= 0 {
		rewritten = strings.TrimSpace(rewritten[i+len("</think>"):])
	}
	if rewritten == "" {
		return latest, fmt.Errorf("rewrite returned blank string")
	}

	triggered := rewritten != latest
	slog.InfoContext(ctx, "rag.rewrite",
		"original", latest,
		"rewritten", rewritten,
		"triggered", triggered,
	)
	return rewritten, nil
}
