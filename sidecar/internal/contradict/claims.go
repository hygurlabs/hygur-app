package contradict

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/hygur/sidecar/internal/llm"
)

// Claim is one explicit assertion an author makes in a document — the semantic
// unit W6 reconciles (decisions, commitments, statuses), complementing the
// deterministic Tier-1 conflicts (amounts/dates/IBAN/VAT) detected by Detect.
//
// Anti-hallucination invariant ("facts before reply"): every Claim carries a
// Quote that is a REAL verbatim span of its source item. ExtractClaims drops any
// claim whose quote can't be found in the text, so a fabricated claim can't enter
// the pipeline at the source. Entity/Attribute/Value drive clustering; the LLM
// reconciliation step (later) sees only validated claims, never free text.
type Claim struct {
	Entity     string `json:"entity"`      // what/whom the claim is about
	Attribute  string `json:"attribute"`   // the property/matter asserted
	Value      string `json:"value"`       // the asserted state/value
	Polarity   string `json:"polarity"`    // "affirm" | "negate"
	Quote      string `json:"quote"`       // verbatim span, gated as a real substring
	SourceID   string `json:"source_id"`   // content_id — filled by the caller, not the LLM
	AssertedAt string `json:"asserted_at"` // RFC3339 item date — filled by the caller
}

const (
	claimsMaxRunes  = 4000 // how much of the item body to send
	claimsMaxTokens = 700  // JSON array of a handful of short claims
	claimsMax       = 12   // bound the claims kept per item
)

// claimsSystemPrompt is deliberately generic and bounded: it describes the claim
// schema and the verbatim-quote requirement, with no domain enumerations or
// profile assumptions, so it generalises across any document the model sees.
const claimsSystemPrompt = `You extract the substantive claims an author makes in a document, so they can later be checked for contradictions across messages. A claim is a single assertion that could later be confirmed or contradicted: a decision, a commitment, a status, a deadline, or a key factual value about a real subject (a party, a project, an obligation, a transaction).

Return ONLY a JSON array of the few most significant such claims. Each element:
{"entity": "...", "attribute": "...", "value": "...", "polarity": "affirm|negate", "quote": "..."}
- entity: the real-world subject the claim is about (a person, organisation, project, obligation, or transaction) — not a link, button, file name, or presentational element.
- attribute: the property or matter being asserted.
- value: the asserted state or value.
- polarity: "affirm" if asserted, "negate" if denied or cancelled.
- quote: the exact span of the document that states it, copied character-for-character.

Extract only substantive, explicitly-stated assertions; never infer. Ignore presentational and boilerplate text — formatting, file metadata, footers, legal or copyright notices, and automated banners. Omit any claim you cannot quote verbatim. If there are no substantive claims, return []. Output the JSON array and nothing else. The document may be in any language.`

// ExtractClaims asks the LLM for the claims asserted in text, then keeps only
// those whose quote is a real substring of text (the anti-hallucination gate).
// The caller stamps SourceID/AssertedAt. Constrained decoding (temp 0,
// enable_thinking off) keeps it fast and stable on a small model.
func ExtractClaims(ctx context.Context, client *llm.Client, text string) ([]Claim, error) {
	body := text
	if r := []rune(body); len(r) > claimsMaxRunes {
		body = string(r[:claimsMaxRunes])
	}
	if strings.TrimSpace(body) == "" {
		return nil, nil
	}
	resp, err := client.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: claimsSystemPrompt},
			{Role: "user", Content: body},
		},
		Temperature:        0,
		MaxTokens:          claimsMaxTokens,
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
	return parseClaims(raw, text), nil
}

// parseClaims extracts the JSON array from a (possibly fenced / chatty) model
// reply and returns only well-formed claims whose quote is verbatim-present in
// sourceText. Pure + deterministic, so it carries the unit tests.
func parseClaims(raw, sourceText string) []Claim {
	arr := firstJSONArray(raw)
	if arr == "" {
		return nil
	}
	var parsed []Claim
	if err := json.Unmarshal([]byte(arr), &parsed); err != nil {
		return nil
	}
	out := make([]Claim, 0, len(parsed))
	for _, c := range parsed {
		c.Entity = strings.TrimSpace(c.Entity)
		c.Attribute = strings.TrimSpace(c.Attribute)
		c.Value = strings.TrimSpace(c.Value)
		c.Quote = strings.TrimSpace(c.Quote)
		// Need a subject, something asserted, and a verbatim quote to gate on.
		if c.Entity == "" || c.Attribute == "" || c.Quote == "" {
			continue
		}
		if !quoteInText(sourceText, c.Quote) {
			continue // anti-hallucination gate: drop fabricated quotes
		}
		if c.Polarity != "negate" {
			c.Polarity = "affirm"
		}
		out = append(out, c)
		if len(out) >= claimsMax {
			break
		}
	}
	return out
}

// firstJSONArray returns the substring from the first '[' to its matching ']'
// (the outermost array), tolerating ```json fences and surrounding prose.
func firstJSONArray(s string) string {
	start := strings.IndexByte(s, '[')
	end := strings.LastIndexByte(s, ']')
	if start < 0 || end < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

// quoteInText reports whether quote appears in text, comparing
// whitespace-collapsed, case-folded forms so trivial spacing/case differences in
// the model's copy don't defeat a genuinely-present quote — while a fabricated
// quote still fails.
func quoteInText(text, quote string) bool {
	q := normalizeWS(quote)
	if q == "" {
		return false
	}
	return strings.Contains(normalizeWS(text), q)
}

func normalizeWS(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
