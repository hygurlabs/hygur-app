// Package ingest provides document ingestion capabilities for the Hygur knowledge base.
// It handles parsing, chunking, and normalizing documents for indexing.
package ingest

import (
	"context"
	"io"
)

// Metadata holds arbitrary key-value metadata extracted from a document.
type Metadata map[string]any

// Parser defines the interface for document parsers.
// Each parser implementation handles specific file formats.
type Parser interface {
	// SupportedExtensions returns a list of file extensions this parser can handle.
	// Extensions should be lowercase and include the dot (e.g., ".txt", ".md").
	SupportedExtensions() []string

	// Parse extracts text content and metadata from the given reader.
	// The context should be respected for cancellation and timeouts.
	Parse(ctx context.Context, r io.Reader) (string, Metadata, error)
}
