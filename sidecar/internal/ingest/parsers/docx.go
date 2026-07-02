// Package parsers provides parser implementations for various document formats.
package parsers

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/nguyenthenguyen/docx"

	"github.com/hygur/sidecar/internal/ingest"
)

// ErrInvalidDOCX indicates the file is not a valid DOCX document.
var ErrInvalidDOCX = errors.New("invalid DOCX file")

// ErrEmptyDOCX indicates the DOCX document contains no text content.
var ErrEmptyDOCX = errors.New("empty DOCX file")

// DOCXParser extracts text content from Microsoft Word DOCX files.
type DOCXParser struct{}

// NewDOCXParser creates a new DOCX parser instance.
func NewDOCXParser() *DOCXParser {
	return &DOCXParser{}
}

// SupportedExtensions returns the file extensions this parser handles.
func (p *DOCXParser) SupportedExtensions() []string {
	return []string{".docx"}
}

// Parse extracts text content and metadata from a DOCX file.
// The reader must provide the complete DOCX file content.
func (p *DOCXParser) Parse(ctx context.Context, r io.Reader) (string, ingest.Metadata, error) {
	// Check for context cancellation
	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	default:
	}

	// Read entire content into memory (DOCX is a ZIP archive, needs ReaderAt)
	data, err := io.ReadAll(r)
	if err != nil {
		return "", nil, fmt.Errorf("reading DOCX data: %w", err)
	}

	if len(data) == 0 {
		return "", nil, ErrEmptyDOCX
	}

	// Check context again after potentially slow read
	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	default:
	}

	// Open the DOCX from memory
	reader := bytes.NewReader(data)
	doc, err := docx.ReadDocxFromMemory(reader, int64(len(data)))
	if err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrInvalidDOCX, err)
	}
	defer doc.Close()

	// Get the raw XML content
	editable := doc.Editable()
	xmlContent := editable.GetContent()

	if xmlContent == "" {
		return "", nil, ErrEmptyDOCX
	}

	// Extract text from XML
	text, err := extractTextFromXML(xmlContent)
	if err != nil {
		return "", nil, fmt.Errorf("extracting text from DOCX: %w", err)
	}

	// Return the RAW extracted text (line breaks + case preserved). The ingest
	// layer derives normalized_text via ingest.NormalizeText and stores both.
	if strings.TrimSpace(text) == "" {
		return "", nil, ErrEmptyDOCX
	}

	// Build metadata
	metadata := ingest.Metadata{
		"format": "docx",
	}

	return text, metadata, nil
}

// extractTextFromXML extracts plain text from DOCX XML content.
// DOCX stores text in <w:t> elements within the document.xml.
func extractTextFromXML(xmlContent string) (string, error) {
	var result strings.Builder
	decoder := xml.NewDecoder(strings.NewReader(xmlContent))

	// Track if we're inside a text element
	inText := false
	// Track paragraph boundaries for proper spacing
	needSpace := false

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parsing XML: %w", err)
		}

		switch t := token.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t": // <w:t> - text element
				inText = true
				if needSpace && result.Len() > 0 {
					result.WriteRune(' ')
					needSpace = false
				}
			case "br": // <w:br/> - line break
				result.WriteRune(' ')
				needSpace = false
			case "tab": // <w:tab/> - tab character
				result.WriteRune(' ')
				needSpace = false
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p": // End of paragraph
				needSpace = true
			}
		case xml.CharData:
			if inText {
				result.Write(t)
			}
		}
	}

	return result.String(), nil
}
