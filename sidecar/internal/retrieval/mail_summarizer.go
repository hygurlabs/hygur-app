package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

// summarizerMaxOneLiner is the hard cap we advertise in the prompt. Slightly
// above the macOS notification body width so the model has room to breathe
// without producing wrapped lines.
const summarizerMaxOneLiner = 110

// summarizerCacheTTL is short on purpose: a mail's metadata never mutates
// after ingest, so the only reason to re-summarise is a code/prompt change.
// 24 h gives us a daily refresh during development without ballooning RAM.
const summarizerCacheTTL = 24 * time.Hour

// summarizerSystemPrompt deliberately constrains the output: a single line,
// emoji + key fact, no commentary. /no_think disables the reasoning trace on
// Qwen-style models so the body lands in `content`.
const summarizerSystemPrompt = `/no_think
Tu résumes un email en UNE seule ligne courte (≤ 110 caractères).
Format: "<emoji> <quoi> : <détail clé>".
Tu n'inventes rien. Si une info manque, ne la cite pas.
Sortie: la ligne, rien d'autre. Pas de markdown, pas de prose.`

// MailSummarizer turns a freshly-indexed priority email into a one-liner
// suitable for a notification body or activity row. Strategy:
//  1. If we have org + amount + due_date + a typed topic → render from a
//     deterministic template (zero LLM cost, zero latency).
//  2. Else, ask the LLM with a strict prompt (60-token budget, T=0).
//  3. On any LLM error → fall back to the email subject (degraded mode).
//
// Results are cached by content_id so a second sync cycle that re-emits the
// same priority mail (e.g. label-change race) doesn't re-bill the LLM.
type MailSummarizer struct {
	llm   *llm.Client
	cache *summaryCache
}

// NewMailSummarizer constructs a summarizer. llm may be nil — in that case
// the templated path still works and unsupported mails fall back to subject.
func NewMailSummarizer(client *llm.Client) *MailSummarizer {
	return &MailSummarizer{
		llm:   client,
		cache: newSummaryCache(),
	}
}

// SummarizeMailOneLiner returns a single line describing the email. Never
// returns the empty string — falls back to the subject when nothing else
// works. The error is non-nil only when the input itself is unusable
// (missing item).
func (s *MailSummarizer) SummarizeMailOneLiner(ctx context.Context, item *store.KnowledgeItem) (string, error) {
	if item == nil {
		return "", fmt.Errorf("nil knowledge item")
	}

	if cached, ok := s.cache.get(item.ContentID); ok {
		return cached, nil
	}

	// Deterministic templated path — exploits Phase 4 entity extraction.
	if line, ok := s.renderFromEntities(item); ok {
		s.cache.set(item.ContentID, line)
		return line, nil
	}

	// LLM fallback. Budget is intentionally tight so a stuck call never
	// blocks a sync cycle for more than a couple of seconds.
	if s.llm != nil {
		if line, err := s.summarizeViaLLM(ctx, item); err == nil && line != "" {
			s.cache.set(item.ContentID, line)
			return line, nil
		}
	}

	// Degraded mode: subject is always available on a real email.
	subject := strings.TrimSpace(item.Title)
	if subject == "" {
		subject = "(sans objet)"
	}
	if len(subject) > summarizerMaxOneLiner {
		subject = subject[:summarizerMaxOneLiner-1] + "…"
	}
	return "📧 " + subject, nil
}

// notificationJudgeSystemPrompt asks the model to BOTH decide relevance and
// write the one-liner in a single call. Broadens importance beyond the
// accounting-keyword rule: the LLM flags any genuinely actionable/time-
// sensitive mail and vetoes informational noise.
const notificationJudgeSystemPrompt = `/no_think
You decide whether an email warrants an immediate notification to the user, then summarize it in one line.

NOTIFY (notify=true) only if the email requires an ACTION or is time-sensitive/important: invoice or payment due, deadline (tax, administrative, legal), a direct request needing a reply, an appointment, an official document, an urgent problem.
DO NOT NOTIFY (notify=false): newsletters, promotions, automatic notifications, read receipts, confirmations with no action, purely informational emails.

Reply ONLY in JSON, no surrounding text:
{"notify": true|false, "line": "<emoji> <what>: <key detail>"}
The line is at most 110 characters. Invent nothing.`

// SummarizeForNotification decides whether a freshly-indexed priority candidate
// is worth a notification and returns its one-liner. The recency gate already
// ran upstream; this is the relevance judgment:
//   - Templated entities (amount / due-date / org) → clearly actionable → notify.
//   - Else the LLM judges relevance AND writes the line in one call.
//   - No LLM or unparseable response → fall back to notifying (the upstream
//     gate already required recency + an actionable signal), subject as line.
func (s *MailSummarizer) SummarizeForNotification(ctx context.Context, item *store.KnowledgeItem) (string, bool) {
	if item == nil {
		return "", false
	}
	if line, ok := s.renderFromEntities(item); ok {
		return line, true
	}
	if s.llm != nil {
		if line, notify, err := s.judgeViaLLM(ctx, item); err == nil && line != "" {
			return line, notify
		}
	}
	subject := strings.TrimSpace(item.Title)
	if subject == "" {
		subject = "(sans objet)"
	}
	return clip("📧 " + subject), true
}

// judgeViaLLM asks the model for {notify, line} in one call.
func (s *MailSummarizer) judgeViaLLM(ctx context.Context, item *store.KnowledgeItem) (string, bool, error) {
	llmCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	body := strings.TrimSpace(item.NormalizedText)
	if len(body) > 800 {
		body = body[:800] + "…"
	}
	from, _ := item.Metadata["mail_from"].(string)

	var b strings.Builder
	b.WriteString("Sujet: ")
	b.WriteString(item.Title)
	if from != "" {
		b.WriteString("\nDe: ")
		b.WriteString(from)
	}
	b.WriteString("\n\nCorps:\n")
	b.WriteString(body)

	resp, err := s.llm.Chat(llmCtx, llm.ChatRequest{
		Category: "background",
		Pass:     "mail_notify_judge",
		Messages: []llm.Message{
			{Role: "system", Content: notificationJudgeSystemPrompt},
			{Role: "user", Content: b.String()},
		},
		Stream:             false,
		Temperature:        llm.Temp(0),
		TopP:               llm.Temp(1),
		Seed:               llm.SeedOf(42),
		MaxTokens:          120,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	})
	if err != nil {
		return "", false, err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return "", false, fmt.Errorf("empty llm response")
	}
	notify, line, ok := parseJudgeJSON(resp.Choices[0].Message.Content)
	if !ok {
		return "", false, fmt.Errorf("could not parse judge response")
	}
	return clip(line), notify, nil
}

// parseJudgeJSON extracts the {notify, line} object from a model reply,
// tolerating surrounding prose or code fences.
func parseJudgeJSON(raw string) (notify bool, line string, ok bool) {
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end <= start {
		return false, "", false
	}
	var parsed struct {
		Notify bool   `json:"notify"`
		Line   string `json:"line"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &parsed); err != nil {
		return false, "", false
	}
	return parsed.Notify, cleanLLMLine(parsed.Line), true
}

// renderFromEntities returns a templated one-liner when the metadata has
// enough structured fields to describe the email without an LLM call. The
// templates are typed by `extracted_topics` so a "facture" mail is rendered
// as a payment hint, an "rdv" as a calendar item, etc.
func (s *MailSummarizer) renderFromEntities(item *store.KnowledgeItem) (string, bool) {
	amount := firstString(item.Metadata, "extracted_amounts")
	dueDate := firstString(item.Metadata, "extracted_due_dates")
	org := firstString(item.Metadata, "extracted_orgs")
	topics := lowerStrings(item.Metadata, "extracted_topics")

	hasInvoice := containsAny(topics, "facture", "invoice", "billing", "paiement")
	hasMeeting := containsAny(topics, "rdv", "rendez-vous", "meeting", "appointment")

	switch {
	case amount != "" && dueDate != "" && org != "":
		return clip(fmt.Sprintf("💰 Facture %s : %s à payer avant le %s", org, amount, dueDate)), true
	case amount != "" && dueDate != "":
		return clip(fmt.Sprintf("💰 %s à payer avant le %s", amount, dueDate)), true
	case hasInvoice && amount != "" && org != "":
		return clip(fmt.Sprintf("💰 Facture %s : %s", org, amount)), true
	case hasMeeting && dueDate != "" && org != "":
		return clip(fmt.Sprintf("📅 RDV %s le %s", org, dueDate)), true
	case dueDate != "" && org != "":
		return clip(fmt.Sprintf("⚠️ %s : échéance %s", org, dueDate)), true
	}
	return "", false
}

// summarizeViaLLM asks the model for a one-liner. Returns the cleaned line
// or an error so the caller can fall back. Budget is small on purpose.
func (s *MailSummarizer) summarizeViaLLM(ctx context.Context, item *store.KnowledgeItem) (string, error) {
	llmCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	body := strings.TrimSpace(item.NormalizedText)
	if len(body) > 800 {
		body = body[:800] + "…"
	}
	from, _ := item.Metadata["mail_from"].(string)

	var b strings.Builder
	b.WriteString("Sujet: ")
	b.WriteString(item.Title)
	if from != "" {
		b.WriteString("\nDe: ")
		b.WriteString(from)
	}
	b.WriteString("\n\nCorps:\n")
	b.WriteString(body)

	resp, err := s.llm.Chat(llmCtx, llm.ChatRequest{
		Category: "background",
		Pass:     "mail_summary",
		Messages: []llm.Message{
			{Role: "system", Content: summarizerSystemPrompt},
			{Role: "user", Content: b.String()},
		},
		Stream:      false,
		Temperature: llm.Temp(0),
		TopP:        llm.Temp(1),
		Seed:        llm.SeedOf(42),
		MaxTokens:   60,
	})
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return "", fmt.Errorf("empty llm response")
	}
	line := cleanLLMLine(resp.Choices[0].Message.Content)
	if line == "" {
		return "", fmt.Errorf("llm returned empty content")
	}
	return clip(line), nil
}

// cleanLLMLine strips quotes, code fences, and extra whitespace. Some local
// models still wrap output in `"..."` despite the instruction.
func cleanLLMLine(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// clip ensures the line never exceeds the max length we advertise.
func clip(s string) string {
	if len(s) <= summarizerMaxOneLiner {
		return s
	}
	return s[:summarizerMaxOneLiner-1] + "…"
}

// firstString reads the first element of a metadata array under `key`,
// tolerating both []string and []any after JSON round-trip.
func firstString(meta map[string]any, key string) string {
	switch v := meta[key].(type) {
	case []string:
		if len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return strings.TrimSpace(s)
			}
		}
	case string:
		return strings.TrimSpace(v)
	}
	return ""
}

// lowerStrings returns all strings under `key` lower-cased and trimmed.
func lowerStrings(meta map[string]any, key string) []string {
	var out []string
	switch v := meta[key].(type) {
	case []string:
		for _, s := range v {
			if t := strings.ToLower(strings.TrimSpace(s)); t != "" {
				out = append(out, t)
			}
		}
	case []any:
		for _, raw := range v {
			if s, ok := raw.(string); ok {
				if t := strings.ToLower(strings.TrimSpace(s)); t != "" {
					out = append(out, t)
				}
			}
		}
	}
	return out
}

func containsAny(haystack []string, needles ...string) bool {
	for _, h := range haystack {
		for _, n := range needles {
			if strings.Contains(h, n) {
				return true
			}
		}
	}
	return false
}

// summaryCache is a tiny TTL map keyed by content_id. We could swap in
// hashicorp/golang-lru if it ever grows, but for one-liners the cardinality
// is small (one per priority mail per 24 h) so a plain map is plenty.
type summaryCache struct {
	mu      sync.Mutex
	entries map[string]summaryCacheEntry
}

type summaryCacheEntry struct {
	value     string
	expiresAt time.Time
}

func newSummaryCache() *summaryCache {
	return &summaryCache{entries: make(map[string]summaryCacheEntry)}
}

func (c *summaryCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return "", false
	}
	if time.Now().After(e.expiresAt) {
		delete(c.entries, key)
		return "", false
	}
	return e.value, true
}

func (c *summaryCache) set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = summaryCacheEntry{
		value:     value,
		expiresAt: time.Now().Add(summarizerCacheTTL),
	}
}
