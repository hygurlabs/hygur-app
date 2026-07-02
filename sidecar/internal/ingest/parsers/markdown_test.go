package parsers

import (
	"context"
	"strings"
	"testing"
)

func TestMarkdownParser_SupportedExtensions(t *testing.T) {
	p := NewMarkdownParser()
	exts := p.SupportedExtensions()

	expected := []string{".md", ".markdown"}
	if len(exts) != len(expected) {
		t.Fatalf("expected %d extensions, got %d", len(expected), len(exts))
	}

	for i, ext := range expected {
		if exts[i] != ext {
			t.Errorf("expected extension %q at index %d, got %q", ext, i, exts[i])
		}
	}
}

func TestMarkdownParser_ParseSimple(t *testing.T) {
	p := NewMarkdownParser()
	ctx := context.Background()

	input := `# Hello World

This is a paragraph.

Another paragraph here.`

	r := strings.NewReader(input)

	text, meta, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain the text content (raw output: case preserved)
	if !strings.Contains(text, "Hello World") {
		t.Errorf("expected text to contain 'Hello World', got %q", text)
	}
	if !strings.Contains(text, "This is a paragraph") {
		t.Errorf("expected text to contain 'This is a paragraph', got %q", text)
	}

	// Should have section_count
	if meta == nil {
		t.Fatal("expected metadata, got nil")
	}
	if count, ok := meta["section_count"]; !ok || count != 1 {
		t.Errorf("expected section_count=1, got %v", count)
	}
}

func TestMarkdownParser_ParseFrontmatter(t *testing.T) {
	p := NewMarkdownParser()
	ctx := context.Background()

	input := `---
title: My Document
author: John Doe
date: 2024-01-15
---

# Introduction

This is the content.`

	r := strings.NewReader(input)

	text, meta, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check metadata extraction
	if meta == nil {
		t.Fatal("expected metadata, got nil")
	}

	if title, ok := meta["title"]; !ok || title != "My Document" {
		t.Errorf("expected title='My Document', got %v", title)
	}
	if author, ok := meta["author"]; !ok || author != "John Doe" {
		t.Errorf("expected author='John Doe', got %v", author)
	}
	// Note: YAML parses dates as time.Time, so we just check the field exists
	if _, ok := meta["date"]; !ok {
		t.Errorf("expected date field to exist in metadata")
	}

	// Frontmatter should not appear in text
	if strings.Contains(text, "title:") {
		t.Errorf("frontmatter should not appear in text: %q", text)
	}

	// Content should be present
	if !strings.Contains(text, "Introduction") {
		t.Errorf("expected text to contain 'Introduction', got %q", text)
	}
	if !strings.Contains(text, "This is the content") {
		t.Errorf("expected text to contain 'This is the content', got %q", text)
	}
}

func TestMarkdownParser_ParseMultipleHeadings(t *testing.T) {
	p := NewMarkdownParser()
	ctx := context.Background()

	input := `# Chapter 1

First chapter content.

## Section 1.1

Sub-section content.

# Chapter 2

Second chapter content.

## Section 2.1

More content.

### Subsection 2.1.1

Deep content.`

	r := strings.NewReader(input)

	text, meta, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have section_count = 5
	if meta == nil {
		t.Fatal("expected metadata, got nil")
	}
	if count, ok := meta["section_count"]; !ok || count != 5 {
		t.Errorf("expected section_count=5, got %v", count)
	}

	// All headings should be in text
	expectedTexts := []string{"Chapter 1", "Section 1.1", "Chapter 2", "Section 2.1", "Subsection 2.1.1"}
	for _, expected := range expectedTexts {
		if !strings.Contains(text, expected) {
			t.Errorf("expected text to contain %q, got %q", expected, text)
		}
	}
}

func TestMarkdownParser_ParseCodeBlocksIncluded(t *testing.T) {
	p := NewMarkdownParser()
	ctx := context.Background()

	input := "# Code Example\n\nHere is some code:\n\n```go\nfunc main() {\n\tfmt.Println(\"Hello\")\n}\n```\n\nEnd of example."

	r := strings.NewReader(input)

	text, _, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Code content SHOULD be in text (important for RAG on notes with code)
	// Note: the parser returns raw text; case is preserved.
	if !strings.Contains(text, "func main") {
		t.Errorf("code block content should appear in text: %q", text)
	}
	if !strings.Contains(text, "Println") {
		t.Errorf("code block content should appear in text: %q", text)
	}

	// Surrounding text should be present
	if !strings.Contains(text, "Code Example") {
		t.Errorf("expected text to contain 'Code Example', got %q", text)
	}
	if !strings.Contains(text, "Here is some code") {
		t.Errorf("expected text to contain 'Here is some code', got %q", text)
	}
	if !strings.Contains(text, "End of example") {
		t.Errorf("expected text to contain 'End of example', got %q", text)
	}
}

func TestMarkdownParser_ParseInlineCodeIncluded(t *testing.T) {
	p := NewMarkdownParser()
	ctx := context.Background()

	input := "Use the `fmt.Println` function to print."

	r := strings.NewReader(input)

	text, _, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Inline code SHOULD be in text (important for RAG on notes with code)
	// Note: the parser returns raw text; case is preserved.
	if !strings.Contains(text, "fmt.Println") {
		t.Errorf("inline code should appear in text: %q", text)
	}

	// Surrounding text should be present
	if !strings.Contains(text, "Use the") {
		t.Errorf("expected text to contain 'Use the', got %q", text)
	}
	if !strings.Contains(text, "function to print") {
		t.Errorf("expected text to contain 'function to print', got %q", text)
	}
}

func TestMarkdownParser_ParseLinksTextOnly(t *testing.T) {
	p := NewMarkdownParser()
	ctx := context.Background()

	input := "Check out [this link](https://example.com) for more info."

	r := strings.NewReader(input)

	text, _, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Link text should be present
	if !strings.Contains(text, "this link") {
		t.Errorf("expected text to contain 'this link', got %q", text)
	}

	// URL should NOT be in text
	if strings.Contains(text, "https://") || strings.Contains(text, "example.com") {
		t.Errorf("URL should not appear in text: %q", text)
	}
}

func TestMarkdownParser_ParseImagesTextOnly(t *testing.T) {
	p := NewMarkdownParser()
	ctx := context.Background()

	input := "Here is an image: ![Alt text for image](image.png)"

	r := strings.NewReader(input)

	text, _, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Alt text should be present
	if !strings.Contains(text, "Alt text for image") {
		t.Errorf("expected text to contain 'Alt text for image', got %q", text)
	}

	// Image path should NOT be in text
	if strings.Contains(text, "image.png") {
		t.Errorf("image path should not appear in text: %q", text)
	}
}

func TestMarkdownParser_ParseEmpty(t *testing.T) {
	p := NewMarkdownParser()
	ctx := context.Background()

	r := strings.NewReader("")

	text, meta, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if text != "" {
		t.Errorf("expected empty string, got %q", text)
	}

	// Should still have section_count
	if meta == nil {
		t.Fatal("expected metadata, got nil")
	}
	if count, ok := meta["section_count"]; !ok || count != 0 {
		t.Errorf("expected section_count=0, got %v", count)
	}
}

func TestMarkdownParser_ParseContextCancelled(t *testing.T) {
	p := NewMarkdownParser()

	// Create an already cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := strings.NewReader("# Test")

	_, _, err := p.Parse(ctx, r)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err != context.Canceled {
		t.Errorf("expected context.Canceled error, got %v", err)
	}
}

func TestMarkdownParser_ParseInvalidFrontmatter(t *testing.T) {
	p := NewMarkdownParser()
	ctx := context.Background()

	// Invalid YAML frontmatter
	input := `---
title: [invalid yaml
---

# Content`

	r := strings.NewReader(input)

	text, meta, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still parse content, treating invalid frontmatter as content
	if !strings.Contains(text, "Content") {
		t.Errorf("expected text to contain 'Content', got %q", text)
	}

	// Metadata should only have section_count
	if meta == nil {
		t.Fatal("expected metadata, got nil")
	}
	if _, ok := meta["title"]; ok {
		t.Errorf("should not have title from invalid frontmatter")
	}
}

func TestMarkdownParser_ParseListItems(t *testing.T) {
	p := NewMarkdownParser()
	ctx := context.Background()

	input := `# Shopping List

- Apples
- Bananas
- Oranges

## Numbered Items

1. First item
2. Second item
3. Third item`

	r := strings.NewReader(input)

	text, _, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// List items should be present
	expectedItems := []string{"Apples", "Bananas", "Oranges", "First item", "Second item", "Third item"}
	for _, item := range expectedItems {
		if !strings.Contains(text, item) {
			t.Errorf("expected text to contain %q, got %q", item, text)
		}
	}
}

func TestMarkdownParser_ParseBlockquotes(t *testing.T) {
	p := NewMarkdownParser()
	ctx := context.Background()

	input := `# Quote Section

> This is a blockquote.
> It spans multiple lines.

Regular paragraph after.`

	r := strings.NewReader(input)

	text, _, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Blockquote content should be present
	if !strings.Contains(text, "This is a blockquote") {
		t.Errorf("expected text to contain 'This is a blockquote', got %q", text)
	}
	if !strings.Contains(text, "spans multiple lines") {
		t.Errorf("expected text to contain 'spans multiple lines', got %q", text)
	}
}

func TestMarkdownParser_ParseBoldAndItalic(t *testing.T) {
	p := NewMarkdownParser()
	ctx := context.Background()

	input := "This is **bold** and this is *italic* text."

	r := strings.NewReader(input)

	text, _, err := p.Parse(ctx, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Text should be present without formatting markers
	if !strings.Contains(text, "bold") {
		t.Errorf("expected text to contain 'bold', got %q", text)
	}
	if !strings.Contains(text, "italic") {
		t.Errorf("expected text to contain 'italic', got %q", text)
	}
	// Markdown markers should not be present
	if strings.Contains(text, "**") || strings.Contains(text, "*") {
		t.Errorf("markdown markers should not appear in text: %q", text)
	}
}
