package ingest

import (
	"context"
	"strings"
	"unicode"

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
	return "You classify emails and notes. Pick the 1 or 2 categories that best describe the " +
		"document's subject, STRICTLY from this list:\n" + strings.Join(MailCategories, ", ") +
		"\n\nReply with ONLY the chosen category names, comma-separated, copied exactly from " +
		"the list. No other text, no explanation. The document may be in any language."
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
		Temperature:        llm.Temp(0),
		TopP:               llm.Temp(1),
		Seed:               llm.SeedOf(42),
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

// matchCategories maps a model reply to canonical taxonomy labels. The reply is
// split into candidate items on list delimiters (comma, newline, …) and each item
// is matched to a label by normalized equality or the label's first word. It does
// NOT grep label words out of free text — so a prose reply, or a negation like
// "not invoicing or banking", yields no spurious tags (the old behaviour, which
// produced systematically wrong tags from chatty/negating models). Output
// preserves taxonomy order and is de-duplicated.
func matchCategories(raw string) []string {
	items := splitCategoryItems(raw)
	if len(items) == 0 {
		return nil
	}
	var out []string
	for _, cat := range MailCategories {
		lk := normCat(cat)
		fw := firstWord(lk)
		for _, it := range items {
			if it == lk || it == fw {
				out = append(out, cat)
				break
			}
		}
	}
	return out
}

// splitCategoryItems splits a model reply on list delimiters into normalized
// candidate labels. Free-form prose collapses into items that won't equal any
// label, so nothing matches — the safe failure mode.
func splitCategoryItems(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ';', '\n', '\r', '\t', '|', '/', '•', '·':
			return true
		}
		return false
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if k := normCat(p); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// normCat lowercases and reduces a string to space-separated alphanumeric words,
// dropping the connector "and" so "Banking & Finance" and "banking and finance"
// both key to "banking finance".
func normCat(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	fields := strings.Fields(b.String())
	kept := fields[:0]
	for _, f := range fields {
		if f != "and" {
			kept = append(kept, f)
		}
	}
	return strings.Join(kept, " ")
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
