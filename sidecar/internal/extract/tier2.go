package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/llm"
)

// Tier2Version is the schema marker written to metadata as `extracted_v2_at`.
// Bump it whenever the prompt/schema changes meaningfully so the backfill CLI
// knows to re-extract older documents.
const Tier2Version = "v1"

const (
	// Larger budget than a strict JSON-only model would need: small models
	// (e.g. minicpm5) ignore /no_think and spend tokens on a <think> block
	// before the JSON, so we leave room for both. parseTier2Response then digs
	// the JSON object out of whatever wraps it.
	tier2MaxTokens = 2048
	tier2Timeout   = 60 * time.Second
	// tier2BodyMaxRunes caps how much of the document is sent to the LLM. We
	// want full coverage for short docs but bound the prompt for long ones —
	// the goal is named-entity recognition, not summarization, so the head of
	// the document is usually enough.
	tier2BodyMaxRunes = 6000
)

// EventDate pairs a normalized date (YYYY-MM-DD when known) with a short
// human-readable context describing what happens on that date. Tier 1
// extracts due dates as raw strings; Tier 2 attaches semantics.
type EventDate struct {
	Date    string `json:"date"`
	Context string `json:"context,omitempty"`
}

// Tier2Entities is the structured output of the LLM extractor.
//
// All slices are normalized: trimmed, deduplicated case-insensitively, and
// limited to a sane upper bound to keep the metadata column small. Empty
// slices are omitted from JSON marshaling so downstream code can rely on
// presence as a signal.
type Tier2Entities struct {
	Persons       []string    `json:"persons,omitempty"`
	Organizations []string    `json:"organizations,omitempty"`
	EventDates    []EventDate `json:"event_dates,omitempty"`
	Projects      []string    `json:"projects,omitempty"`
	Topics        []string    `json:"topics,omitempty"`
}

// Count returns the total number of extracted entities across all categories.
func (t Tier2Entities) Count() int {
	return len(t.Persons) + len(t.Organizations) + len(t.EventDates) +
		len(t.Projects) + len(t.Topics)
}

// /no_think disables chain-of-thought on Qwen / Nemotron-style reasoning models
// served through LM Studio so the JSON answer arrives in seconds instead of
// being truncated at the max_tokens cap. Models that do not honour the
// directive treat it as plain text and ignore it.
const tier2SystemPrompt = `/no_think
You are an entity extractor. Read the document and return the named entities it contains.

Categories:
- persons: full names of individuals mentioned (people, not roles or pronouns).
- organizations: companies, public institutions, administrations, suppliers.
- event_dates: dates that anchor a concrete event (deadline, meeting, payment, contract). For each, output {"date":"YYYY-MM-DD","context":"<short reason>"}. Skip vague references like "yesterday" without a clear absolute date.
- projects: internal project or initiative names (capitalized, often a single word).
- topics: 1-3 normalized lowercase topic labels describing the document's subject (e.g. "tva", "facturation", "rh", "juridique", "marketing"). Choose from a small vocabulary; reuse common labels.

Rules:
- Only extract entities that genuinely appear in the document; do NOT invent.
- Normalize names: trim, fix obvious casing, deduplicate.
- If a category has no instance, return an empty array.
- Return at most 20 persons, 20 organizations, 20 event_dates, 10 projects, 5 topics.

Respond ONLY with valid JSON, no commentary, no markdown fences. Schema:
{"persons":["..."],"organizations":["..."],"event_dates":[{"date":"YYYY-MM-DD","context":"..."}],"projects":["..."],"topics":["..."]}`

// ExtractTier2 runs the LLM-backed NER pass on `text`. Fail-soft: any LLM,
// timeout, or parse error returns an empty Tier2Entities and a non-nil error
// so the caller can log it without aborting the ingestion pipeline.
func ExtractTier2(ctx context.Context, client *llm.Client, text string) (Tier2Entities, error) {
	if client == nil {
		return Tier2Entities{}, fmt.Errorf("tier2: nil llm client")
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return Tier2Entities{}, nil
	}

	tctx, cancel := context.WithTimeout(ctx, tier2Timeout)
	defer cancel()

	body := truncateRunes(trimmed, tier2BodyMaxRunes)
	userPrompt := "Document:\n" + body + "\n\nReturn the JSON now."

	resp, err := client.Chat(tctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: tier2SystemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0,
		MaxTokens:   tier2MaxTokens,
		// Hard-disable reasoning at the chat-template level. The `/no_think`
		// prefix above is ignored by some models (notably minicpm5), which then
		// spend the whole token budget on an unterminated <think> block and never
		// emit the JSON. enable_thinking=false stops that at the backend; it is
		// silently ignored by templates that don't support it.
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	})
	if err != nil {
		return Tier2Entities{}, fmt.Errorf("tier2: chat failed: %w", err)
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return Tier2Entities{}, fmt.Errorf("tier2: empty response")
	}

	// Reasoning-capable backends (Nemotron-omni, some Qwen builds) put the
	// answer in `message.reasoning` and leave `message.content` empty when the
	// model treats its whole turn as a thinking block. Fall back to reasoning
	// when content is missing.
	raw := resp.Choices[0].Message.Content
	if strings.TrimSpace(raw) == "" {
		raw = resp.Choices[0].Message.Reasoning
	}

	out, err := parseTier2Response(raw)
	if err != nil {
		return Tier2Entities{}, fmt.Errorf("tier2: parse: %w", err)
	}
	return normalizeTier2(out), nil
}

func parseTier2Response(raw string) (Tier2Entities, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	// Strip a reasoning block. Some models (notably minicpm5) ignore /no_think
	// and wrap output in <think>…</think>; keep what follows the LAST close.
	if i := strings.LastIndex(raw, "</think>"); i >= 0 {
		raw = strings.TrimSpace(raw[i+len("</think>"):])
	}

	// Small models often emit the JSON wrapped in prose or an unterminated
	// <think> (no closing tag within the token budget). Dig out the outermost
	// {...} object so we still capture entities instead of failing the parse.
	trimmed := strings.TrimLeft(raw, " \t\r\n")
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		if obj := extractFirstJSONObject(raw); obj != "" {
			raw = obj
		}
	}

	// Some models wrap the JSON in a single-element array; accept that.
	trimmed = strings.TrimLeft(raw, " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		var arr []Tier2Entities
		if err := json.Unmarshal([]byte(raw), &arr); err != nil {
			return Tier2Entities{}, fmt.Errorf("invalid json array: %w (raw=%q)", err, truncateRunes(raw, 200))
		}
		if len(arr) == 0 {
			return Tier2Entities{}, nil
		}
		return arr[0], nil
	}

	var out Tier2Entities
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		// Fail-soft: try repairing the most common Nemotron-omni stumbles
		// before giving up. We only attempt structural fixes, never invent data.
		if repaired, ok := repairTier2JSON(raw); ok {
			if err2 := json.Unmarshal([]byte(repaired), &out); err2 == nil {
				return out, nil
			}
		}
		return Tier2Entities{}, fmt.Errorf("invalid json: %w (raw=%q)", err, truncateRunes(raw, 200))
	}
	return out, nil
}

// extractFirstJSONObject returns the substring from the first '{' to its
// matching '}' (brace-balanced, string-aware), or "" if none is found. Used to
// recover the JSON object from small-model output that wraps it in prose or an
// unterminated <think> block.
func extractFirstJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// repairTier2JSON patches the two most frequent malformations observed in
// Nemotron-omni Tier 2 output:
//  1. Spurious closing brace after event_dates: `]},"projects":` → `],"projects":`
//  2. Missing comma between event_dates and the next key: `]"projects":` → `],"projects":`
//
// Returns the (possibly modified) string and a bool indicating whether any
// repair was applied. Conservative — only touches event_dates → next-key joints.
func repairTier2JSON(raw string) (string, bool) {
	changed := false
	for _, key := range []string{"projects", "topics", "persons", "organizations"} {
		bad1 := `]},"` + key + `":`
		good := `],"` + key + `":`
		if strings.Contains(raw, bad1) {
			raw = strings.ReplaceAll(raw, bad1, good)
			changed = true
		}
		bad2 := `]"` + key + `":`
		if strings.Contains(raw, bad2) {
			raw = strings.ReplaceAll(raw, bad2, good)
			changed = true
		}
	}
	return raw, changed
}

// normalizeTier2 trims, dedupes (case-insensitive on the simple slices), and
// caps each list. Topics are additionally lowercased.
func normalizeTier2(t Tier2Entities) Tier2Entities {
	t.Persons = dedupStringsCI(dropPlaceholders(t.Persons), 20, false)
	t.Organizations = dedupStringsCI(dropPlaceholders(t.Organizations), 20, false)
	t.Projects = dedupStringsCI(dropPlaceholders(t.Projects), 10, false)
	t.Topics = dedupStringsCI(dropPlaceholders(t.Topics), 5, true)
	t.EventDates = dedupEventDates(dropPlaceholderDates(t.EventDates), 20)
	return t
}

// isSchemaPlaceholder reports whether s is a literal example token from the
// Tier 2 schema that a weak model may echo verbatim instead of real values
// ("...", "YYYY-MM-DD", "<short reason>", etc.). Such echoes are dropped so a
// 1B model that fails to follow the instruction pollutes nothing.
func isSchemaPlaceholder(s string) bool {
	t := strings.ToLower(strings.TrimSpace(s))
	switch t {
	case "", "...", "…", "..", "yyyy-mm-dd", "<short reason>", "short reason", "string", "name":
		return true
	}
	return strings.HasPrefix(t, "<") && strings.HasSuffix(t, ">")
}

func dropPlaceholders(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !isSchemaPlaceholder(s) {
			out = append(out, s)
		}
	}
	return out
}

func dropPlaceholderDates(in []EventDate) []EventDate {
	out := make([]EventDate, 0, len(in))
	for _, e := range in {
		if e.Date == "" || isSchemaPlaceholder(e.Date) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func dedupStringsCI(in []string, maxLen int, lower bool) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if lower {
			s = strings.ToLower(s)
		}
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
		if len(out) >= maxLen {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func dedupEventDates(in []EventDate, maxLen int) []EventDate {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]EventDate, 0, len(in))
	for _, e := range in {
		e.Date = strings.TrimSpace(e.Date)
		e.Context = strings.TrimSpace(e.Context)
		if e.Date == "" {
			continue
		}
		key := e.Date + "|" + strings.ToLower(e.Context)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, e)
		if len(out) >= maxLen {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// MergeTier2IntoMetadata writes a Tier2Entities into the metadata map under
// the conventional `extracted_*` keys consumed by retrieval/entity_search.
// Empty fields are skipped so callers can rely on key presence as a signal.
// Also writes `extracted_v2_at` (RFC3339) and `extracted_v2_version` so the
// backfill CLI can detect which docs need re-extraction.
func MergeTier2IntoMetadata(metadata map[string]any, tier2 Tier2Entities) {
	if len(tier2.Persons) > 0 {
		metadata["extracted_persons"] = tier2.Persons
	}
	if len(tier2.Organizations) > 0 {
		metadata["extracted_orgs"] = tier2.Organizations
	}
	if len(tier2.EventDates) > 0 {
		serialized := make([]map[string]any, len(tier2.EventDates))
		for i, e := range tier2.EventDates {
			serialized[i] = map[string]any{"date": e.Date, "context": e.Context}
		}
		metadata["extracted_event_dates"] = serialized
	}
	if len(tier2.Projects) > 0 {
		metadata["extracted_projects"] = tier2.Projects
	}
	if len(tier2.Topics) > 0 {
		metadata["extracted_topics"] = tier2.Topics
	}
	metadata["extracted_v2_at"] = time.Now().UTC().Format(time.RFC3339)
	metadata["extracted_v2_version"] = Tier2Version
}

// truncateRunes safely truncates a string to at most n runes, appending
// nothing extra. Used both to bound prompt size and to truncate raw responses
// for error messages.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}
