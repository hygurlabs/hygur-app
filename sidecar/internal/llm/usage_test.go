package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type recordedUsage struct {
	category  string
	tokensIn  int
	tokensOut int
}

type fakeRecorder struct {
	mu     sync.Mutex
	events []recordedUsage
}

func (f *fakeRecorder) RecordUsage(category string, tokensIn, tokensOut int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, recordedUsage{category, tokensIn, tokensOut})
}

func (f *fakeRecorder) snapshot() []recordedUsage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedUsage(nil), f.events...)
}

// streamUsageServer emits a minimal SSE completion ending with a usage chunk,
// and captures whether the request opted into stream_options.include_usage.
func streamUsageServer(t *testing.T, sawIncludeUsage *bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		*sawIncludeUsage = req.StreamOptions != nil && req.StreamOptions.IncludeUsage

		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`)
		flusher.Flush()
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}}`)
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

func TestStreamChatRecordsUsageAndSetsStreamOptions(t *testing.T) {
	var sawIncludeUsage bool
	server := streamUsageServer(t, &sawIncludeUsage)
	defer server.Close()

	client := newTestClient(t, server)
	rec := &fakeRecorder{}
	client.SetUsageRecorder(rec, "")

	err := client.StreamChat(context.Background(), ChatRequest{Model: "m"}, func(string, bool, *Usage) error { return nil })
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if !sawIncludeUsage {
		t.Error("expected stream_options.include_usage on the request when a recorder is attached")
	}
	got := rec.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected 1 recorded usage, got %d (%+v)", len(got), got)
	}
	if got[0] != (recordedUsage{"chat", 12, 7}) {
		t.Errorf("recorded %+v, want {chat 12 7}", got[0])
	}
}

func TestStreamChatRichRecordsUsageWithCategory(t *testing.T) {
	var sawIncludeUsage bool
	server := streamUsageServer(t, &sawIncludeUsage)
	defer server.Close()

	client := newTestClient(t, server)
	rec := &fakeRecorder{}
	client.SetUsageRecorder(rec, "indexing") // distinct indexing client

	err := client.StreamChatRich(context.Background(), ChatRequest{Model: "m"}, func(StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("StreamChatRich: %v", err)
	}
	got := rec.snapshot()
	if len(got) != 1 || got[0] != (recordedUsage{"indexing", 12, 7}) {
		t.Fatalf("recorded %+v, want one {indexing 12 7}", got)
	}
}

func TestStreamWithoutRecorderOmitsStreamOptions(t *testing.T) {
	var sawIncludeUsage bool
	server := streamUsageServer(t, &sawIncludeUsage)
	defer server.Close()

	client := newTestClient(t, server) // no recorder
	err := client.StreamChat(context.Background(), ChatRequest{Model: "m"}, func(string, bool, *Usage) error { return nil })
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if sawIncludeUsage {
		t.Error("stream_options.include_usage must not be set when no recorder is attached")
	}
}

func TestEmbeddingsRecordUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]}],"model":"emb","usage":{"prompt_tokens":42,"total_tokens":42}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	rec := &fakeRecorder{}
	client.SetUsageRecorder(rec, "chat")

	if _, err := client.GenerateEmbeddings(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("GenerateEmbeddings: %v", err)
	}
	got := rec.snapshot()
	if len(got) != 1 || got[0] != (recordedUsage{"embedding", 42, 0}) {
		t.Fatalf("recorded %+v, want one {embedding 42 0}", got)
	}
}
