package edge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// captureServer records ingest-text pushes and answers /health.
func captureServer(t *testing.T) (*httptest.Server, *[]IngestText, *int) {
	t.Helper()
	var mu sync.Mutex
	var got []IngestText
	noToken := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/knowledge/ingest-text", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Hygur-Token") == "" {
			mu.Lock()
			noToken++
			mu.Unlock()
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var in IngestText
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		got = append(got, in)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "indexed"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &got, &noToken
}

func TestTextParsers_Extensions(t *testing.T) {
	p := TextParsers()
	for _, ext := range []string{".txt", ".text", ".md", ".markdown", ".docx", ".pdf"} {
		if p[ext] == nil {
			t.Errorf("TextParsers missing parser for %s", ext)
		}
	}
}

func TestClient_PushText(t *testing.T) {
	srv, got, _ := captureServer(t)
	c := NewClient(srv.URL, "dev-token")
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	status, err := c.PushText(context.Background(), IngestText{Text: "hi", SourceRef: "x:1", SourceType: "file"})
	if err != nil {
		t.Fatalf("PushText: %v", err)
	}
	if status != "indexed" {
		t.Errorf("status = %q, want indexed", status)
	}
	if len(*got) != 1 || (*got)[0].Text != "hi" || (*got)[0].SourceRef != "x:1" {
		t.Fatalf("server received %+v", *got)
	}
}

func TestFileSync_WalkExtractPush(t *testing.T) {
	srv, got, _ := captureServer(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "hello world")
	mustWrite(t, filepath.Join(dir, "note.md"), "# Title\n\nbody")
	mustWrite(t, filepath.Join(dir, "ignore.bin"), "\x00\x01binary")
	mustWrite(t, filepath.Join(dir, "sub", "deep.txt"), "nested")

	fs := NewFileSync(NewClient(srv.URL, "dev-token"), TextParsers())

	// First run (epoch watermark) → pushes the 3 supported text files, ignores .bin.
	st, err := fs.Run(context.Background(), dir, time.Time{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Pushed != 3 {
		t.Fatalf("pushed = %d, want 3 (got server=%d)", st.Pushed, len(*got))
	}
	for _, g := range *got {
		if g.SourceType != "file" || g.SourceRef == "" || g.Text == "" {
			t.Errorf("bad push: %+v", g)
		}
	}

	// Second run with a future watermark → everything skipped, nothing pushed.
	*got = (*got)[:0]
	st2, err := fs.Run(context.Background(), dir, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if st2.Pushed != 0 || st2.Skipped != 3 {
		t.Errorf("second run pushed=%d skipped=%d, want 0/3", st2.Pushed, st2.Skipped)
	}
	if len(*got) != 0 {
		t.Errorf("second run should push nothing, got %d", len(*got))
	}
}

func TestFileSync_PrunesNoise(t *testing.T) {
	srv, got, _ := captureServer(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "doc.md"), "real document")                   // kept
	mustWrite(t, filepath.Join(dir, "node_modules", "dep", "README.md"), "noise") // pruned (dep)
	mustWrite(t, filepath.Join(dir, "vendor", "LICENSE.md"), "license")           // pruned (dep)
	mustWrite(t, filepath.Join(dir, ".git", "notes.md"), "vcs")                   // pruned (dot-dir)
	mustWrite(t, filepath.Join(dir, ".secret.md"), "hidden")                      // pruned (hidden file)

	st, err := NewFileSync(NewClient(srv.URL, "tok"), TextParsers()).Run(context.Background(), dir, time.Time{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Pushed != 1 {
		t.Fatalf("pushed = %d, want 1 (only doc.md; deps/VCS/hidden pruned)", st.Pushed)
	}
	if len(*got) != 1 || (*got)[0].Title != "doc.md" {
		t.Fatalf("expected only doc.md, got %+v", *got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
