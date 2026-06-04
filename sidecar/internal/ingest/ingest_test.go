package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

// mockParser is a test parser implementation.
type mockParser struct {
	extensions []string
	content    string
	metadata   Metadata
	err        error
	parseCalls int
}

func (m *mockParser) SupportedExtensions() []string {
	return m.extensions
}

func (m *mockParser) Parse(ctx context.Context, r io.Reader) (string, Metadata, error) {
	m.parseCalls++
	if m.err != nil {
		return "", nil, m.err
	}
	// Respect context cancellation
	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	default:
	}
	return m.content, m.metadata, nil
}

// Regression: uploading "IMG_0090.JPG" failed with "no parser available for
// file type: .JPG". The lookup must be case-insensitive (normalizeExtension
// lowercases), so an uppercase extension resolves to a parser registered for
// the lowercase form.
func TestGetParser_CaseInsensitiveExtension(t *testing.T) {
	ing := NewIngestor()
	ing.RegisterParser(&mockParser{extensions: []string{".jpg"}})

	// normalizeExtension lowercases at the registry boundary, so the original
	// failing case (".JPG", from IMG_0090.JPG) resolves to the ".jpg" parser.
	if ing.GetParser(".JPG") == nil {
		t.Error(`GetParser(".JPG") = nil, want the parser registered for ".jpg"`)
	}
	if ing.GetParser(".gif") != nil {
		t.Error(`GetParser(".gif") should be nil — no parser registered for it`)
	}
}

func TestValidatePath(t *testing.T) {
	// Create a temporary directory for test files
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create a symlink for testing
	symlinkPath := filepath.Join(tmpDir, "link.txt")
	if err := os.Symlink(tmpFile, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	tests := []struct {
		name    string
		path    string
		wantErr error
	}{
		{
			name:    "valid absolute path",
			path:    tmpFile,
			wantErr: nil,
		},
		{
			name:    "relative path",
			path:    "test.txt",
			wantErr: ErrNotAbsolutePath,
		},
		{
			name:    "path with dot-dot",
			path:    filepath.Join(tmpDir, "..", "test.txt"),
			wantErr: ErrPathTraversal,
		},
		{
			name:    "symlink",
			path:    symlinkPath,
			wantErr: ErrSymlinkNotAllowed,
		},
		{
			name:    "non-existent file",
			path:    filepath.Join(tmpDir, "nonexistent.txt"),
			wantErr: os.ErrNotExist,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePath(tt.path)
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("ValidatePath(%q) = %v, want nil", tt.path, err)
				}
				return
			}

			if err == nil {
				t.Errorf("ValidatePath(%q) = nil, want error containing %v", tt.path, tt.wantErr)
				return
			}

			if !errors.Is(err, tt.wantErr) && !os.IsNotExist(err) {
				t.Errorf("ValidatePath(%q) = %v, want %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestNewIngestor(t *testing.T) {
	i := NewIngestor()
	if i == nil {
		t.Fatal("NewIngestor returned nil")
	}
	if i.parsers == nil {
		t.Fatal("parsers map is nil")
	}
}

func TestIngestor_RegisterParser(t *testing.T) {
	i := NewIngestor()

	p := &mockParser{
		extensions: []string{".txt", ".text"},
		content:    "test content",
	}

	i.RegisterParser(p)

	// Verify parsers are registered
	if got := i.GetParser(".txt"); got != p {
		t.Error("parser not registered for .txt")
	}
	if got := i.GetParser(".text"); got != p {
		t.Error("parser not registered for .text")
	}
	if got := i.GetParser(".md"); got != nil {
		t.Error("unexpected parser for .md")
	}
}

func TestIngestor_RegisterParser_Overwrite(t *testing.T) {
	i := NewIngestor()

	p1 := &mockParser{extensions: []string{".txt"}, content: "first"}
	p2 := &mockParser{extensions: []string{".txt"}, content: "second"}

	i.RegisterParser(p1)
	i.RegisterParser(p2)

	// Second registration should overwrite
	if got := i.GetParser(".txt"); got != p2 {
		t.Error("parser was not overwritten")
	}
}

func TestIngestor_Ingest(t *testing.T) {
	// Create a temporary test file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	i := NewIngestor()
	p := &mockParser{
		extensions: []string{".txt"},
		content:    "parsed test content",
		metadata:   Metadata{"title": "Test Document"},
	}
	i.RegisterParser(p)

	ctx := context.Background()
	result, err := i.Ingest(ctx, tmpFile, IngestOptions{})
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}

	if result.Status != "indexed" {
		t.Errorf("Status = %q, want 'indexed'", result.Status)
	}
	if result.ChunkCount == 0 {
		t.Error("ChunkCount = 0, want > 0")
	}
	if result.ContentID == "" {
		t.Error("ContentID is empty")
	}
	if p.parseCalls != 1 {
		t.Errorf("parser called %d times, want 1", p.parseCalls)
	}
}

func TestIngestor_Ingest_NoParser(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.xyz")
	if err := os.WriteFile(tmpFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	i := NewIngestor()
	ctx := context.Background()
	_, err := i.Ingest(ctx, tmpFile, IngestOptions{})
	if err == nil {
		t.Fatal("expected error for unknown extension")
	}
	if !errors.Is(err, ErrNoParser) {
		t.Errorf("error = %v, want ErrNoParser", err)
	}
}

func TestIngestor_Ingest_FileTooLarge(t *testing.T) {
	// Create a file that exceeds MaxFileSize
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "large.txt")

	// Create a sparse file that reports large size
	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	// Truncate to MaxFileSize + 1
	if err := f.Truncate(MaxFileSize + 1); err != nil {
		f.Close()
		t.Fatalf("failed to truncate file: %v", err)
	}
	f.Close()

	i := NewIngestor()
	p := &mockParser{extensions: []string{".txt"}, content: "test"}
	i.RegisterParser(p)

	ctx := context.Background()
	_, err = i.Ingest(ctx, tmpFile, IngestOptions{})
	if err == nil {
		t.Fatal("expected error for large file")
	}
	if !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("error = %v, want ErrFileTooLarge", err)
	}
}

func TestIngestor_Ingest_InvalidPath(t *testing.T) {
	i := NewIngestor()
	ctx := context.Background()

	tests := []struct {
		name string
		path string
	}{
		{"relative path", "test.txt"},
		{"path traversal", "/tmp/../etc/passwd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := i.Ingest(ctx, tt.path, IngestOptions{})
			if err == nil {
				t.Error("expected error for invalid path")
			}
		})
	}
}

func TestIngestor_Ingest_ContextCancellation(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	i := NewIngestor()
	p := &mockParser{
		extensions: []string{".txt"},
		content:    "test",
	}
	i.RegisterParser(p)

	// Create already cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := i.Ingest(ctx, tmpFile, IngestOptions{})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestIngestor_Ingest_ContextTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	i := NewIngestor()
	p := &mockParser{
		extensions: []string{".txt"},
		content:    "test",
	}
	i.RegisterParser(p)

	// Create context with very short timeout that's already expired
	ctx, cancel := context.WithTimeout(context.Background(), -1*time.Second)
	defer cancel()

	_, err := i.Ingest(ctx, tmpFile, IngestOptions{})
	if err == nil {
		t.Fatal("expected error for timed out context")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestIngestor_Ingest_EmptyContent(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "empty.txt")
	if err := os.WriteFile(tmpFile, []byte(""), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	i := NewIngestor()
	p := &mockParser{
		extensions: []string{".txt"},
		content:    "", // Parser returns empty content
	}
	i.RegisterParser(p)

	ctx := context.Background()
	_, err := i.Ingest(ctx, tmpFile, IngestOptions{})
	if err == nil {
		t.Fatal("expected error for empty content")
	}
	if !errors.Is(err, ErrEmptyContent) {
		t.Errorf("error = %v, want ErrEmptyContent", err)
	}
}

func TestIngestor_Ingest_WithOptions(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	i := NewIngestor()
	p := &mockParser{
		extensions: []string{".txt"},
		content:    "test content",
	}
	i.RegisterParser(p)

	projectID := "project-123"
	opts := IngestOptions{
		ProjectID: &projectID,
		Tags:      []string{"important", "documentation"},
	}

	ctx := context.Background()
	result, err := i.Ingest(ctx, tmpFile, opts)
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}

	// Options are stored but not yet used - just verify ingestion succeeds
	if result.Status != "indexed" {
		t.Errorf("Status = %q, want 'indexed'", result.Status)
	}
}

// TestIngestor_Ingest_AppliesTier1ToMarkdown verifies that Tier1 regex
// extraction runs uniformly on non-email source types when ingested through
// the full pipeline (parser -> normalize -> Tier1 -> store). Without this, a
// markdown file containing an IBAN or amount would land in the DB without the
// `extracted_*` keys that retrieval/entity_search relies on.
func TestIngestor_Ingest_AppliesTier1ToMarkdown(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "facture.md")
	if err := os.WriteFile(tmpFile, []byte("# Facture\n"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory DB: %v", err)
	}
	defer db.Close()

	body := "Cher client,\nMontant : 1234,56 EUR\nIBAN : BE68 5390 0754 7034\nTVA : BE0123456789"
	i := NewIngestorWithStore(db)
	p := &mockParser{
		extensions: []string{".md"},
		content:    body,
		metadata:   Metadata{"title": "Facture test"},
	}
	i.RegisterParser(p)

	ctx := context.Background()
	result, err := i.Ingest(ctx, tmpFile, IngestOptions{})
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}

	item, err := db.GetKnowledgeItem(ctx, result.ContentID)
	if err != nil {
		t.Fatalf("GetKnowledgeItem failed: %v", err)
	}
	if item == nil {
		t.Fatalf("knowledge item not persisted")
	}

	for _, key := range []string{"extracted_iban", "extracted_amounts", "extracted_vat_numbers"} {
		if _, ok := item.Metadata[key]; !ok {
			t.Errorf("metadata[%q] missing on persisted markdown item; got keys: %v", key, metadataKeys(item.Metadata))
		}
	}
}

func metadataKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// fakeChatServer responds to chat completions with a fixed content. Used to
// simulate Tier 2 NER without depending on a real LLM endpoint.
func fakeChatServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"id":      "test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"choices": []map[string]any{
				{
					"index":         0,
					"message":       map[string]string{"role": "assistant", "content": content},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// TestIngestor_Ingest_PopulatesTier2Metadata verifies that when an LLM client
// is configured, Tier 2 NER runs alongside ingestion and its output lands in
// the persisted metadata under the conventional `extracted_*` keys.
func TestIngestor_Ingest_PopulatesTier2Metadata(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "brief.md")
	if err := os.WriteFile(tmpFile, []byte("# Brief"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tier2JSON := `{"persons":["Jean Dupont"],"organizations":["Acme Compta"],"projects":["Hygur"],"topics":["TVA"]}`
	srv := fakeChatServer(t, tier2JSON)
	defer srv.Close()
	llmClient := llm.NewClientWithHTTP(srv.URL, 5*time.Second, 0, srv.Client())

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	i := NewIngestorWithStore(db)
	i.SetLLMClient(llmClient)
	i.RegisterParser(&mockParser{
		extensions: []string{".md"},
		content:    "Jean travaille avec Acme Compta sur le projet Hygur.",
		metadata:   Metadata{"title": "Brief"},
	})

	result, err := i.Ingest(context.Background(), tmpFile, IngestOptions{})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	item, err := db.GetKnowledgeItem(context.Background(), result.ContentID)
	if err != nil || item == nil {
		t.Fatalf("get item: %v (item=%v)", err, item)
	}

	for _, key := range []string{
		"extracted_persons", "extracted_orgs", "extracted_projects",
		"extracted_topics", "extracted_v2_at", "extracted_v2_version",
	} {
		if _, ok := item.Metadata[key]; !ok {
			t.Errorf("metadata[%q] missing; got keys: %v", key, metadataKeys(item.Metadata))
		}
	}
}

// TestIngestor_Ingest_Tier2FailureDoesNotBlockIngestion verifies fail-soft:
// if the LLM endpoint errors, ingestion still succeeds and the document is
// persisted (without Tier 2 metadata) so the backfill CLI can re-process it.
func TestIngestor_Ingest_Tier2FailureDoesNotBlockIngestion(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "x.md")
	if err := os.WriteFile(tmpFile, []byte("# x"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	llmClient := llm.NewClientWithHTTP(srv.URL, 1*time.Second, 0, srv.Client())

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	i := NewIngestorWithStore(db)
	i.SetLLMClient(llmClient)
	i.RegisterParser(&mockParser{
		extensions: []string{".md"},
		content:    "any content",
		metadata:   Metadata{},
	})

	result, err := i.Ingest(context.Background(), tmpFile, IngestOptions{})
	if err != nil {
		t.Fatalf("ingest should succeed despite tier2 failure: %v", err)
	}
	if result.Status != "indexed" {
		t.Errorf("status = %q, want indexed", result.Status)
	}

	item, err := db.GetKnowledgeItem(context.Background(), result.ContentID)
	if err != nil || item == nil {
		t.Fatalf("doc should be persisted: %v", err)
	}
	if _, ok := item.Metadata["extracted_v2_at"]; ok {
		t.Errorf("extracted_v2_at should NOT be set when tier2 errored; got %v", item.Metadata["extracted_v2_at"])
	}
}

func TestIngestor_Ingest_Directory(t *testing.T) {
	tmpDir := t.TempDir()

	i := NewIngestor()
	ctx := context.Background()

	_, err := i.Ingest(ctx, tmpDir, IngestOptions{})
	if err == nil {
		t.Fatal("expected error when ingesting directory")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error should mention directory: %v", err)
	}
}
