package ingest

import (
	"context"
	"strings"

	"github.com/hygur/sidecar/internal/llm"
)

// MailCategories is the closed taxonomy mail is classified into. Kept small and
// stable so the auto-tags group meaningfully (≈20 buckets) instead of producing
// one-off labels. Edit this list to retune the taxonomy.
var MailCategories = []string{
	"Invoicing",
	"Banking & Finance",
	"Insurance",
	"Energy & Utilities",
	"Telecom",
	"Tax & VAT",
	"Legal & Contracts",
	"HR & Payroll",
	"Accounting",
	"Real Estate",
	"Development & IT",
	"Marketing & Sales",
	"Purchases & Orders",
	"Transport & Mobility",
	"Health",
	"Administrative",
	"Notifications & Accounts",
	"Personal",
	"Travel",
	"Subscriptions",
}

const (
	mailCatMaxRunes  = 4000 // how much of the mail body to send
	mailCatMaxTokens = 64   // the answer is a short comma-separated list
	mailCatMax       = 2    // keep at most N categories per mail
)

func mailCatSystemPrompt() string {
	return "You classify emails. Pick the 1 or 2 categories that best describe the email's " +
		"subject, STRICTLY from this list:\n" + strings.Join(MailCategories, ", ") +
		"\n\nReply with ONLY the chosen category names, comma-separated, copied exactly from " +
		"the list. No other text, no explanation. The email may be in any language."
}

// classifyMail asks the LLM to bucket a mail into MailCategories. The output is a
// short comma-separated list (constrained → fast and reliable even on a small
// model). Returns the canonical labels matched, capped at mailCatMax.
func classifyMail(ctx context.Context, client *llm.Client, text string) ([]string, error) {
	body := text
	if r := []rune(body); len(r) > mailCatMaxRunes {
		body = string(r[:mailCatMaxRunes])
	}
	if strings.TrimSpace(body) == "" {
		return nil, nil
	}
	resp, err := client.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: mailCatSystemPrompt()},
			{Role: "user", Content: body},
		},
		Temperature:        0,
		MaxTokens:          mailCatMaxTokens,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return nil, nil
	}
	raw := resp.Choices[0].Message.Content
	if strings.TrimSpace(raw) == "" {
		raw = resp.Choices[0].Message.Reasoning
	}
	cats := matchCategories(raw)
	if len(cats) > mailCatMax {
		cats = cats[:mailCatMax]
	}
	return cats, nil
}

// matchCategories maps free-form model output to canonical taxonomy labels,
// case-insensitively, preserving taxonomy order and dropping anything unknown.
// It matches the full label or — for multi-word labels — the first word, so a
// model that replies "Banque" still resolves to "Banque & Finance".
func matchCategories(raw string) []string {
	low := strings.ToLower(raw)
	var out []string
	for _, cat := range MailCategories {
		lc := strings.ToLower(cat)
		if strings.Contains(low, lc) || containsWord(low, firstWord(lc)) {
			out = append(out, cat)
		}
	}
	return out
}

// firstWord returns the first token of a label, splitting on spaces and '&'.
func firstWord(s string) string {
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '&' }) {
		if w != "" {
			return w
		}
	}
	return s
}

// containsWord reports whether word appears in text as a whole token (bounded by
// non-letter characters), so "achats" doesn't match inside "rachats".
func containsWord(text, word string) bool {
	if word == "" {
		return false
	}
	for {
		i := strings.Index(text, word)
		if i < 0 {
			return false
		}
		before := i == 0 || !isLetter(rune(text[i-1]))
		afterIdx := i + len(word)
		after := afterIdx >= len(text) || !isLetter(rune(text[afterIdx]))
		if before && after {
			return true
		}
		text = text[i+len(word):]
	}
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// categoriesFromMetadata reads cached categories, tolerating the []string and
// (post-JSON-round-trip) []any shapes.
func categoriesFromMetadata(m map[string]any) []string {
	raw, ok := m["mail_categories"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
