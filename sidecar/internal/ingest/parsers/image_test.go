package parsers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestImageParser_SupportedExtensions verifies all expected extensions are declared.
func TestImageParser_SupportedExtensions(t *testing.T) {
	p := NewImageParser("")
	exts := p.SupportedExtensions()

	expected := map[string]bool{
		".png":  true,
		".jpg":  true,
		".jpeg": true,
		".heic": true,
		".webp": true,
	}

	if len(exts) != len(expected) {
		t.Fatalf("expected %d extensions, got %d: %v", len(expected), len(exts), exts)
	}

	for _, ext := range exts {
		if !expected[ext] {
			t.Errorf("unexpected extension %q", ext)
		}
	}
}

// TestImageParser_FailSoftWhenTesseractUnavailable verifies that Parse never
// panics and returns a valid (possibly empty) result even when Tesseract is
// not installed or fails. This test does not depend on Tesseract being
// available on the system.
func TestImageParser_FailSoftWhenTesseractUnavailable(t *testing.T) {
	p := NewImageParser("") // no vision endpoint either

	ctx := context.Background()
	// A tiny 1×1 white PNG in raw bytes — small enough that Tesseract would
	// return nothing useful, but valid enough to pass io.ReadAll.
	pngBytes := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk length + type
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // width=1, height=1
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, // bit depth=8, color=RGB
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41, // IDAT chunk
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
		0x00, 0x00, 0x02, 0x00, 0x01, 0xE2, 0x21, 0xBC,
		0x33, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, // IEND chunk
		0x44, 0xAE, 0x42, 0x60, 0x82,
	}

	text, meta, err := p.Parse(ctx, strings.NewReader(string(pngBytes)))

	// Must not panic (the test reaching here already validates that).
	// Must not return a fatal error.
	if err != nil {
		t.Errorf("Parse returned unexpected error: %v", err)
	}

	// Metadata must always be set with source_type = "image".
	if meta == nil {
		t.Fatal("expected non-nil metadata")
	}
	if st, ok := meta["source_type"]; !ok || st != "image" {
		t.Errorf("expected meta[source_type] = \"image\", got %v", st)
	}

	// text may be empty (Tesseract not installed / no vision endpoint) — that
	// is explicitly allowed by the fail-soft contract.
	_ = text
}

// TestImageParser_FailSoftOnReadError verifies that an unreadable reader
// produces a valid empty result rather than an error.
func TestImageParser_FailSoftOnReadError(t *testing.T) {
	p := NewImageParser("")

	// errReader always returns an error on Read.
	errReader := &alwaysErrorReader{}

	ctx := context.Background()
	text, meta, err := p.Parse(ctx, errReader)

	if err != nil {
		t.Errorf("Parse should be fail-soft but returned: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text on read error, got %q", text)
	}
	if meta == nil {
		t.Fatal("expected non-nil metadata")
	}
}

// TestImageParser_VisionFallbackCallsEndpoint verifies that when Tesseract
// returns no text, the parser posts to the configured vision endpoint.
func TestImageParser_VisionFallbackCallsEndpoint(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello world"}}]}`))
	}))
	defer srv.Close()

	// Inject vision endpoint; Tesseract is expected to produce no output for
	// the tiny stub PNG (or not be installed — both are fine for this test).
	p := NewImageParser(srv.URL)

	ctx := context.Background()
	text, _, _ := p.Parse(ctx, strings.NewReader("not-a-real-png"))

	if !called {
		t.Error("vision endpoint was never called")
	}
	// If the vision endpoint responded successfully we should get the text.
	// If Tesseract somehow produced output first, text may differ — that's ok
	// as long as the endpoint was reached.
	_ = text
}

// TestDetectTesseractLang_FrPath validates the language detection heuristic.
func TestDetectTesseractLang_FrPath(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		path string
		want string
	}{
		{"/home/user/documents/fr/screenshot.png", "fra+eng"},
		{"/home/user/FR/notes/photo.jpg", "fra+eng"},
		{"/home/user/documents/en/screenshot.png", "eng+fra"},
		{"/home/user/screenshots/photo.jpg", "eng+fra"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := detectTesseractLang(ctx, tt.path)
			if got != tt.want {
				t.Errorf("detectTesseractLang(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// TestCleanOCRText verifies short-line filtering.
func TestCleanOCRText(t *testing.T) {
	input := "ab\nhello world\nok\nthis is valid text\n\n  \n"
	got := cleanOCRText(input)

	lines := strings.Split(got, "\n")
	for _, line := range lines {
		if len([]rune(line)) > 0 && len([]rune(line)) < 3 {
			t.Errorf("cleanOCRText kept short line %q", line)
		}
	}

	if !strings.Contains(got, "hello world") {
		t.Error("cleanOCRText dropped valid line 'hello world'")
	}
	if !strings.Contains(got, "this is valid text") {
		t.Error("cleanOCRText dropped valid line 'this is valid text'")
	}
}

// alwaysErrorReader is an io.Reader that always returns an error.
type alwaysErrorReader struct{}

func (r *alwaysErrorReader) Read(_ []byte) (int, error) {
	return 0, http.ErrBodyReadAfterClose
}
