package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWebSearch_ParsesSearxng: the tool hits the configured (trusted) endpoint
// with format=json and returns capped, untrusted-marked results.
func TestWebSearch_ParsesSearxng(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "json" || r.URL.Query().Get("q") == "" {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]string{
				{"title": "First", "url": "https://example.com/1", "content": "snippet one"},
				{"title": "Second", "url": "https://example.com/2", "content": "snippet two"},
			},
		})
	}))
	defer ts.Close()

	tool := NewWebSearchTool(ts.URL, "", 5)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"hello"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var res webSearchResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res.Results) != 2 || res.Results[0].URL != "https://example.com/1" {
		t.Fatalf("unexpected results: %+v", res.Results)
	}
	if !strings.Contains(strings.ToUpper(res.Note), "UNTRUSTED") {
		t.Errorf("missing untrusted note: %q", res.Note)
	}

	// Empty query rejected.
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"query":""}`)); err == nil {
		t.Error("empty query should error")
	}
}

// TestFetchURL_SSRFGuard: model-controlled URLs that resolve internal must be
// refused, and bad schemes/args rejected — before any request leaves the host.
func TestFetchURL_SSRFGuard(t *testing.T) {
	tool := NewFetchURLTool(0)
	bad := []string{
		`{}`,                              // missing url
		`{"url":"http://127.0.0.1:1/"}`,   // loopback
		`{"url":"http://169.254.169.254/"}`, // cloud metadata
		`{"url":"http://10.0.0.5/"}`,      // private
		`{"url":"ftp://example.com/"}`,    // scheme
	}
	for _, args := range bad {
		if _, err := tool.Execute(context.Background(), json.RawMessage(args)); err == nil {
			t.Errorf("fetch_url(%s) should have been refused", args)
		}
	}
}
