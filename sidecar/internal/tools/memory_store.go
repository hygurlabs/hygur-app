package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

// MemoryStoreTool stores and retrieves memories from the database.
type MemoryStoreTool struct {
	store *store.DB
	llm   *llm.Client
}

// NewMemoryStoreTool creates a new MemoryStoreTool.
func NewMemoryStoreTool(store *store.DB, llm *llm.Client) *MemoryStoreTool {
	return &MemoryStoreTool{
		store: store,
		llm:   llm,
	}
}

// StoreRequest represents the input for storing a memory.
type StoreRequest struct {
	Content    string
	MemoryType string // "fact", "action", "preference"
	ContextID  string
}

// StoreResult represents the result of storing a memory.
type StoreResult struct {
	MemoryID string
}

// Store saves a new memory to the database with a default 90-day TTL.
func (t *MemoryStoreTool) Store(content string, memoryType string, contextID string) (string, error) {
	if content == "" {
		return "", fmt.Errorf("content cannot be empty")
	}

	memoryID := uuid.New().String()
	expiresAt := time.Now().Add(90 * 24 * time.Hour)

	err := t.store.InsertMemory(&store.Memory{
		MemoryID:  memoryID,
		Type:      store.MemoryType(memoryType),
		Content:   content,
		ContextID: contextID,
		CreatedAt: time.Now(),
		ExpiresAt: &expiresAt,
		Score:     0.0,
	})
	if err != nil {
		return "", fmt.Errorf("failed to store memory: %w", err)
	}

	return memoryID, nil
}

// StoreWithExpiry saves a memory with an explicit expiration time. Pass nil
// for `expiresAt` to keep the memory forever.
func (t *MemoryStoreTool) StoreWithExpiry(content, memoryType, contextID string, expiresAt *time.Time) (string, error) {
	if content == "" {
		return "", fmt.Errorf("content cannot be empty")
	}
	memoryID := uuid.New().String()
	err := t.store.InsertMemory(&store.Memory{
		MemoryID:  memoryID,
		Type:      store.MemoryType(memoryType),
		Content:   content,
		ContextID: contextID,
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
		Score:     0.0,
	})
	if err != nil {
		return "", fmt.Errorf("failed to store memory: %w", err)
	}
	return memoryID, nil
}

// ExtractedMemory is the typed output of the LLM extractor. Type maps to
// store.MemoryType ("fact" | "preference" | "action"). ExpiresAt is RFC3339
// or empty (no expiration set; default TTL applied at insert time).
type ExtractedMemory struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// extractorSystemPrompt is the system instruction the LLM receives. It
// deliberately constrains the output: at most 3 entries, JSON array only,
// strict types, no commentary. The prompt also tells the model to return an
// empty array when the turn is short banter / pleasantries — that's the main
// failure mode we observed with the previous freeform string-list version.
const extractorSystemPrompt = `You extract durable user-specific facts from a conversation turn. Output a strict JSON array (no prose, no markdown).

Rules:
- Each item: {"type": "fact" | "preference" | "action", "content": "<≤140 chars>", "expires_at"?: "YYYY-MM-DD"}
- "fact": durable identity / relationship / config (e.g. "Comptable: Pierre Dupont chez Acme Compta").
- "preference": stated user preference (e.g. "Préfère les réponses en français").
- "action": something the user committed to doing with a deadline (set expires_at to the deadline).
- Skip greetings, acknowledgements, jokes, transient task details, anything ephemeral.
- Skip information that is not specifically about the user or their world.
- If the turn contains nothing memorable, return [].
- Maximum 3 items. Output ONLY the JSON array.`

// ExtractMemoriesFromTurn analyses a single user/assistant turn and returns
// the durable memories worth persisting. Returns an empty slice (no error)
// when the turn carries nothing memorable. Caller is expected to invoke this
// in a goroutine — the LLM call typically takes 1-3 s.
func (t *MemoryStoreTool) ExtractMemoriesFromTurn(ctx context.Context, userMessage, assistantMessage string) ([]ExtractedMemory, error) {
	if t.llm == nil {
		return nil, fmt.Errorf("LLM client not configured")
	}
	// Cheap pre-filter: short banter never produces useful memories and
	// burns LLM time. Threshold tuned by observation — anything below ~30
	// combined chars is "ok", "thanks", "merci", etc.
	combined := strings.TrimSpace(userMessage) + " " + strings.TrimSpace(assistantMessage)
	if len(strings.TrimSpace(combined)) < 30 {
		return nil, nil
	}

	userPrompt := fmt.Sprintf("User: %s\n\nAssistant: %s", userMessage, assistantMessage)
	resp, err := t.llm.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: extractorSystemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Stream:      false,
		Temperature: 0,
		MaxTokens:   400,
	})
	if err != nil {
		return nil, fmt.Errorf("extract memory: %w", err)
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return nil, nil
	}

	return parseExtractorOutput(resp.Choices[0].Message.Content)
}

// parseExtractorOutput is split out so tests can exercise the parser without
// spinning up a fake LLM. Tolerates minor model wobble: leading prose, code
// fences, single-object output.
func parseExtractorOutput(raw string) ([]ExtractedMemory, error) {
	text := strings.TrimSpace(raw)
	// Strip ```json fences if the model added them.
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	// If the model returned commentary then a JSON block, locate the array.
	if start := strings.Index(text, "["); start >= 0 {
		if end := strings.LastIndex(text, "]"); end > start {
			text = text[start : end+1]
		}
	}

	var out []ExtractedMemory
	if err := json.Unmarshal([]byte(text), &out); err == nil {
		return validateExtracted(out), nil
	}

	// Tolerance: accept a single object instead of an array.
	var single ExtractedMemory
	if err := json.Unmarshal([]byte(text), &single); err == nil && single.Content != "" {
		return validateExtracted([]ExtractedMemory{single}), nil
	}

	return nil, nil
}

// validateExtracted enforces the contract advertised in the system prompt.
// Drops items with empty content, normalizes types, caps the slice at 3.
func validateExtracted(in []ExtractedMemory) []ExtractedMemory {
	out := make([]ExtractedMemory, 0, len(in))
	for _, m := range in {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		typ := strings.ToLower(strings.TrimSpace(m.Type))
		switch typ {
		case "fact", "preference", "action":
			// ok
		default:
			typ = "fact"
		}
		out = append(out, ExtractedMemory{
			Type:      typ,
			Content:   content,
			ExpiresAt: strings.TrimSpace(m.ExpiresAt),
		})
		if len(out) >= 3 {
			break
		}
	}
	return out
}

// PersistExtracted saves the extractor output. Returns the count of rows
// stored and the first row-level error encountered (if any) so callers can
// distinguish "nothing memorable" from "DB rejected every insert".
func (t *MemoryStoreTool) PersistExtracted(memories []ExtractedMemory, contextID string) (int, error) {
	stored := 0
	var firstErr error
	for _, m := range memories {
		var expiry *time.Time
		if m.ExpiresAt != "" {
			if d, err := time.Parse("2006-01-02", m.ExpiresAt); err == nil {
				expiry = &d
			}
		}
		var (
			id  string
			err error
		)
		if expiry != nil {
			id, err = t.StoreWithExpiry(m.Content, m.Type, contextID, expiry)
		} else {
			id, err = t.Store(m.Content, m.Type, contextID)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if id != "" {
			stored++
		}
	}
	return stored, firstErr
}
