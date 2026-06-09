package contradict

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hygur/sidecar/internal/llm"
)

// W6 stage 3b (REDUCE — reconciliation). Each candidate from DetectClaimConflicts
// (same entity+attribute, ≥2 divergent values, ≥2 sources) is judged by the LLM
// from ONLY its cited values + dates — never free text — into:
//   - conflict   : genuinely incompatible, nothing says one replaces the other,
//   - supersedes : a later source updates an earlier value (evolution, a timeline),
//   - none       : not a real contradiction (units/scope/rounding/not comparable).
// The prompt is strict (adversarial mindset folded in: default "none" unless the
// values are clearly comparable AND incompatible), so false positives are dropped.
// Fail-closed: an LLM/parse error yields "none" → never a fabricated conflict.

// Verdict is the LLM's judgement on a candidate conflict.
type Verdict struct {
	Kind   string `json:"kind"`   // "conflict" | "supersedes" | "none"
	Reason string `json:"reason"` // one short sentence
}

// ReconciledConflict pairs a candidate with its verdict (conflict or supersedes).
type ReconciledConflict struct {
	ClaimConflict
	Verdict Verdict `json:"verdict"`
}

const reconcileSystemPrompt = `You judge whether claims about the same subject genuinely contradict each other, for a contradiction checker. You are given an entity, an attribute, and several values asserted by different sources at different dates. Decide exactly one:
- "conflict": the sources assert genuinely incompatible values and nothing indicates one replaces another.
- "supersedes": the values differ because a later source updates an earlier one — a normal evolution over time, not a contradiction.
- "none": not a real contradiction — different units or scope, rounding, or values that are not truly comparable.

Be strict: answer "none" unless the values are clearly comparable AND incompatible. Judge ONLY from the values and dates given; never invent context.

Return ONLY a JSON object: {"kind":"conflict|supersedes|none","reason":"<one short sentence>"}. No other text.`

const reconcileMaxTokens = 200

// ReconcileClaimConflict asks the LLM to classify one candidate. Returns a "none"
// verdict on any error (fail-closed). Constrained decoding (temp 0, thinking off).
func ReconcileClaimConflict(ctx context.Context, client *llm.Client, c ClaimConflict) (Verdict, error) {
	if client == nil || len(c.Members) < 2 {
		return Verdict{Kind: "none"}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Entity: %s\nAttribute: %s\nAsserted values:\n", c.Entity, c.Attribute)
	for _, m := range c.Members {
		date := m.AssertedAt
		if date == "" {
			date = "unknown date"
		}
		fmt.Fprintf(&b, "- %q (source %s, %s)\n", m.Value, shortID(m.SourceID), date)
	}
	resp, err := client.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: reconcileSystemPrompt},
			{Role: "user", Content: b.String()},
		},
		Temperature:        0,
		MaxTokens:          reconcileMaxTokens,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	})
	if err != nil {
		return Verdict{Kind: "none"}, err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return Verdict{Kind: "none"}, nil
	}
	raw := resp.Choices[0].Message.Content
	if strings.TrimSpace(raw) == "" {
		raw = resp.Choices[0].Message.Reasoning
	}
	return parseVerdict(raw), nil
}

// Reconcile judges every candidate and returns only those that are real conflicts
// or supersedes (evolutions), each with its verdict. "none" and errors are dropped.
func Reconcile(ctx context.Context, client *llm.Client, candidates []ClaimConflict) []ReconciledConflict {
	out := make([]ReconciledConflict, 0, len(candidates))
	for _, c := range candidates {
		v, err := ReconcileClaimConflict(ctx, client, c)
		if err != nil || v.Kind == "none" || v.Kind == "" {
			continue
		}
		out = append(out, ReconciledConflict{ClaimConflict: c, Verdict: v})
	}
	return out
}

// parseVerdict extracts the JSON object from a (possibly fenced/chatty) reply and
// normalizes kind to one of conflict|supersedes|none. Unknown/garbage → "none".
func parseVerdict(raw string) Verdict {
	obj := firstJSONObject(raw)
	if obj == "" {
		return Verdict{Kind: "none"}
	}
	var v Verdict
	if err := json.Unmarshal([]byte(obj), &v); err != nil {
		return Verdict{Kind: "none"}
	}
	switch strings.ToLower(strings.TrimSpace(v.Kind)) {
	case "conflict":
		v.Kind = "conflict"
	case "supersedes":
		v.Kind = "supersedes"
	default:
		v.Kind = "none"
	}
	v.Reason = strings.TrimSpace(v.Reason)
	return v
}

// firstJSONObject returns the substring from the first '{' to the last '}'.
func firstJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

// shortID trims a content_id to its last path/colon segment for a compact prompt.
func shortID(id string) string {
	if i := strings.LastIndexAny(id, ":/"); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}
