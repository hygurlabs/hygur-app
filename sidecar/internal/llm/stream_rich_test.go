package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestToolCallAssembler_MergesFragments(t *testing.T) {
	a := NewToolCallAssembler()
	// Chunk 1: id + name
	a.Add(ToolCallDelta{
		Index: 0,
		ID:    "call_1",
		Type:  "function",
		Function: &ToolCallFunctionDelta{
			Name:      "create_note",
			Arguments: `{"title":"`,
		},
	})
	// Chunk 2: more args
	a.Add(ToolCallDelta{
		Index: 0,
		Function: &ToolCallFunctionDelta{
			Arguments: `Hello"`,
		},
	})
	// Chunk 3: closing args
	a.Add(ToolCallDelta{
		Index: 0,
		Function: &ToolCallFunctionDelta{
			Arguments: `,"content":"World"}`,
		},
	})

	calls := a.Finalize()
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	got := calls[0]
	if got.ID != "call_1" {
		t.Errorf("ID = %q, want call_1", got.ID)
	}
	if got.Type != "function" {
		t.Errorf("Type = %q, want function", got.Type)
	}
	if got.Function.Name != "create_note" {
		t.Errorf("Name = %q, want create_note", got.Function.Name)
	}
	want := `{"title":"Hello","content":"World"}`
	if got.Function.Arguments != want {
		t.Errorf("Arguments = %q, want %q", got.Function.Arguments, want)
	}
}

func TestToolCallAssembler_PreservesIndexOrder(t *testing.T) {
	a := NewToolCallAssembler()
	a.Add(ToolCallDelta{Index: 1, ID: "second", Function: &ToolCallFunctionDelta{Name: "b"}})
	a.Add(ToolCallDelta{Index: 0, ID: "first", Function: &ToolCallFunctionDelta{Name: "a"}})

	calls := a.Finalize()
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(calls))
	}
	// Order is determined by *first observation*, not by index.
	if calls[0].ID != "second" || calls[1].ID != "first" {
		t.Errorf("order = %v %v, want second then first", calls[0].ID, calls[1].ID)
	}
}

func TestToolCallAssembler_EmptyReturnsNil(t *testing.T) {
	a := NewToolCallAssembler()
	if got := a.Finalize(); got != nil {
		t.Fatalf("Finalize on empty assembler = %v, want nil", got)
	}
	if a.Len() != 0 {
		t.Fatalf("Len on empty = %d, want 0", a.Len())
	}
}

// TestStreamChatRich_EmitsToolCallDeltas exercises the rich streaming path
// end-to-end by stubbing an OpenAI-compatible SSE response that carries a
// tool_calls fragment, a finish_reason, then [DONE].
func TestStreamChatRich_EmitsToolCallDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)

		// First chunk: assistant role declared, no content.
		write := func(payload string) {
			_, _ = w.Write([]byte("data: " + payload + "\n\n"))
			flusher.Flush()
		}

		write(`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`)
		// Tool-call fragment 1: id + name + partial arguments.
		write(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_42","type":"function","function":{"name":"create_note","arguments":"{\"title\":\""}}]}}]}`)
		// Fragment 2: rest of arguments.
		write(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"hi\"}"}}]}}]}`)
		// Finish reason.
		write(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
		write(`[DONE]`)
	}))
	defer server.Close()

	client := NewClientWithHTTP(server.URL, 5*time.Second, 0, server.Client())

	var observed []StreamEvent
	err := client.StreamChatRich(context.Background(), ChatRequest{Model: "test"}, func(evt StreamEvent) error {
		observed = append(observed, evt)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChatRich: %v", err)
	}

	// Walk the observed events: at least one with tool_call deltas, one with
	// finish_reason=tool_calls, and one Done event.
	var (
		sawToolFragments bool
		sawFinishReason  string
		sawDone          bool
	)
	asm := NewToolCallAssembler()
	for _, evt := range observed {
		if evt.Done {
			sawDone = true
			continue
		}
		if evt.FinishReason != "" {
			sawFinishReason = evt.FinishReason
		}
		for _, d := range evt.ToolCallDeltas {
			asm.Add(d)
			sawToolFragments = true
		}
	}
	if !sawToolFragments {
		t.Fatal("expected at least one event with ToolCallDeltas")
	}
	if sawFinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want tool_calls", sawFinishReason)
	}
	if !sawDone {
		t.Error("expected Done event")
	}

	calls := asm.Finalize()
	if len(calls) != 1 || calls[0].ID != "call_42" || calls[0].Function.Name != "create_note" {
		t.Fatalf("assembled tool call = %+v", calls)
	}
	// Validate the JSON arguments parse cleanly.
	var args map[string]string
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatalf("arguments not valid JSON: %v (got %q)", err, calls[0].Function.Arguments)
	}
	if args["title"] != "hi" {
		t.Errorf("title = %q, want hi", args["title"])
	}
}
