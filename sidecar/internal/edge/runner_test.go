package edge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// TestRunner_RunLoop verifies the shared sync loop: it pushes immediately (before
// the first interval sleep) and exits promptly when the context is cancelled.
func TestRunner_RunLoop(t *testing.T) {
	pushed := make(chan struct{}, 8)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/knowledge/ingest-text", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "indexed"})
		select {
		case pushed <- struct{}{}:
		default:
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "doc.md"), "hello world")
	cfgPath := filepath.Join(dir, "config.json")
	cfg := &Config{
		Mode: "cloud", Server: srv.URL, Token: "tok",
		Folder: dir, ProtonMailbox: "All Mail", IntervalSecs: 1,
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { NewRunner(cfgPath).RunLoop(ctx); close(done) }()

	// First push must arrive immediately (RunOnce before the interval sleep).
	select {
	case <-pushed:
	case <-time.After(3 * time.Second):
		t.Fatal("no push within 3s — RunLoop didn't sync on start")
	}

	// Cancelling the context must stop the loop promptly.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunLoop did not exit after ctx cancel")
	}
}
