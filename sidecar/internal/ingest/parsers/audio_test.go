package parsers

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAudioParser_SupportedExtensions verifies all expected extensions.
func TestAudioParser_SupportedExtensions(t *testing.T) {
	p := NewAudioParser("http://localhost:1234")
	exts := p.SupportedExtensions()

	expected := map[string]bool{
		".mp3": true,
		".m4a": true,
		".wav": true,
		".ogg": true,
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

// TestParseAudio_PostsMultipart verifies that Parse sends a properly formed
// multipart request with a "file" field to the transcription endpoint.
func TestParseAudio_PostsMultipart(t *testing.T) {
	var (
		gotMethod      string
		gotContentType string
		gotHasFile     bool
		gotModel       string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")

		mediaType, params, err := mime.ParseMediaType(gotContentType)
		if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
			t.Errorf("expected multipart content-type, got %q (err: %v)", gotContentType, err)
			http.Error(w, "bad content type", http.StatusBadRequest)
			return
		}

		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("multipart read error: %v", err)
				break
			}
			switch part.FormName() {
			case "file":
				gotHasFile = true
			case "model":
				data, _ := io.ReadAll(part)
				gotModel = string(data)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"text":"bonjour le monde"}`))
	}))
	defer srv.Close()

	p := NewAudioParser(srv.URL)
	ctx := context.Background()

	// Send a minimal fake audio payload.
	text, meta, err := p.Parse(ctx, strings.NewReader("fake-audio-bytes"))

	if err != nil {
		t.Errorf("Parse returned unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if !gotHasFile {
		t.Error("multipart request missing 'file' field")
	}
	if gotModel != defaultWhisperModel {
		t.Errorf("model field = %q, want %q", gotModel, defaultWhisperModel)
	}
	if text != "bonjour le monde" {
		t.Errorf("text = %q, want %q", text, "bonjour le monde")
	}
	if st, ok := meta["source_type"]; !ok || st != "audio" {
		t.Errorf("meta[source_type] = %v, want \"audio\"", st)
	}
}

// TestParseAudio_FailSoftOnHTTPError verifies that a 500 response from the
// server results in an empty string and no error (fail-soft contract).
func TestParseAudio_FailSoftOnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewAudioParser(srv.URL)
	ctx := context.Background()

	text, meta, err := p.Parse(ctx, strings.NewReader("fake-audio-bytes"))

	// Must not return a fatal error.
	if err != nil {
		t.Errorf("Parse should be fail-soft but returned: %v", err)
	}
	// Text must be empty on error.
	if text != "" {
		t.Errorf("expected empty text on HTTP error, got %q", text)
	}
	// Metadata must always be set.
	if meta == nil {
		t.Fatal("expected non-nil metadata")
	}
	if st, ok := meta["source_type"]; !ok || st != "audio" {
		t.Errorf("meta[source_type] = %v, want \"audio\"", st)
	}
}

// TestParseAudio_FailSoftWhenNoEndpoint verifies fail-soft when llmBaseURL is empty.
func TestParseAudio_FailSoftWhenNoEndpoint(t *testing.T) {
	p := NewAudioParser("") // no endpoint
	ctx := context.Background()

	text, meta, err := p.Parse(ctx, strings.NewReader("fake-audio"))

	if err != nil {
		t.Errorf("Parse should be fail-soft but returned: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text when no endpoint, got %q", text)
	}
	if meta == nil {
		t.Fatal("expected non-nil metadata")
	}
	if st, ok := meta["source_type"]; !ok || st != "audio" {
		t.Errorf("meta[source_type] = %v, want \"audio\"", st)
	}
}

// TestParseAudio_FailSoftOnMalformedJSON verifies that malformed JSON from the
// server produces an empty result without a fatal error.
func TestParseAudio_FailSoftOnMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not-valid-json`))
	}))
	defer srv.Close()

	p := NewAudioParser(srv.URL)
	ctx := context.Background()

	text, meta, err := p.Parse(ctx, strings.NewReader("fake-audio"))

	if err != nil {
		t.Errorf("Parse should be fail-soft but returned: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text on JSON error, got %q", text)
	}
	if meta == nil {
		t.Fatal("expected non-nil metadata")
	}
}
