package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

func TestParseExtractorOutput_PlainArray(t *testing.T) {
	raw := `[{"type":"fact","content":"Comptable: Pierre Dupont chez Acme Compta"},{"type":"preference","content":"Préfère les réponses en français"}]`
	got, err := parseExtractorOutput(raw)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 memories, got %d", len(got))
	}
	if got[0].Type != "fact" || !strings.Contains(got[0].Content, "Pierre Dupont") {
		t.Fatalf("unexpected first memory: %+v", got[0])
	}
}

func TestParseExtractorOutput_StripsCodeFences(t *testing.T) {
	raw := "```json\n[{\"type\":\"fact\",\"content\":\"X\"}]\n```"
	got, err := parseExtractorOutput(raw)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(got) != 1 || got[0].Content != "X" {
		t.Fatalf("unexpected output: %+v", got)
	}
}

func TestParseExtractorOutput_WithLeadingProse(t *testing.T) {
	raw := `Voici les faits extraits :
[{"type":"fact","content":"Y"}]`
	got, err := parseExtractorOutput(raw)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(got) != 1 || got[0].Content != "Y" {
		t.Fatalf("unexpected output: %+v", got)
	}
}

func TestParseExtractorOutput_SingleObjectFallback(t *testing.T) {
	raw := `{"type":"action","content":"Payer TVA","expires_at":"2026-04-30"}`
	got, err := parseExtractorOutput(raw)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(got) != 1 || got[0].Type != "action" || got[0].ExpiresAt != "2026-04-30" {
		t.Fatalf("unexpected output: %+v", got)
	}
}

func TestParseExtractorOutput_EmptyArray(t *testing.T) {
	got, err := parseExtractorOutput("[]")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no memories, got %d", len(got))
	}
}

func TestValidateExtracted_NormalizesAndCaps(t *testing.T) {
	in := []ExtractedMemory{
		{Type: "FACT", Content: "  one  "},
		{Type: "weird", Content: "two"},
		{Type: "preference", Content: ""},
		{Type: "action", Content: "three", ExpiresAt: "2026-05-01"},
		{Type: "fact", Content: "four"},
	}
	got := validateExtracted(in)
	if len(got) != 3 {
		t.Fatalf("want 3 (cap + drop empty), got %d: %+v", len(got), got)
	}
	if got[0].Type != "fact" || got[0].Content != "one" {
		t.Fatalf("first: want type=fact content=one, got %+v", got[0])
	}
	if got[1].Type != "fact" {
		t.Fatalf("second: unknown type should normalize to fact, got %+v", got[1])
	}
	if got[2].Type != "action" || got[2].ExpiresAt != "2026-05-01" {
		t.Fatalf("third: %+v", got[2])
	}
}

// TestExtractMemoriesFromTurn_IgnoresShortBanter ensures the cheap pre-filter
// kicks in before any LLM call when the turn is just pleasantries.
func TestExtractMemoriesFromTurn_IgnoresShortBanter(t *testing.T) {
	tool := &MemoryStoreTool{
		llm: &llm.Client{}, // not nil; would panic if called
	}
	got, err := tool.ExtractMemoriesFromTurn(context.Background(), "merci", "de rien")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("short banter should yield nil, got %+v", got)
	}
}

// TestExtractMemoriesFromTurn_ParsesMockedLLM exercises the full extraction
// path with a stubbed LM Studio chat endpoint.
func TestExtractMemoriesFromTurn_ParsesMockedLLM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		resp := llm.ChatResponse{
			Choices: []llm.Choice{{
				Message: &llm.Message{
					Role:    "assistant",
					Content: `[{"type":"fact","content":"Comptable: Pierre Dupont chez Acme Compta"}]`,
				},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := llm.NewClientWithHTTP(srv.URL, 5*time.Second, 0, srv.Client())
	tool := &MemoryStoreTool{llm: client}

	got, err := tool.ExtractMemoriesFromTurn(
		context.Background(),
		"Mon comptable s'appelle Pierre Dupont chez Acme Compta",
		"Bien noté, je m'en souviendrai.",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Type != "fact" || !strings.Contains(got[0].Content, "Pierre Dupont") {
		t.Fatalf("unexpected output: %+v", got)
	}
}

func TestPersistExtracted_StoresWithExpiry(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close()

	tool := &MemoryStoreTool{store: db}
	in := []ExtractedMemory{
		{Type: "fact", Content: "Comptable: Pierre Dupont"},
		{Type: "action", Content: "Payer TVA", ExpiresAt: "2026-04-30"},
	}
	stored, err := tool.PersistExtracted(in, "session-1")
	if err != nil {
		t.Fatalf("persist err: %v", err)
	}
	if stored != 2 {
		t.Fatalf("want 2 stored, got %d", stored)
	}
	got, err := db.SearchMemories(context.Background(), "Pierre", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 memory matching Pierre, got %d", len(got))
	}
}
