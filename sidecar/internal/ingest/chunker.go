package ingest

import (
	"strings"

	"github.com/google/uuid"
)

// ChunkerConfig configures the chunking behavior.
type ChunkerConfig struct {
	// MaxTokens is the maximum number of tokens per chunk.
	// Default: 1024
	MaxTokens int

	// Overlap is the number of overlapping tokens between adjacent chunks.
	// Default: 128
	Overlap int
}

// ChunkMetadata contains metadata about a chunk.
type ChunkMetadata struct {
	// SourceID is the identifier of the source document (filled by caller).
	SourceID string

	// Position is the index of this chunk (0, 1, 2...).
	Position int

	// HeadingPath is the path of headings for Markdown documents.
	HeadingPath []string

	// Type is the chunk type: "section", "paragraph", or "sentence".
	Type string
}

// Chunk represents a piece of a document.
type Chunk struct {
	// ID is the unique identifier of this chunk (UUID).
	ID string

	// Text is the content of this chunk.
	Text string

	// StartOffset is the character offset in the original document.
	StartOffset int

	// EndOffset is the ending character offset in the original document.
	EndOffset int

	// Metadata contains additional information about this chunk.
	Metadata ChunkMetadata
}

// Chunker splits text into overlapping chunks.
type Chunker struct {
	config ChunkerConfig
}

// tokensPerChar is the approximation ratio: 1 token ~ 4 characters.
const tokensPerChar = 4

// NewChunker creates a new Chunker with the given configuration.
func NewChunker(config ChunkerConfig) *Chunker {
	if config.MaxTokens <= 0 {
		config.MaxTokens = 1024
	}
	if config.Overlap < 0 {
		config.Overlap = 0
	}
	if config.Overlap >= config.MaxTokens {
		config.Overlap = config.MaxTokens / 8
	}
	return &Chunker{config: config}
}

// DefaultChunker creates a Chunker with default configuration.
// MaxTokens=1024, Overlap=128
func DefaultChunker() *Chunker {
	return NewChunker(ChunkerConfig{
		MaxTokens: 1024,
		Overlap:   128,
	})
}

// estimateTokens estimates the number of tokens in a text.
// Approximation: 1 token ~ 4 characters.
func estimateTokens(text string) int {
	return (len(text) + tokensPerChar - 1) / tokensPerChar
}

// tokensToChars converts a token count to character count.
func tokensToChars(tokens int) int {
	return tokens * tokensPerChar
}

// Chunk splits text into overlapping chunks.
func (c *Chunker) Chunk(text string) ([]Chunk, error) {
	return c.ChunkWithMetadata(text, nil)
}

// ChunkWithMetadata splits text into overlapping chunks with heading context.
func (c *Chunker) ChunkWithMetadata(text string, headings []string) ([]Chunk, error) {
	if text == "" {
		return nil, nil
	}

	maxChars := tokensToChars(c.config.MaxTokens)
	overlapChars := tokensToChars(c.config.Overlap)

	// If text fits in one chunk, return it directly
	if len(text) <= maxChars {
		return []Chunk{
			{
				ID:          uuid.New().String(),
				Text:        text,
				StartOffset: 0,
				EndOffset:   len(text),
				Metadata: ChunkMetadata{
					Position:    0,
					HeadingPath: headings,
					Type:        determineChunkType(text),
				},
			},
		}, nil
	}

	// Split text into segments respecting natural boundaries
	segments := c.splitIntoSegments(text, maxChars)

	// Build chunks with overlap
	return c.buildChunksWithOverlap(text, segments, headings, maxChars, overlapChars), nil
}

// segment represents a piece of text with its position.
type segment struct {
	text  string
	start int
	end   int
}

// splitIntoSegments splits text into segments respecting natural boundaries.
func (c *Chunker) splitIntoSegments(text string, maxChars int) []segment {
	// First, split by paragraphs
	paragraphs := splitByParagraphs(text)

	var segments []segment
	for _, para := range paragraphs {
		if len(para.text) <= maxChars {
			segments = append(segments, para)
		} else {
			// Paragraph too large, split by sentences
			sentences := splitBySentences(para.text, para.start)
			for _, sent := range sentences {
				if len(sent.text) <= maxChars {
					segments = append(segments, sent)
				} else {
					// Sentence too large, split by words
					words := splitByWords(sent.text, sent.start, maxChars)
					segments = append(segments, words...)
				}
			}
		}
	}

	return segments
}

// splitByParagraphs splits text by double newlines.
func splitByParagraphs(text string) []segment {
	var segments []segment
	parts := strings.Split(text, "\n\n")

	offset := 0
	for i, part := range parts {
		if part == "" {
			if i < len(parts)-1 {
				offset += 2 // account for \n\n
			}
			continue
		}

		segments = append(segments, segment{
			text:  part,
			start: offset,
			end:   offset + len(part),
		})

		offset += len(part)
		if i < len(parts)-1 {
			offset += 2 // account for \n\n separator
		}
	}

	return segments
}

// splitBySentences splits text by sentence boundaries (". ").
func splitBySentences(text string, baseOffset int) []segment {
	var segments []segment

	// Split by common sentence endings
	delimiters := []string{". ", "! ", "? ", ".\n", "!\n", "?\n"}

	remaining := text
	offset := 0

	for len(remaining) > 0 {
		// Find the earliest delimiter
		minIdx := -1
		minDelim := ""
		for _, delim := range delimiters {
			idx := strings.Index(remaining, delim)
			if idx >= 0 && (minIdx < 0 || idx < minIdx) {
				minIdx = idx
				minDelim = delim
			}
		}

		if minIdx < 0 {
			// No more delimiters, add remaining text
			if remaining != "" {
				segments = append(segments, segment{
					text:  remaining,
					start: baseOffset + offset,
					end:   baseOffset + offset + len(remaining),
				})
			}
			break
		}

		// Include the delimiter (except the space/newline)
		sentenceEnd := minIdx + 1 // include the punctuation
		sentence := remaining[:sentenceEnd]

		if sentence != "" {
			segments = append(segments, segment{
				text:  sentence,
				start: baseOffset + offset,
				end:   baseOffset + offset + len(sentence),
			})
		}

		offset += minIdx + len(minDelim)
		remaining = remaining[minIdx+len(minDelim):]
	}

	return segments
}

// splitByWords splits text by words, ensuring no segment exceeds maxChars.
func splitByWords(text string, baseOffset int, maxChars int) []segment {
	var segments []segment

	words := strings.Fields(text)
	if len(words) == 0 {
		return segments
	}

	// Reconstruct with proper spacing by tracking position in original text
	currentText := ""
	currentStart := baseOffset

	// Find actual position of first word
	firstWordIdx := strings.Index(text, words[0])
	if firstWordIdx > 0 {
		currentStart = baseOffset + firstWordIdx
	}

	for i, word := range words {
		if currentText == "" {
			// Check if single word exceeds max
			if len(word) > maxChars {
				// Force split the word
				for j := 0; j < len(word); j += maxChars {
					end := j + maxChars
					if end > len(word) {
						end = len(word)
					}
					segments = append(segments, segment{
						text:  word[j:end],
						start: currentStart + j,
						end:   currentStart + end,
					})
				}
				// Update for next word
				if i < len(words)-1 {
					// Find next word in remaining text
					remaining := text[strings.Index(text, word)+len(word):]
					nextIdx := strings.Index(remaining, words[i+1])
					currentStart = baseOffset + len(text) - len(remaining) + nextIdx
				}
			} else {
				currentText = word
			}
		} else {
			testText := currentText + " " + word
			if len(testText) > maxChars {
				// Save current segment
				segments = append(segments, segment{
					text:  currentText,
					start: currentStart,
					end:   currentStart + len(currentText),
				})
				currentStart = currentStart + len(currentText) + 1 // +1 for space
				currentText = word
			} else {
				currentText = testText
			}
		}
	}

	// Add remaining text
	if currentText != "" {
		segments = append(segments, segment{
			text:  currentText,
			start: currentStart,
			end:   currentStart + len(currentText),
		})
	}

	return segments
}

// buildChunksWithOverlap combines segments into chunks with overlap.
func (c *Chunker) buildChunksWithOverlap(originalText string, segments []segment, headings []string, maxChars, overlapChars int) []Chunk {
	if len(segments) == 0 {
		return nil
	}

	var chunks []Chunk
	position := 0

	currentText := ""
	currentStart := segments[0].start
	segmentIdx := 0

	for segmentIdx < len(segments) {
		seg := segments[segmentIdx]

		// Try to add segment to current chunk
		var testText string
		if currentText == "" {
			testText = seg.text
		} else {
			// Add appropriate separator
			testText = currentText + " " + seg.text
		}

		if len(testText) <= maxChars {
			// Segment fits, add it
			currentText = testText
			if currentText == seg.text {
				currentStart = seg.start
			}
			segmentIdx++
		} else {
			// Segment doesn't fit, finalize current chunk
			if currentText != "" {
				currentEnd := currentStart + len(currentText)
				// Adjust end offset to not exceed original text
				if currentEnd > len(originalText) {
					currentEnd = len(originalText)
				}

				chunks = append(chunks, Chunk{
					ID:          uuid.New().String(),
					Text:        currentText,
					StartOffset: currentStart,
					EndOffset:   currentEnd,
					Metadata: ChunkMetadata{
						Position:    position,
						HeadingPath: headings,
						Type:        determineChunkType(currentText),
					},
				})
				position++

				// Calculate overlap start
				overlapStart := calculateOverlapStart(currentText, overlapChars)
				if overlapStart > 0 && overlapStart < len(currentText) {
					currentText = currentText[overlapStart:]
					currentStart = currentStart + overlapStart
				} else {
					currentText = ""
					currentStart = seg.start
				}
			} else {
				// Current text is empty but segment doesn't fit
				// This means the segment itself is larger than maxChars
				// Force add it (this shouldn't happen if splitByWords works correctly)
				chunks = append(chunks, Chunk{
					ID:          uuid.New().String(),
					Text:        seg.text[:min(len(seg.text), maxChars)],
					StartOffset: seg.start,
					EndOffset:   seg.start + min(len(seg.text), maxChars),
					Metadata: ChunkMetadata{
						Position:    position,
						HeadingPath: headings,
						Type:        determineChunkType(seg.text),
					},
				})
				position++
				segmentIdx++
				currentText = ""
				if segmentIdx < len(segments) {
					currentStart = segments[segmentIdx].start
				}
			}
		}
	}

	// Add final chunk if there's remaining text
	if currentText != "" {
		currentEnd := currentStart + len(currentText)
		if currentEnd > len(originalText) {
			currentEnd = len(originalText)
		}

		chunks = append(chunks, Chunk{
			ID:          uuid.New().String(),
			Text:        currentText,
			StartOffset: currentStart,
			EndOffset:   currentEnd,
			Metadata: ChunkMetadata{
				Position:    position,
				HeadingPath: headings,
				Type:        determineChunkType(currentText),
			},
		})
	}

	return chunks
}

// calculateOverlapStart finds the position to start overlap from.
// Returns the character offset from the start of text where overlap should begin.
func calculateOverlapStart(text string, overlapChars int) int {
	if overlapChars <= 0 || len(text) <= overlapChars {
		return 0
	}

	// Target position: len(text) - overlapChars
	targetPos := len(text) - overlapChars

	// Try to find a word boundary near the target position
	// Look for a space before the target position
	for i := targetPos; i >= 0 && i > targetPos-50; i-- {
		if text[i] == ' ' || text[i] == '\n' {
			return i + 1
		}
	}

	// No word boundary found, use exact position
	return targetPos
}

// determineChunkType determines the type of chunk based on its content.
func determineChunkType(text string) string {
	// Check if it looks like a section (has heading markers)
	if strings.HasPrefix(text, "#") || strings.Contains(text, "\n#") {
		return "section"
	}

	// Check if it's a single paragraph (no double newlines)
	if !strings.Contains(text, "\n\n") {
		// Check if it's sentence-like (short, ends with punctuation)
		trimmed := strings.TrimSpace(text)
		// A sentence is typically short (less than 200 chars) and ends with punctuation
		if len(trimmed) < 200 && (strings.HasSuffix(trimmed, ".") || strings.HasSuffix(trimmed, "!") || strings.HasSuffix(trimmed, "?")) {
			return "sentence"
		}
		return "paragraph"
	}

	return "section"
}

// min returns the minimum of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// NOTE: the legacy character-based ChunkText/ChunkOptions/LegacyChunk API was
// removed during the RAG rebuild. All ingestion now goes through BuildSections
// (structural sections) → the token-budget Chunker above. Nothing should
// reintroduce a fixed-size character chunker.
