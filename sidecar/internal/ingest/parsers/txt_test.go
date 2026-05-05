package parsers

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestTXTParser_SupportedExtensions(t *testing.T) {
	p := NewTXTParser()
	exts := p.SupportedExtensions()

	expected := []string{".txt", ".text"}
	if len(exts) != len(expected) {
		t.Fatalf("expected %d extensions, got %d", len(expected), len(exts))
	}

	for i, ext := range expected {
		if exts[i] != ext {
			t.Errorf("expected extension %q at index %d, got %q", ext, i, exts[i])
		}
	}
}

func TestTXTParser_ParseSimple(t *testing.T) {
	p := NewTXTParser()
	ctx := context.Background()

	input := "Hello, World!\nThis is a test."
	r := strings.NewReader(input)

	text, meta, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// NormalizeText converts to lowercase and collapses whitespace
	expected := "hello, world! this is a test."
	if text != expected {
		t.Errorf("expected %q, got %q", expected, text)
	}

	if meta != nil {
		t.Errorf("expected nil metadata, got %v", meta)
	}
}

func TestTXTParser_ParseUTF8(t *testing.T) {
	p := NewTXTParser()
	ctx := context.Background()

	// Test various UTF-8 characters
	input := "Bonjour le monde! Salut tout le monde."
	r := strings.NewReader(input)

	text, _, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "bonjour le monde! salut tout le monde."
	if text != expected {
		t.Errorf("expected %q, got %q", expected, text)
	}
}

func TestTXTParser_ParseUTF8WithAccents(t *testing.T) {
	p := NewTXTParser()
	ctx := context.Background()

	// Test UTF-8 with accented characters
	input := "Les caracteres: e, a, u, c"
	r := strings.NewReader(input)

	text, _, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "les caracteres: e, a, u, c"
	if text != expected {
		t.Errorf("expected %q, got %q", expected, text)
	}
}

func TestTXTParser_ParseEmpty(t *testing.T) {
	p := NewTXTParser()
	ctx := context.Background()

	r := strings.NewReader("")

	text, meta, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if text != "" {
		t.Errorf("expected empty string, got %q", text)
	}

	if meta != nil {
		t.Errorf("expected nil metadata, got %v", meta)
	}
}

func TestTXTParser_ParseContextCancelled(t *testing.T) {
	p := NewTXTParser()

	// Create an already cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := strings.NewReader("This should not be read")

	_, _, err := p.Parse(ctx, r)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err != context.Canceled {
		t.Errorf("expected context.Canceled error, got %v", err)
	}
}

func TestTXTParser_ParseContextTimeout(t *testing.T) {
	p := NewTXTParser()

	// Create a context that times out immediately
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Wait for timeout
	time.Sleep(10 * time.Millisecond)

	r := strings.NewReader("This should not be read")

	_, _, err := p.Parse(ctx, r)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded error, got %v", err)
	}
}

func TestTXTParser_ParseWhitespaceNormalization(t *testing.T) {
	p := NewTXTParser()
	ctx := context.Background()

	// Test multiple spaces and tabs
	input := "Hello    World\t\tTest\n\nNew paragraph"
	r := strings.NewReader(input)

	text, _, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// NormalizeText collapses multiple whitespace into single space
	expected := "hello world test new paragraph"
	if text != expected {
		t.Errorf("expected %q, got %q", expected, text)
	}
}

func TestTXTParser_ParseControlCharacters(t *testing.T) {
	p := NewTXTParser()
	ctx := context.Background()

	// Test control characters (should be removed)
	input := "Hello\x00World\x01Test"
	r := strings.NewReader(input)

	text, _, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "helloworldtest"
	if text != expected {
		t.Errorf("expected %q, got %q", expected, text)
	}
}
