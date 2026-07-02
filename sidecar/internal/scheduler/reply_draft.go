package scheduler

import (
	"context"
	"fmt"
	"strings"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/prose"
	"github.com/hygur/sidecar/internal/store"
)

// replyDraftPromptBase: grounded, on-demand. Reply in the email's own language;
// invent nothing; leave a placeholder when a detail is missing.
const replyDraftPromptBase = `You are a personal assistant drafting a reply to an email, on the user's behalf.

From ONLY the email below, write a concise, professional reply draft in the SAME language as the email. Address its actual content (questions, requests, deadlines). Use only what the email says; never invent a fact, name, date, amount or commitment. When a needed detail is missing, leave a clear placeholder like [...].

Output only the reply body — no subject line, no preamble like "Here is". Keep internal reasoning minimal.`

// replyDraftSystemPrompt = base + the shared prose-voice block.
var replyDraftSystemPrompt = llm.WithVoice(replyDraftPromptBase)

// DraftReply produces a short, grounded reply draft for a mail item. On-demand,
// not cached (the user wants a fresh take and may regenerate). Returns "" when
// the LLM isn't configured.
func (d *DailyBrief) DraftReply(ctx context.Context, item *store.KnowledgeItem) (string, error) {
	if d == nil || d.llm == nil || item == nil {
		return "", nil
	}
	var sb strings.Builder
	if from := senderOf(item); from != "" {
		fmt.Fprintf(&sb, "From: %s\n", from)
	}
	fmt.Fprintf(&sb, "Subject: %s\n\n%s", strings.TrimSpace(item.Title), snippet(item.NormalizedText, 2000))

	resp, err := d.llm.Chat(ctx, llm.ChatRequest{
		Category: "background",
		Pass:     "reply_draft",
		Messages: []llm.Message{
			{Role: "system", Content: replyDraftSystemPrompt},
			{Role: "user", Content: sb.String()},
		},
		Temperature:        llm.Temp(0.4),
		MaxTokens:          600,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	})
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return "", nil
	}
	out := strings.TrimSpace(stripReasoningTags(resp.Choices[0].Message.Content))
	if out == "" {
		out = strings.TrimSpace(stripReasoningTags(resp.Choices[0].Message.Reasoning))
	}
	// Couche B: deterministic cleanup (auto-detect language — the draft mirrors
	// the mail's, which may be French or English).
	return prose.Tidy(out, ""), nil
}
