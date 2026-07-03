package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
)

// TestDeterminedAnswerFromToolResult encodes the REAL shapes of the engine→render bridge: a
// high/medium lookup_identifier result renders the ENGINE's value; a decline renders an honest
// "no verified value" with NO value; any other tool renders nothing.
func TestDeterminedAnswerFromToolResult(t *testing.T) {
	mk := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

	t.Run("high value is rendered from the engine", func(t *testing.T) {
		res := mk(tools.LookupResponse{Type: "enterprise_number", Label: "TVA", Subject: "you",
			Value: "1021", Tier: "high"})
		evt, ok := determinedAnswerFromToolResult("lookup_identifier", res)
		if !ok {
			t.Fatal("expected a determined_answer event")
		}
		if evt.Value != "1021" {
			t.Errorf("value = %q, want 1021", evt.Value)
		}
		if evt.Confidence != "high" || evt.Subject != "you" || evt.Label != "TVA" {
			t.Errorf("unexpected event: %+v", evt)
		}
	})

	t.Run("decline renders no value, honest message", func(t *testing.T) {
		res := mk(tools.LookupResponse{Type: "iban", Label: "IBAN", Subject: "you", Tier: "none",
			Reason: "uncorroborated_candidate"})
		evt, ok := determinedAnswerFromToolResult("lookup_identifier", res)
		if !ok {
			t.Fatal("expected a determined_answer event on decline")
		}
		if evt.Value != "" {
			t.Errorf("decline must carry no value; got %q", evt.Value)
		}
		if evt.Confidence != "none" || evt.Message == "" {
			t.Errorf("decline event should be none + honest message: %+v", evt)
		}
	})

	t.Run("other tools render nothing", func(t *testing.T) {
		if _, ok := determinedAnswerFromToolResult("search_knowledge_base", mk(map[string]any{"x": 1})); ok {
			t.Error("only lookup_identifier should produce a determined_answer")
		}
	})
}

// stubLookupTool is a canned lookup_identifier that returns the engine's high-confidence VAT
// value "1021" — standing in for the deterministic resolver so the handler test can prove the
// SSE render without a DB. Read-only.
type stubLookupTool struct{ tools.NoSideEffect }

func (stubLookupTool) Name() string                    { return "lookup_identifier" }
func (stubLookupTool) Description() string             { return "stub" }
func (stubLookupTool) ParameterSchema() map[string]any { return map[string]any{"type": "object"} }
func (stubLookupTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.Marshal(tools.LookupResponse{Type: "enterprise_number", Label: "TVA", Subject: "you",
		Value: "1021", Tier: "high", Guidance: "State this value plainly."})
}

// determinedMockLLM is a two-round mock: round 1 (no tool message yet) emits a lookup_identifier
// tool call; round 2 (a role:"tool" message is present) HEDGES in prose, even citing a document
// number "152". The test asserts the hedge cannot change the engine-rendered answer.
func determinedMockLLM(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(s string) { _, _ = w.Write([]byte("data: " + s + "\n\n")); flusher.Flush() }
		if strings.Contains(string(body), `"role":"tool"`) {
			// Round 2: the model hedges and even reaches for a document number.
			write(`{"id":"2","choices":[{"delta":{"content":"Je ne suis pas certain — peut-être 152 ?"},"finish_reason":"stop"}]}`)
			write("[DONE]")
			return
		}
		// Round 1: the language-triggered tool call.
		write(`{"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup_identifier","arguments":"{\"entity\":\"moi\",\"type\":\"TVA\"}"}}]}}]}`)
		write(`{"id":"1","choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
		write("[DONE]")
	}))
}

// TestRAGChatHandler_DeterminedAnswerRenderedCutSafe is the CORE barrier: for « quel est mon
// numéro de TVA », the engine's value ("1021") is streamed as an authoritative determined_answer
// render, and the LLM's hedging prose (which even name-drops the document number "152") CANNOT
// substitute, hedge, or decline it. The value the user sees comes from the engine, not the model.
func TestRAGChatHandler_DeterminedAnswerRenderedCutSafe(t *testing.T) {
	srv := determinedMockLLM(t)
	defer srv.Close()

	registry := tools.NewRegistry()
	registry.MustRegister(stubLookupTool{})

	handler := NewRAGChatHandler(createMockLLMClient(srv.URL), nil, nil, DefaultRAGConfig, zerolog.Nop())
	handler.SetToolRegistry(registry)

	reqBody := `{"messages":[{"role":"user","content":"quel est mon numéro de TVA ?"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var determined *DeterminedAnswerEvent
	var prose strings.Builder
	for _, e := range parseSSEEvents(t, rec.Body.String()) {
		if e["type"] == "determined_answer" {
			b, _ := json.Marshal(e)
			var ev DeterminedAnswerEvent
			_ = json.Unmarshal(b, &ev)
			determined = &ev
		}
		if d, ok := e["delta"].(string); ok {
			prose.WriteString(d)
		}
	}

	if determined == nil {
		t.Fatal("no determined_answer event: the engine value was never rendered")
	}
	if determined.Value != "1021" {
		t.Errorf("determined value = %q, want 1021 (the engine's value)", determined.Value)
	}
	if determined.Value == "152" {
		t.Error("the render served a document number (152) — RAG must never supply the value")
	}
	if determined.Confidence != "high" {
		t.Errorf("confidence = %q, want high", determined.Confidence)
	}
	// The LLM did hedge in prose (proving it's demoted to cosmetic) yet the engine render stands.
	if !strings.Contains(prose.String(), "152") {
		t.Log("note: the mock LLM hedge text was expected to include 152 (cosmetic only)")
	}
}
