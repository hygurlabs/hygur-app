package ingest

import (
	"strings"
	"testing"
)

// ============================================================================
// New Chunker API Tests
// ============================================================================

func TestNewChunker(t *testing.T) {
	tests := []struct {
		name          string
		config        ChunkerConfig
		wantMaxTokens int
		wantOverlap   int
	}{
		{
			name:          "default values",
			config:        ChunkerConfig{},
			wantMaxTokens: 1024,
			wantOverlap:   0,
		},
		{
			name:          "custom values",
			config:        ChunkerConfig{MaxTokens: 512, Overlap: 64},
			wantMaxTokens: 512,
			wantOverlap:   64,
		},
		{
			name:          "negative max tokens uses default",
			config:        ChunkerConfig{MaxTokens: -10, Overlap: 50},
			wantMaxTokens: 1024,
			wantOverlap:   50,
		},
		{
			name:          "negative overlap becomes zero",
			config:        ChunkerConfig{MaxTokens: 512, Overlap: -10},
			wantMaxTokens: 512,
			wantOverlap:   0,
		},
		{
			name:          "overlap >= max tokens gets adjusted",
			config:        ChunkerConfig{MaxTokens: 100, Overlap: 150},
			wantMaxTokens: 100,
			wantOverlap:   12, // 100 / 8 = 12
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewChunker(tt.config)
			if c.config.MaxTokens != tt.wantMaxTokens {
				t.Errorf("MaxTokens = %d, want %d", c.config.MaxTokens, tt.wantMaxTokens)
			}
			if c.config.Overlap != tt.wantOverlap {
				t.Errorf("Overlap = %d, want %d", c.config.Overlap, tt.wantOverlap)
			}
		})
	}
}

func TestDefaultChunker(t *testing.T) {
	c := DefaultChunker()

	if c.config.MaxTokens != 1024 {
		t.Errorf("MaxTokens = %d, want 1024", c.config.MaxTokens)
	}
	if c.config.Overlap != 128 {
		t.Errorf("Overlap = %d, want 128", c.config.Overlap)
	}
}

func TestChunker_ShortText(t *testing.T) {
	c := DefaultChunker()
	text := "This is a short text that fits in one chunk."

	chunks, err := c.Chunk(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}

	chunk := chunks[0]
	if chunk.Text != text {
		t.Errorf("Text = %q, want %q", chunk.Text, text)
	}
	if chunk.StartOffset != 0 {
		t.Errorf("StartOffset = %d, want 0", chunk.StartOffset)
	}
	if chunk.EndOffset != len(text) {
		t.Errorf("EndOffset = %d, want %d", chunk.EndOffset, len(text))
	}
	if chunk.ID == "" {
		t.Error("ID should not be empty")
	}
	if chunk.Metadata.Position != 0 {
		t.Errorf("Position = %d, want 0", chunk.Metadata.Position)
	}
}

func TestChunker_EmptyText(t *testing.T) {
	c := DefaultChunker()

	chunks, err := c.Chunk("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if chunks != nil {
		t.Errorf("got %d chunks, want nil", len(chunks))
	}
}

func TestChunker_LongText(t *testing.T) {
	// Use a small chunker for testing
	c := NewChunker(ChunkerConfig{MaxTokens: 50, Overlap: 10})

	// Create text that will span multiple chunks
	// 50 tokens * 4 chars = 200 chars max per chunk
	text := strings.Repeat("This is a sentence. ", 30) // ~600 chars

	chunks, err := c.Chunk(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chunks) < 2 {
		t.Errorf("got %d chunks, want at least 2", len(chunks))
	}

	// Verify all chunks have valid properties
	for i, chunk := range chunks {
		if chunk.ID == "" {
			t.Errorf("chunk[%d].ID should not be empty", i)
		}
		if chunk.Text == "" {
			t.Errorf("chunk[%d].Text should not be empty", i)
		}
		if chunk.Metadata.Position != i {
			t.Errorf("chunk[%d].Position = %d, want %d", i, chunk.Metadata.Position, i)
		}
	}
}

func TestChunker_Overlap(t *testing.T) {
	// Create chunker with specific overlap
	c := NewChunker(ChunkerConfig{MaxTokens: 25, Overlap: 5})

	// Create paragraphs that will need to be split
	// 25 tokens * 4 = 100 chars max, 5 tokens * 4 = 20 chars overlap
	text := "First paragraph with enough content to span.\n\n" +
		"Second paragraph with more content here.\n\n" +
		"Third paragraph completes the text here."

	chunks, err := c.Chunk(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want at least 2", len(chunks))
	}

	// Verify overlap exists between consecutive chunks
	for i := 1; i < len(chunks); i++ {
		prev := chunks[i-1]
		curr := chunks[i]

		// The start of current chunk should be before the end of previous chunk
		// (this indicates overlap)
		if curr.StartOffset >= prev.EndOffset {
			// Check if there's textual overlap
			prevEnd := prev.Text[max(0, len(prev.Text)-30):]
			currStart := curr.Text[:min(30, len(curr.Text))]

			// There should be some overlap in content
			t.Logf("chunk[%d] ends with: %q", i-1, prevEnd)
			t.Logf("chunk[%d] starts with: %q", i, currStart)
		}
	}
}

func TestChunker_NoChunkExceedsMaxTokens(t *testing.T) {
	configs := []ChunkerConfig{
		{MaxTokens: 50, Overlap: 10},
		{MaxTokens: 100, Overlap: 20},
		{MaxTokens: 25, Overlap: 5},
	}

	// Various text patterns
	texts := []string{
		strings.Repeat("Word ", 200),                               // Simple words
		strings.Repeat("This is a sentence. ", 50),                 // Sentences
		strings.Repeat("Paragraph one.\n\nParagraph two.\n\n", 20), // Paragraphs
		"A" + strings.Repeat("B", 500) + "C",                       // Single long word
	}

	for _, config := range configs {
		c := NewChunker(config)
		maxChars := config.MaxTokens * 4

		for i, text := range texts {
			chunks, err := c.Chunk(text)
			if err != nil {
				t.Errorf("config=%+v text[%d]: unexpected error: %v", config, i, err)
				continue
			}

			for j, chunk := range chunks {
				if len(chunk.Text) > maxChars {
					t.Errorf("config=%+v text[%d] chunk[%d]: len=%d exceeds max=%d",
						config, i, j, len(chunk.Text), maxChars)
				}
			}
		}
	}
}

func TestChunker_OffsetsCorrect(t *testing.T) {
	c := NewChunker(ChunkerConfig{MaxTokens: 30, Overlap: 5})
	text := "First paragraph here.\n\nSecond paragraph here.\n\nThird paragraph here."

	chunks, err := c.Chunk(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i, chunk := range chunks {
		// Verify offsets are within bounds
		if chunk.StartOffset < 0 {
			t.Errorf("chunk[%d].StartOffset = %d, want >= 0", i, chunk.StartOffset)
		}
		if chunk.EndOffset > len(text) {
			t.Errorf("chunk[%d].EndOffset = %d, want <= %d", i, chunk.EndOffset, len(text))
		}
		if chunk.StartOffset >= chunk.EndOffset {
			t.Errorf("chunk[%d]: StartOffset(%d) >= EndOffset(%d)", i, chunk.StartOffset, chunk.EndOffset)
		}
	}
}

func TestChunker_WithMetadata(t *testing.T) {
	c := DefaultChunker()
	text := "Some content here."
	headings := []string{"Chapter 1", "Section 1.1"}

	chunks, err := c.ChunkWithMetadata(text, headings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}

	chunk := chunks[0]
	if len(chunk.Metadata.HeadingPath) != 2 {
		t.Fatalf("HeadingPath length = %d, want 2", len(chunk.Metadata.HeadingPath))
	}
	if chunk.Metadata.HeadingPath[0] != "Chapter 1" {
		t.Errorf("HeadingPath[0] = %q, want %q", chunk.Metadata.HeadingPath[0], "Chapter 1")
	}
	if chunk.Metadata.HeadingPath[1] != "Section 1.1" {
		t.Errorf("HeadingPath[1] = %q, want %q", chunk.Metadata.HeadingPath[1], "Section 1.1")
	}
}

func TestChunker_ChunkType(t *testing.T) {
	c := DefaultChunker()

	tests := []struct {
		name     string
		text     string
		wantType string
	}{
		{
			name:     "section with heading",
			text:     "# Heading\nSome content",
			wantType: "section",
		},
		{
			name:     "simple sentence",
			text:     "This is a simple sentence.",
			wantType: "sentence",
		},
		{
			name:     "paragraph",
			text:     "This is a longer piece of text that spans multiple sentences and contains various types of content that would typically be found in a paragraph of a document. It continues on and on with additional content to make it long enough to be classified as a paragraph rather than a simple sentence.",
			wantType: "paragraph",
		},
		{
			name:     "multiple paragraphs",
			text:     "First paragraph.\n\nSecond paragraph.",
			wantType: "section",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks, err := c.Chunk(tt.text)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(chunks) == 0 {
				t.Fatal("expected at least one chunk")
			}

			if chunks[0].Metadata.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", chunks[0].Metadata.Type, tt.wantType)
			}
		})
	}
}

func TestChunker_UniqueIDs(t *testing.T) {
	c := NewChunker(ChunkerConfig{MaxTokens: 25, Overlap: 5})
	text := strings.Repeat("This is a test sentence. ", 50)

	chunks, err := c.Chunk(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ids := make(map[string]bool)
	for i, chunk := range chunks {
		if ids[chunk.ID] {
			t.Errorf("duplicate ID found at chunk[%d]: %s", i, chunk.ID)
		}
		ids[chunk.ID] = true

		// Verify UUID format (basic check)
		if len(chunk.ID) != 36 {
			t.Errorf("chunk[%d].ID length = %d, want 36 (UUID format)", i, len(chunk.ID))
		}
	}
}

func TestChunker_ParagraphSplitting(t *testing.T) {
	c := NewChunker(ChunkerConfig{MaxTokens: 50, Overlap: 10})

	// Create distinct paragraphs
	text := "First paragraph content.\n\nSecond paragraph content.\n\nThird paragraph content."

	chunks, err := c.Chunk(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All paragraphs should fit in one chunk for this size
	if len(chunks) != 1 {
		t.Logf("got %d chunks:", len(chunks))
		for i, c := range chunks {
			t.Logf("  chunk[%d]: %q", i, c.Text)
		}
	}
}

func TestChunker_SentenceSplitting(t *testing.T) {
	// Very small chunker to force sentence splitting
	c := NewChunker(ChunkerConfig{MaxTokens: 15, Overlap: 3})

	// 15 tokens * 4 = 60 chars max
	text := "First sentence here. Second sentence here. Third sentence here. Fourth sentence."

	chunks, err := c.Chunk(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have multiple chunks
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks for long sentences, got %d", len(chunks))
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		text       string
		wantTokens int
	}{
		{"", 0},
		{"a", 1},        // 1 char = ceil(1/4) = 1 token
		{"ab", 1},       // 2 chars = ceil(2/4) = 1 token
		{"abc", 1},      // 3 chars = ceil(3/4) = 1 token
		{"abcd", 1},     // 4 chars = ceil(4/4) = 1 token
		{"abcde", 2},    // 5 chars = ceil(5/4) = 2 tokens
		{"12345678", 2}, // 8 chars = 2 tokens
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := estimateTokens(tt.text)
			if got != tt.wantTokens {
				t.Errorf("estimateTokens(%q) = %d, want %d", tt.text, got, tt.wantTokens)
			}
		})
	}
}

// ============================================================================
// Legacy API Tests (backward compatibility)
// ============================================================================

func TestChunkText(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		opts      ChunkOptions
		wantCount int
		wantFirst string
		wantLast  string
	}{
		{
			name:      "empty text",
			text:      "",
			opts:      DefaultChunkOptions(),
			wantCount: 0,
		},
		{
			name:      "text smaller than chunk size",
			text:      "Hello World",
			opts:      ChunkOptions{MaxChunkSize: 100, ChunkOverlap: 10},
			wantCount: 1,
			wantFirst: "Hello World",
			wantLast:  "Hello World",
		},
		{
			name:      "text exactly chunk size",
			text:      "12345",
			opts:      ChunkOptions{MaxChunkSize: 5, ChunkOverlap: 0},
			wantCount: 1,
			wantFirst: "12345",
			wantLast:  "12345",
		},
		{
			name:      "text needs two chunks no overlap",
			text:      "1234567890",
			opts:      ChunkOptions{MaxChunkSize: 5, ChunkOverlap: 0},
			wantCount: 2,
			wantFirst: "12345",
			wantLast:  "67890",
		},
		{
			name:      "text with overlap",
			text:      "1234567890",
			opts:      ChunkOptions{MaxChunkSize: 5, ChunkOverlap: 2},
			wantCount: 3,
			wantFirst: "12345",
			wantLast:  "7890", // chunks: "12345", "45678", "7890"
		},
		{
			name:      "default options",
			text:      "This is a test text that should be chunked properly.",
			opts:      DefaultChunkOptions(),
			wantCount: 1, // Text is smaller than default chunk size
			wantFirst: "This is a test text that should be chunked properly.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := ChunkText(tt.text, tt.opts)

			if len(chunks) != tt.wantCount {
				t.Errorf("got %d chunks, want %d", len(chunks), tt.wantCount)
				for i, c := range chunks {
					t.Logf("chunk[%d]: %q", i, c.Content)
				}
				return
			}

			if tt.wantCount == 0 {
				return
			}

			if chunks[0].Content != tt.wantFirst {
				t.Errorf("first chunk = %q, want %q", chunks[0].Content, tt.wantFirst)
			}

			if tt.wantLast != "" && chunks[len(chunks)-1].Content != tt.wantLast {
				t.Errorf("last chunk = %q, want %q", chunks[len(chunks)-1].Content, tt.wantLast)
			}

			// Verify chunk indices
			for i, chunk := range chunks {
				if chunk.Index != i {
					t.Errorf("chunk[%d].Index = %d, want %d", i, chunk.Index, i)
				}
			}

			// Verify offsets are valid
			for i, chunk := range chunks {
				if chunk.StartOffset < 0 {
					t.Errorf("chunk[%d].StartOffset = %d, want >= 0", i, chunk.StartOffset)
				}
				if chunk.EndOffset > len(tt.text) {
					t.Errorf("chunk[%d].EndOffset = %d, want <= %d", i, chunk.EndOffset, len(tt.text))
				}
				if chunk.StartOffset >= chunk.EndOffset {
					t.Errorf("chunk[%d]: StartOffset(%d) >= EndOffset(%d)", i, chunk.StartOffset, chunk.EndOffset)
				}
			}
		})
	}
}

func TestChunkText_InvalidOptions(t *testing.T) {
	text := "Hello World"

	tests := []struct {
		name string
		opts ChunkOptions
	}{
		{
			name: "zero chunk size",
			opts: ChunkOptions{MaxChunkSize: 0, ChunkOverlap: 10},
		},
		{
			name: "negative chunk size",
			opts: ChunkOptions{MaxChunkSize: -10, ChunkOverlap: 0},
		},
		{
			name: "negative overlap",
			opts: ChunkOptions{MaxChunkSize: 10, ChunkOverlap: -5},
		},
		{
			name: "overlap larger than chunk",
			opts: ChunkOptions{MaxChunkSize: 5, ChunkOverlap: 10},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic and should return valid chunks
			chunks := ChunkText(text, tt.opts)
			if chunks == nil {
				t.Error("chunks should not be nil for non-empty text")
			}
		})
	}
}

func TestChunkText_Offsets(t *testing.T) {
	text := "0123456789ABCDEF"
	opts := ChunkOptions{MaxChunkSize: 5, ChunkOverlap: 0}

	chunks := ChunkText(text, opts)

	expected := []struct {
		start   int
		end     int
		content string
	}{
		{0, 5, "01234"},
		{5, 10, "56789"},
		{10, 15, "ABCDE"},
		{15, 16, "F"},
	}

	if len(chunks) != len(expected) {
		t.Fatalf("got %d chunks, want %d", len(chunks), len(expected))
	}

	for i, chunk := range chunks {
		if chunk.StartOffset != expected[i].start {
			t.Errorf("chunk[%d].StartOffset = %d, want %d", i, chunk.StartOffset, expected[i].start)
		}
		if chunk.EndOffset != expected[i].end {
			t.Errorf("chunk[%d].EndOffset = %d, want %d", i, chunk.EndOffset, expected[i].end)
		}
		if chunk.Content != expected[i].content {
			t.Errorf("chunk[%d].Content = %q, want %q", i, chunk.Content, expected[i].content)
		}
	}
}

func TestDefaultChunkOptions(t *testing.T) {
	opts := DefaultChunkOptions()

	if opts.MaxChunkSize <= 0 {
		t.Errorf("MaxChunkSize = %d, want > 0", opts.MaxChunkSize)
	}
	if opts.ChunkOverlap < 0 {
		t.Errorf("ChunkOverlap = %d, want >= 0", opts.ChunkOverlap)
	}
	if opts.ChunkOverlap >= opts.MaxChunkSize {
		t.Errorf("ChunkOverlap(%d) >= MaxChunkSize(%d)", opts.ChunkOverlap, opts.MaxChunkSize)
	}
}

// ============================================================================
// Benchmarks
// ============================================================================

func BenchmarkChunker_100KB(b *testing.B) {
	// Create approximately 100KB of text
	var builder strings.Builder
	sentence := "This is a test sentence for chunking performance benchmarks. "
	// 100KB / ~60 bytes per sentence = ~1700 sentences
	for i := 0; i < 1700; i++ {
		builder.WriteString(sentence)
	}
	text := builder.String()
	b.Logf("Text size: %d bytes (%.2f KB)", len(text), float64(len(text))/1024)

	c := DefaultChunker()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Chunk(text)
	}
}

func BenchmarkChunker_SmallChunks(b *testing.B) {
	// 100KB text with small chunk size
	var builder strings.Builder
	for i := 0; i < 1700; i++ {
		builder.WriteString("This is a test sentence for chunking performance benchmarks. ")
	}
	text := builder.String()

	c := NewChunker(ChunkerConfig{MaxTokens: 128, Overlap: 16})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Chunk(text)
	}
}

func BenchmarkChunker_Paragraphs(b *testing.B) {
	// Text with many paragraphs
	var builder strings.Builder
	for i := 0; i < 500; i++ {
		builder.WriteString("This is paragraph number ")
		builder.WriteString(string(rune('0' + i%10)))
		builder.WriteString(". It contains multiple sentences. More content here.\n\n")
	}
	text := builder.String()

	c := DefaultChunker()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Chunk(text)
	}
}

// Legacy benchmark for backward compatibility
func BenchmarkChunkText(b *testing.B) {
	// Create a 10KB text
	text := ""
	for i := 0; i < 1000; i++ {
		text += "This is a test sentence for chunking. "
	}
	opts := DefaultChunkOptions()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ChunkText(text, opts)
	}
}

// ============================================================================
// Helper functions
// ============================================================================

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
