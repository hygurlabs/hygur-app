package parsers

import (
	"context"
	"io"
	"testing"

	"github.com/hygur/sidecar/internal/ingest"
)

// mockParser is a test parser implementation.
type mockParser struct {
	extensions []string
	content    string
	metadata   ingest.Metadata
	err        error
}

func (m *mockParser) SupportedExtensions() []string {
	return m.extensions
}

func (m *mockParser) Parse(ctx context.Context, r io.Reader) (string, ingest.Metadata, error) {
	if m.err != nil {
		return "", nil, m.err
	}
	return m.content, m.metadata, nil
}

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if r.parsers == nil {
		t.Fatal("parsers map is nil")
	}
}

func TestRegistry_Register(t *testing.T) {
	tests := []struct {
		name       string
		extensions []string
		wantErr    bool
	}{
		{
			name:       "single extension",
			extensions: []string{".txt"},
			wantErr:    false,
		},
		{
			name:       "multiple extensions",
			extensions: []string{".md", ".markdown"},
			wantErr:    false,
		},
		{
			name:       "extension without dot",
			extensions: []string{"json"},
			wantErr:    false,
		},
		{
			name:       "empty extensions",
			extensions: []string{},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			p := &mockParser{extensions: tt.extensions}
			err := r.Register(p)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Verify all extensions are registered
			for _, ext := range tt.extensions {
				got := r.Get(ext)
				if got != p {
					t.Errorf("Get(%q) = %v, want %v", ext, got, p)
				}
			}
		})
	}
}

func TestRegistry_Register_Conflict(t *testing.T) {
	r := NewRegistry()

	p1 := &mockParser{extensions: []string{".txt"}}
	if err := r.Register(p1); err != nil {
		t.Fatalf("first register failed: %v", err)
	}

	p2 := &mockParser{extensions: []string{".txt", ".text"}}
	err := r.Register(p2)
	if err == nil {
		t.Error("expected conflict error, got nil")
	}

	// Verify original parser is still registered
	got := r.Get(".txt")
	if got != p1 {
		t.Error("original parser was overwritten")
	}

	// Verify .text was not registered (atomic failure)
	got = r.Get(".text")
	if got != nil {
		t.Error(".text should not be registered after conflict")
	}
}

func TestRegistry_Get(t *testing.T) {
	r := NewRegistry()
	p := &mockParser{extensions: []string{".TXT"}}
	if err := r.Register(p); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	tests := []struct {
		ext  string
		want bool
	}{
		{".txt", true},
		{".TXT", true},
		{".Txt", true},
		{"txt", true},
		{".md", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			got := r.Get(tt.ext)
			if tt.want && got == nil {
				t.Errorf("Get(%q) = nil, want parser", tt.ext)
			}
			if !tt.want && got != nil {
				t.Errorf("Get(%q) = parser, want nil", tt.ext)
			}
		})
	}
}

func TestRegistry_Extensions(t *testing.T) {
	r := NewRegistry()

	p1 := &mockParser{extensions: []string{".txt", ".text"}}
	p2 := &mockParser{extensions: []string{".md"}}

	if err := r.Register(p1); err != nil {
		t.Fatalf("register p1 failed: %v", err)
	}
	if err := r.Register(p2); err != nil {
		t.Fatalf("register p2 failed: %v", err)
	}

	exts := r.Extensions()
	if len(exts) != 3 {
		t.Errorf("got %d extensions, want 3", len(exts))
	}

	// Check all expected extensions are present
	extMap := make(map[string]bool)
	for _, ext := range exts {
		extMap[ext] = true
	}

	for _, want := range []string{".txt", ".text", ".md"} {
		if !extMap[want] {
			t.Errorf("missing extension %q", want)
		}
	}
}

// TestRegistry_RoutesImageExtensionsToImageParser verifies that all image
// extensions are routed to an ImageParser instance after registration.
func TestRegistry_RoutesImageExtensionsToImageParser(t *testing.T) {
	r := NewRegistry()
	imageParser := NewImageParser("")

	if err := r.Register(imageParser); err != nil {
		t.Fatalf("Register(ImageParser) failed: %v", err)
	}

	for _, ext := range []string{".png", ".jpg", ".jpeg", ".heic", ".webp"} {
		got := r.Get(ext)
		if got == nil {
			t.Errorf("Get(%q) = nil, want ImageParser", ext)
			continue
		}
		if _, ok := got.(*ImageParser); !ok {
			t.Errorf("Get(%q) = %T, want *ImageParser", ext, got)
		}
	}
}

// TestRegistry_RoutesAudioExtensionsToAudioParser verifies that all audio
// extensions are routed to an AudioParser instance after registration.
func TestRegistry_RoutesAudioExtensionsToAudioParser(t *testing.T) {
	r := NewRegistry()
	audioParser := NewAudioParser("")

	if err := r.Register(audioParser); err != nil {
		t.Fatalf("Register(AudioParser) failed: %v", err)
	}

	for _, ext := range []string{".mp3", ".m4a", ".wav", ".ogg"} {
		got := r.Get(ext)
		if got == nil {
			t.Errorf("Get(%q) = nil, want AudioParser", ext)
			continue
		}
		if _, ok := got.(*AudioParser); !ok {
			t.Errorf("Get(%q) = %T, want *AudioParser", ext, got)
		}
	}
}

func TestNormalizeExtension(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{".txt", ".txt"},
		{"txt", ".txt"},
		{".TXT", ".txt"},
		{"TXT", ".txt"},
		{".MD", ".md"},
		{"", "."},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeExtension(tt.input)
			if got != tt.want {
				t.Errorf("normalizeExtension(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
