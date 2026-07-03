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

// Store saves a new manual memory to the database with a default 90-day TTL.
// Manual memories are auto-accepted: they bypass the pending-review queue and
// become eligible for system-prompt injection immediately.
func (t *MemoryStoreTool) Store(content string, memoryType string, contextID string) (string, error) {
	if content == "" {
		return "", fmt.Errorf("content cannot be empty")
	}

	memoryID := uuid.New().String()
	expiresAt := time.Now().Add(90 * 24 * time.Hour)
	now := time.Now()
	embedding := t.embedContent(content)

	err := t.store.InsertMemory(&store.Memory{
		MemoryID:   memoryID,
		Type:       store.MemoryType(memoryType),
		Content:    content,
		ContextID:  contextID,
		CreatedAt:  now,
		ExpiresAt:  &expiresAt,
		Score:      0.0,
		Source:     store.MemorySourceManual,
		AcceptedAt: &now,
		Embedding:  embedding,
		SessionID:  contextID,
	})
	if err != nil {
		return "", fmt.Errorf("failed to store memory: %w", err)
	}

	return memoryID, nil
}

// embedContent returns the embedding for `content`, or nil when the LLM client
// is missing/embedding fails. Phase 3.3 injection still works without
// embeddings (it falls back to "skip injection") so this is best-effort.
func (t *MemoryStoreTool) embedContent(content string) []float32 {
	if t.llm == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	vec, err := t.llm.GenerateEmbedding(ctx, content)
	if err != nil {
		return nil
	}
	return vec
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
- "preference": stated user preference (e.g. "Prefers concise answers").
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
		// Post-turn memory extraction: background work triggered by the chat, but
		// it must NOT consume the user's Ask cap (WP16a).
		Category: "background",
		Pass:     "memory_extract",
		Messages: []llm.Message{
			{Role: "system", Content: extractorSystemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Stream:      false,
		Temperature: llm.Temp(0),
		TopP:        llm.Temp(1),
		Seed:        llm.SeedOf(42),
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

// PersistExtracted saves the extractor output as PENDING (Phase 3.3). Each
// memory lands with source='extracted' and accepted_at=NULL, so they will not
// be injected into chats until the user explicitly accepts them via the
// /memory/{id}/accept endpoint. Returns the count of rows stored and the
// first row-level error encountered.
func (t *MemoryStoreTool) PersistExtracted(memories []ExtractedMemory, sessionID string) (int, error) {
	stored, err := t.persistExtracted(memories, sessionID)
	return len(stored), err
}

// PersistExtractedReturning behaves like PersistExtracted but returns the rows
// actually stored (id, type, content, source, accepted_at) so the caller can
// surface each autonomous write — e.g. the chat handler streams a `memory_write`
// SSE event per row so the user sees it inline instead of only in the review
// queue. Storage semantics are identical: every row lands PENDING.
func (t *MemoryStoreTool) PersistExtractedReturning(memories []ExtractedMemory, sessionID string) ([]store.Memory, error) {
	return t.persistExtracted(memories, sessionID)
}

// persistExtracted is the shared insert loop behind PersistExtracted and
// PersistExtractedReturning. It returns the rows successfully stored and the
// first row-level error encountered.
func (t *MemoryStoreTool) persistExtracted(memories []ExtractedMemory, sessionID string) ([]store.Memory, error) {
	stored := make([]store.Memory, 0, len(memories))
	var firstErr error
	for _, m := range memories {
		var expiry *time.Time
		if m.ExpiresAt != "" {
			if d, err := time.Parse("2006-01-02", m.ExpiresAt); err == nil {
				expiry = &d
			}
		}
		// Default 90-day TTL for extracted memories without an explicit deadline.
		if expiry == nil {
			fallback := time.Now().Add(90 * 24 * time.Hour)
			expiry = &fallback
		}
		mem := store.Memory{
			MemoryID:   uuid.New().String(),
			Type:       store.MemoryType(m.Type),
			Content:    m.Content,
			ContextID:  sessionID,
			CreatedAt:  time.Now(),
			ExpiresAt:  expiry,
			Score:      0.0,
			Source:     store.MemorySourceExtracted,
			AcceptedAt: nil, // pending review
			Embedding:  t.embedContent(m.Content),
			SessionID:  sessionID,
		}
		if err := t.store.InsertMemory(&mem); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		stored = append(stored, mem)
	}
	return stored, firstErr
}

// sessionExtractorSystemPrompt is the prompt used by ExtractMemoriesFromSession.
// Identical contract to extractorSystemPrompt but loosened the cap to 5 because
// a full conversation typically holds more memorable signals than a single turn.
const sessionExtractorSystemPrompt = `You distill durable user-specific facts from a full conversation transcript. Output a strict JSON array (no prose, no markdown).

Rules:
- Each item: {"type": "fact" | "preference" | "action", "content": "<≤140 chars>", "expires_at"?: "YYYY-MM-DD"}
- "fact": durable identity / relationship / config (e.g. "Comptable: Pierre Dupont chez Acme Compta").
- "preference": stated user preference (e.g. "Prefers concise answers").
- "action": something the user committed to doing with a deadline (set expires_at to the deadline).
- Skip greetings, acknowledgements, jokes, transient task details, anything ephemeral.
- Skip information that is not specifically about the user or their world.
- Quality over quantity: prefer 0 items to a weak item.
- If the transcript contains nothing memorable, return [].
- Maximum 5 items. Output ONLY the JSON array.`

// ExtractMemoriesFromSession analyses a full chat transcript (alternating user
// and assistant messages) and proposes durable memories for the user to review.
// Returns an empty slice when the conversation carries nothing memorable.
//
// The caller is expected to invoke this *after* a conversation ends rather
// than per-turn — that's what makes the candidates broad enough to be worth
// reviewing as a batch in the Memories tab.
func (t *MemoryStoreTool) ExtractMemoriesFromSession(ctx context.Context, transcript []TranscriptMessage) ([]ExtractedMemory, error) {
	if t.llm == nil {
		return nil, fmt.Errorf("LLM client not configured")
	}
	rendered := renderTranscript(transcript)
	if strings.TrimSpace(rendered) == "" {
		return nil, nil
	}
	// Cheap pre-filter: a transcript under ~80 combined chars is essentially
	// pleasantries. Mirrors the per-turn extractor's threshold but a bit more
	// permissive because session-level we have multiple turns to combine.
	if len(strings.TrimSpace(rendered)) < 80 {
		return nil, nil
	}

	resp, err := t.llm.Chat(ctx, llm.ChatRequest{
		// Session-level memory extraction: background, off the Ask cap (WP16a).
		Category: "background",
		Pass:     "memory_extract_session",
		Messages: []llm.Message{
			{Role: "system", Content: sessionExtractorSystemPrompt},
			{Role: "user", Content: rendered},
		},
		Stream:      false,
		Temperature: llm.Temp(0),
		TopP:        llm.Temp(1),
		Seed:        llm.SeedOf(42),
		MaxTokens:   600,
	})
	if err != nil {
		return nil, fmt.Errorf("extract session memory: %w", err)
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return nil, nil
	}
	memories, err := parseExtractorOutput(resp.Choices[0].Message.Content)
	if err != nil {
		return nil, err
	}
	// Cap session-level extraction at 5 (extends the per-turn cap of 3).
	if len(memories) > 5 {
		memories = memories[:5]
	}
	return memories, nil
}

// TranscriptMessage is the minimal shape ExtractMemoriesFromSession needs from
// a chat transcript. We don't reuse llm.Message here because we don't want
// attachments / tool calls polluting the extractor input — they add noise
// without informing the durable-memory decision.
type TranscriptMessage struct {
	Role    string // "user" | "assistant" | other (skipped)
	Content string
}

// renderTranscript turns a list of transcript messages into a readable
// "User: ...\n\nAssistant: ..." block. Empty content is skipped; tool/system
// messages are dropped because they don't represent user-relevant signal.
func renderTranscript(msgs []TranscriptMessage) string {
	var b strings.Builder
	for _, m := range msgs {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(m.Role))
		switch role {
		case "user":
			b.WriteString("User: ")
		case "assistant":
			b.WriteString("Assistant: ")
		default:
			continue
		}
		b.WriteString(content)
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
