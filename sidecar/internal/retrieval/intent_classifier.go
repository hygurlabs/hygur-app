package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/llm"
)

// Query intent categories produced by the LLM classifier.
const (
	IntentFactualEntity  = "factual_entity"
	IntentTopic          = "topic"
	IntentTemporal       = "temporal"
	IntentConversational = "conversational"
)

const (
	classifyMaxTokens = 256
	classifyTimeout   = 20 * time.Second
)

// QueryIntent is the structured output of the LLM classifier.
//
// Category is one of IntentFactualEntity / IntentTopic / IntentTemporal /
// IntentConversational. Entity is the proper noun the question targets
// (person, company, project) when applicable; Attribute is the property being
// asked for (e.g. "national_number", "address", "phone", "birth_date", "iban",
// "invoice"). Both can be empty when the category is not factual_entity.
type QueryIntent struct {
	Category   string  `json:"category"`
	Entity     string  `json:"entity,omitempty"`
	Attribute  string  `json:"attribute,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

const intentClassifierSystemPrompt = `You classify a search query into one of four categories.

Categories:
- factual_entity: question about a specific named entity (a person, company, or project) seeking a precise factual attribute (national number, address, phone, birth date, IBAN, invoice, etc.).
- topic: thematic question about a subject ("emails about VAT payment", "notes on project X").
- temporal: question primarily anchored on time ("yesterday's brief", "last invoice received") with no specific named entity.
- conversational: short follow-up that requires conversation context to be resolved ("and his IBAN", "the previous one").

For factual_entity, also extract:
- entity: the proper noun (the *name*) targeted (e.g. "Jean", "Stripe", "Hygur").
- attribute: short label for the property requested. Use one of: national_number, address, phone, birth_date, iban, amount, invoice, communication, person, organization, project, topic, other.

Respond ONLY with valid JSON, no commentary, no markdown fences. Schema:
{"category":"<cat>","entity":"<name or empty>","attribute":"<label or empty>","confidence":<0..1>}`

const intentClassifierFewShot = `Examples:

Query: "quel est le numéro national d'Jean ?"
{"category":"factual_entity","entity":"Jean","attribute":"national_number","confidence":0.95}

Query: "TVA déclaration trimestrielle"
{"category":"topic","entity":"","attribute":"","confidence":0.9}

Query: "adresse postale d'Jean"
{"category":"factual_entity","entity":"Jean","attribute":"address","confidence":0.95}

Query: "dernière facture reçue"
{"category":"temporal","entity":"","attribute":"","confidence":0.9}

Query: "dernière facture Stripe"
{"category":"factual_entity","entity":"Stripe","attribute":"invoice","confidence":0.85}

Query: "et son IBAN"
{"category":"conversational","entity":"","attribute":"iban","confidence":0.9}

Query: "notes sur le projet Hygur"
{"category":"topic","entity":"Hygur","attribute":"","confidence":0.85}

Query: "qui travaille sur le projet Hygur ?"
{"category":"factual_entity","entity":"Hygur","attribute":"person","confidence":0.9}

Query: "documents qui mentionnent Acme Compta"
{"category":"factual_entity","entity":"Acme Compta","attribute":"organization","confidence":0.9}
`

// ClassifyQuery asks the LLM to classify the query into one of four intent
// categories and extract the entity / attribute pair when relevant.
//
// Returns an error on LLM failure so the caller can decide between abstaining
// and falling back to a default strategy. The parser tolerates the same JSON
// quirks as the judge (markdown fences, <think> blocks, bare-array shapes).
func ClassifyQuery(ctx context.Context, client *llm.Client, query string) (*QueryIntent, error) {
	if client == nil {
		return nil, fmt.Errorf("classify: nil llm client")
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("classify: empty query")
	}

	cctx, cancel := context.WithTimeout(ctx, classifyTimeout)
	defer cancel()

	userPrompt := intentClassifierFewShot + "\nQuery: " + fmt.Sprintf("%q", query) + "\nReturn the JSON now."

	resp, err := client.Chat(cctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: intentClassifierSystemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0,
		MaxTokens:   classifyMaxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("classify: chat failed: %w", err)
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return nil, fmt.Errorf("classify: empty response")
	}

	rawAnswer := resp.Choices[0].Message.Content
	if strings.TrimSpace(rawAnswer) == "" {
		rawAnswer = resp.Choices[0].Message.Reasoning
	}
	intent, err := parseIntentResponse(rawAnswer)
	if err != nil {
		return nil, fmt.Errorf("classify: parse: %w", err)
	}
	return intent, nil
}

func parseIntentResponse(raw string) (*QueryIntent, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	if i := strings.Index(raw, "</think>"); i >= 0 {
		raw = strings.TrimSpace(raw[i+len("</think>"):])
	}

	// Some models emit a JSON array containing one object — accept it.
	trimmed := strings.TrimLeft(raw, " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		var arr []QueryIntent
		if err := json.Unmarshal([]byte(raw), &arr); err != nil {
			return nil, fmt.Errorf("invalid json array: %w (raw=%q)", err, truncate(raw, 200))
		}
		if len(arr) == 0 {
			return nil, fmt.Errorf("empty array")
		}
		return normalizeIntent(&arr[0]), nil
	}

	var out QueryIntent
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("invalid json: %w (raw=%q)", err, truncate(raw, 200))
	}
	return normalizeIntent(&out), nil
}

func normalizeIntent(q *QueryIntent) *QueryIntent {
	q.Category = strings.ToLower(strings.TrimSpace(q.Category))
	q.Entity = strings.TrimSpace(q.Entity)
	q.Attribute = strings.ToLower(strings.TrimSpace(q.Attribute))
	switch q.Category {
	case IntentFactualEntity, IntentTopic, IntentTemporal, IntentConversational:
		// ok
	default:
		// Unknown label — coerce to topic so we fall back to the baseline pipeline
		// instead of erroring the whole probe run.
		q.Category = IntentTopic
	}
	return q
}
