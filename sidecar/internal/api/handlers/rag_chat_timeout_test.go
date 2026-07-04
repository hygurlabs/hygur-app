package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
)

// swap temporarily overrides a *time.Duration package backstop for one test and
// returns a restore func. Tests are non-parallel so mutating these globals is safe.
func swapDuration(p *time.Duration, v time.Duration) func() {
	old := *p
	*p = v
	return func() { *p = old }
}

// TestRAGChatHandler_SynthesisRoundTimeout: when the model sits silent past
// synthesisRoundTimeout, the handler must NOT hang — it emits the honest SSE
// `error` event (code TIMEOUT) the client already consumes, and returns.
func TestRAGChatHandler_SynthesisRoundTimeout(t *testing.T) {
	defer swapDuration(&synthesisRoundTimeout, 80*time.Millisecond)()

	// Mock LLM that stalls well past the (shrunk) backstop before any byte.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"late\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	handler := NewRAGChatHandler(createMockLLMClient(srv.URL), nil, nil, DefaultRAGConfig, zerolog.Nop())

	reqBody := `{"messages":[{"role":"user","content":"Hello there"}],"stream":true,"rag_enabled":false}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() { handler.ServeHTTP(rec, req); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler hung on a timed-out synthesis round instead of emitting an error")
	}

	var sawTimeout bool
	for _, e := range parseSSEEvents(t, rec.Body.String()) {
		if e["type"] == "error" {
			if errObj, ok := e["error"].(map[string]any); ok && errObj["code"] == "TIMEOUT" {
				sawTimeout = true
			}
		}
	}
	if !sawTimeout {
		t.Fatalf("expected an SSE error event with code TIMEOUT, body:\n%s", rec.Body.String())
	}
}

// blockingTool ignores its context and blocks — modelling a tool wedged on a
// stuck resource. It proves executeToolGuarded returns on the backstop even when
// the tool itself never honours cancellation.
type blockingTool struct{ tools.NoSideEffect }

func (blockingTool) Name() string                    { return "search_knowledge_base" }
func (blockingTool) Description() string             { return "stub blocking tool" }
func (blockingTool) ParameterSchema() map[string]any { return map[string]any{"type": "object"} }
func (blockingTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	time.Sleep(600 * time.Millisecond) // deliberately ignores ctx
	return json.RawMessage(`{"ok":true}`), nil
}

// TestRAGChatHandler_ToolExecTimeout: a tool that blocks past toolExecTimeout
// (and ignores ctx) must not wedge the turn. The guarded exec returns a deadline
// error, which is surfaced on the tool_call event and fed back to the model, and
// the turn recovers to a final answer.
func TestRAGChatHandler_ToolExecTimeout(t *testing.T) {
	defer swapDuration(&toolExecTimeout, 80*time.Millisecond)()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(s string) { _, _ = w.Write([]byte("data: " + s + "\n\n")); flusher.Flush() }
		if strings.Contains(string(body), `"role":"tool"`) {
			// Round 2: the model recovers after seeing the tool error.
			write(`{"id":"2","choices":[{"delta":{"content":"Recovered."},"finish_reason":"stop"}]}`)
			write("[DONE]")
			return
		}
		// Round 1: request the (soon-to-time-out) tool.
		write(`{"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"search_knowledge_base","arguments":"{}"}}]}}]}`)
		write(`{"id":"1","choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
		write("[DONE]")
	}))
	defer srv.Close()

	registry := tools.NewRegistry()
	registry.MustRegister(blockingTool{})

	handler := NewRAGChatHandler(createMockLLMClient(srv.URL), nil, nil, DefaultRAGConfig, zerolog.Nop())
	handler.SetToolRegistry(registry)

	reqBody := `{"messages":[{"role":"user","content":"look something up please"}],"stream":true,"rag_enabled":false}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() { handler.ServeHTTP(rec, req); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler hung on a blocking tool instead of backstopping it")
	}

	var toolErrSeen bool
	var prose strings.Builder
	for _, e := range parseSSEEvents(t, rec.Body.String()) {
		if e["type"] == "tool_call" {
			if _, ok := e["error"]; ok {
				toolErrSeen = true
			}
		}
		if d, ok := e["delta"].(string); ok {
			prose.WriteString(d)
		}
	}
	if !toolErrSeen {
		t.Errorf("expected a tool_call event carrying an error (the backstop), body:\n%s", rec.Body.String())
	}
	if !strings.Contains(prose.String(), "Recovered") {
		t.Errorf("turn did not recover to a final answer after the tool timeout, prose=%q", prose.String())
	}
}

// TestRAGChatHandler_HeartbeatDoesNotCorruptDeltas: with the heartbeat firing
// repeatedly during a slow round, the client contract still holds — assembling
// the `delta` events yields the exact answer, and the `{"type":"working"}`
// heartbeat events are present but inert (no delta/done/error).
func TestRAGChatHandler_HeartbeatDoesNotCorruptDeltas(t *testing.T) {
	defer swapDuration(&keepAliveInterval, 20*time.Millisecond)()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(s string) { _, _ = w.Write([]byte("data: " + s + "\n\n")); flusher.Flush() }
		write(`{"id":"1","choices":[{"delta":{"content":"Hello"}}]}`)
		time.Sleep(70 * time.Millisecond) // heartbeats fire in this gap
		write(`{"id":"1","choices":[{"delta":{"content":" world"},"finish_reason":"stop"}]}`)
		time.Sleep(70 * time.Millisecond)
		write("[DONE]")
	}))
	defer srv.Close()

	handler := NewRAGChatHandler(createMockLLMClient(srv.URL), nil, nil, DefaultRAGConfig, zerolog.Nop())

	reqBody := `{"messages":[{"role":"user","content":"greet the world"}],"stream":true,"rag_enabled":false}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Client-side reconstruction: concat every delta, ignore working/comment lines.
	var assembled strings.Builder
	var working int
	var done bool
	for _, e := range parseSSEEvents(t, rec.Body.String()) {
		switch e["type"] {
		case "working":
			working++
			if _, has := e["delta"]; has {
				t.Error("heartbeat event carried a delta — delta contract corrupted")
			}
		}
		if d, ok := e["delta"].(string); ok {
			assembled.WriteString(d)
		}
		if e["done"] == true {
			done = true
		}
	}
	if assembled.String() != "Hello world" {
		t.Errorf("assembled answer = %q, want %q (heartbeats corrupted the delta stream)", assembled.String(), "Hello world")
	}
	if !done {
		t.Error("no terminal done event — done contract broken by heartbeats")
	}
	if working == 0 {
		t.Error("expected at least one heartbeat working event to have fired during the slow round")
	}
}
