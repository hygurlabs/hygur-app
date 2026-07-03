package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/retrieval"
)

// TestSearchKnowledgeBaseTool_WrapsExcerptsUntrusted is the WP3 Décision 1
// guarantee (test b, tool path): every excerpt the tool returns — the exact
// bytes injected into the prompt as the tool result — is wrapped in the uniform
// UNTRUSTED envelope so the model reads retrieved content as data, not
// instructions.
func TestSearchKnowledgeBaseTool_WrapsExcerptsUntrusted(t *testing.T) {
	db := setupSearchTestDB(t)
	defer db.Close()

	server := mockEmbeddingServer(t)
	defer server.Close()

	llmClient := llm.NewClientWithHTTP(server.URL, 10*time.Second, 0, http.DefaultClient)
	searcher := retrieval.NewUnifiedSearcher(db, llmClient)
	// minConfidence 0 so seeded fixtures pass the score gate.
	tool := NewSearchKnowledgeBaseTool(searcher, 10, 0, 0)

	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"programming"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var out SearchKnowledgeBaseResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(out.Sources) == 0 {
		t.Fatal("expected at least one source")
	}
	for i, s := range out.Sources {
		if want := retrieval.WrapUntrusted(retrieval.UnwrapUntrusted(s.Excerpt)); want != s.Excerpt {
			t.Fatalf("source %d excerpt not wrapped in UNTRUSTED envelope: %q", i, s.Excerpt)
		}
		if !strings.Contains(s.Excerpt, "UNTRUSTED CONTENT") {
			t.Fatalf("source %d excerpt missing envelope marker: %q", i, s.Excerpt)
		}
	}
}

func TestSearchKnowledgeBaseTool_Metadata(t *testing.T) {
	tool := NewSearchKnowledgeBaseTool(nil, 0, 0, 0)

	if tool.Name() != "search_knowledge_base" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "search_knowledge_base")
	}

	if !strings.Contains(tool.Description(), "knowledge base") {
		t.Errorf("Description should mention knowledge base, got %q", tool.Description())
	}

	schema := tool.ParameterSchema()
	if schema["type"] != "object" {
		t.Errorf("schema type should be object, got %v", schema["type"])
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties should be a map")
	}
	if _, ok := props["query"]; !ok {
		t.Error("schema should declare a 'query' parameter")
	}
	if _, ok := props["top_k"]; !ok {
		t.Error("schema should declare a 'top_k' parameter")
	}

	required, _ := schema["required"].([]string)
	if len(required) != 1 || required[0] != "query" {
		t.Errorf("only 'query' should be required, got %v", required)
	}
}

func TestSearchKnowledgeBaseTool_DefaultTopK(t *testing.T) {
	tool := NewSearchKnowledgeBaseTool(nil, 0, 0, 0)
	if tool.defaultTopK != 10 {
		t.Errorf("default topK should fall back to 10 when 0 is passed, got %d", tool.defaultTopK)
	}

	tool = NewSearchKnowledgeBaseTool(nil, 25, 0, 0)
	if tool.defaultTopK != 25 {
		t.Errorf("explicit topK should be honoured, got %d", tool.defaultTopK)
	}
}

func TestSearchKnowledgeBaseTool_RejectsInvalidArgs(t *testing.T) {
	tool := NewSearchKnowledgeBaseTool(nil, 10, 0, 0)
	ctx := context.Background()

	t.Run("missing query", func(t *testing.T) {
		_, err := tool.Execute(ctx, json.RawMessage(`{}`))
		if err == nil {
			t.Error("expected error when query is missing")
		}
	})

	t.Run("empty query", func(t *testing.T) {
		_, err := tool.Execute(ctx, json.RawMessage(`{"query": ""}`))
		if err == nil {
			t.Error("expected error when query is empty")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := tool.Execute(ctx, json.RawMessage(`{not json}`))
		if err == nil {
			t.Error("expected error on malformed JSON")
		}
	})

	t.Run("nil searcher", func(t *testing.T) {
		_, err := tool.Execute(ctx, json.RawMessage(`{"query": "anything"}`))
		if err == nil {
			t.Error("expected error when searcher is nil")
		}
	})
}

func TestSearchKnowledgeBaseTool_RegistersCleanly(t *testing.T) {
	registry := NewRegistry()
	tool := NewSearchKnowledgeBaseTool(nil, 10, 0, 0)
	if err := registry.Register(tool); err != nil {
		t.Fatalf("registration failed: %v", err)
	}

	defs := registry.OpenAIDefinitions()
	if len(defs) != 1 {
		t.Fatalf("expected 1 tool def, got %d", len(defs))
	}

	fn, ok := defs[0]["function"].(map[string]any)
	if !ok {
		t.Fatal("OpenAI def should expose a function map")
	}
	if fn["name"] != "search_knowledge_base" {
		t.Errorf("OpenAI def name = %v, want search_knowledge_base", fn["name"])
	}
}
