// Package parsers provides parser implementations for various document formats.
package parsers

import (
	"context"
	"io"

	"github.com/hygur/sidecar/internal/ingest"
)

// TXTParser handles plain text files.
type TXTParser struct{}

// NewTXTParser creates a new TXT parser.
func NewTXTParser() *TXTParser {
	return &TXTParser{}
}

// SupportedExtensions returns the file extensions this parser can handle.
func (p *TXTParser) SupportedExtensions() []string {
	return []string{".txt", ".text"}
}

// Parse extracts text content from a plain text file.
// It normalizes the content using ingest.NormalizeText().
func (p *TXTParser) Parse(ctx context.Context, r io.Reader) (string, ingest.Metadata, error) {
	// Check for context cancellation before reading
	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	default:
	}

	content, err := io.ReadAll(r)
	if err != nil {
		return "", nil, err
	}

	// Check for context cancellation after reading
	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	default:
	}

	// Normalize UTF-8 text
	normalized := ingest.NormalizeText(string(content))

	return normalized, nil, nil
}
