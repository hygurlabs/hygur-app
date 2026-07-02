// Package parsers provides parser implementations for various document formats.
package parsers

import (
	"context"
	"io"
	"regexp"
	"strings"

	"github.com/hygur/sidecar/internal/ingest"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"
)

// frontmatterRegex matches YAML frontmatter between --- delimiters.
var frontmatterRegex = regexp.MustCompile(`(?s)^---\s*\n(.*?)\n---\s*\n?`)

// MarkdownParser handles Markdown files with optional YAML frontmatter.
type MarkdownParser struct {
	md goldmark.Markdown
}

// NewMarkdownParser creates a new Markdown parser.
func NewMarkdownParser() *MarkdownParser {
	return &MarkdownParser{
		md: goldmark.New(),
	}
}

// SupportedExtensions returns the file extensions this parser can handle.
func (p *MarkdownParser) SupportedExtensions() []string {
	return []string{".md", ".markdown"}
}

// Parse extracts text content and metadata from a Markdown file.
// It handles YAML frontmatter extraction and converts Markdown AST to plain text.
func (p *MarkdownParser) Parse(ctx context.Context, r io.Reader) (string, ingest.Metadata, error) {
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

	// Extract frontmatter if present
	metadata, body := p.extractFrontmatter(content)

	// Parse Markdown to AST
	reader := text.NewReader(body)
	doc := p.md.Parser().Parse(reader)

	// Extract plain text from AST
	var textBuilder strings.Builder
	sectionCount := p.walkAST(doc, body, &textBuilder)

	// Add section_count to metadata
	if metadata == nil {
		metadata = make(ingest.Metadata)
	}
	metadata["section_count"] = sectionCount

	// Return the RAW extracted text (case preserved). The ingest layer derives
	// normalized_text via ingest.NormalizeText and stores both.
	return textBuilder.String(), metadata, nil
}

// extractFrontmatter extracts YAML frontmatter from the content.
// Returns metadata and the remaining body content.
func (p *MarkdownParser) extractFrontmatter(content []byte) (ingest.Metadata, []byte) {
	matches := frontmatterRegex.FindSubmatch(content)
	if matches == nil {
		return nil, content
	}

	// Parse YAML frontmatter
	var frontmatter map[string]any
	if err := yaml.Unmarshal(matches[1], &frontmatter); err != nil {
		// If YAML parsing fails, return content as-is without metadata
		return nil, content
	}

	// Extract specific metadata fields
	metadata := make(ingest.Metadata)
	if title, ok := frontmatter["title"]; ok {
		metadata["title"] = title
	}
	if author, ok := frontmatter["author"]; ok {
		metadata["author"] = author
	}
	// Accept several common date frontmatter keys → normalised to "date"
	for _, key := range []string{"date", "created", "published", "created_at"} {
		if v, ok := frontmatter[key]; ok {
			metadata["date"] = v
			break
		}
	}

	// Remove frontmatter from body
	body := content[len(matches[0]):]

	return metadata, body
}

// walkAST traverses the Markdown AST and extracts plain text.
// Returns the number of heading sections found.
func (p *MarkdownParser) walkAST(node ast.Node, source []byte, builder *strings.Builder) int {
	sectionCount := 0

	err := ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		switch n.Kind() {
		case ast.KindHeading:
			sectionCount++
			// Add space before heading text
			if builder.Len() > 0 {
				builder.WriteString(" ")
			}

		case ast.KindText:
			textNode := n.(*ast.Text)
			builder.Write(textNode.Segment.Value(source))
			if textNode.SoftLineBreak() || textNode.HardLineBreak() {
				builder.WriteString(" ")
			}

		case ast.KindString:
			stringNode := n.(*ast.String)
			builder.Write(stringNode.Value)

		case ast.KindCodeBlock:
			// Extract text from code blocks (important for personal notes with data)
			codeBlock := n.(*ast.CodeBlock)
			lines := codeBlock.Lines()
			for i := 0; i < lines.Len(); i++ {
				line := lines.At(i)
				builder.Write(line.Value(source))
			}
			builder.WriteString(" ")
			return ast.WalkSkipChildren, nil

		case ast.KindFencedCodeBlock:
			// Extract text from fenced code blocks
			fencedBlock := n.(*ast.FencedCodeBlock)
			lines := fencedBlock.Lines()
			for i := 0; i < lines.Len(); i++ {
				line := lines.At(i)
				builder.Write(line.Value(source))
			}
			builder.WriteString(" ")
			return ast.WalkSkipChildren, nil

		case ast.KindCodeSpan:
			// Extract inline code content
			codeSpan := n.(*ast.CodeSpan)
			for child := codeSpan.FirstChild(); child != nil; child = child.NextSibling() {
				if textNode, ok := child.(*ast.Text); ok {
					builder.Write(textNode.Segment.Value(source))
				}
			}
			return ast.WalkSkipChildren, nil

		case ast.KindImage:
			// For images, extract alt text only
			imageNode := n.(*ast.Image)
			if len(imageNode.Title) > 0 {
				builder.Write(imageNode.Title)
				builder.WriteString(" ")
			}
			// Let children (alt text) be processed

		case ast.KindLink:
			// For links, extract text content only (skip the URL)
			// Let children be processed for link text

		case ast.KindAutoLink:
			// Skip auto-links entirely
			return ast.WalkSkipChildren, nil

		case ast.KindParagraph, ast.KindListItem, ast.KindBlockquote:
			// Add space after block elements
			if builder.Len() > 0 {
				lastByte := builder.String()[builder.Len()-1]
				if lastByte != ' ' && lastByte != '\n' {
					builder.WriteString(" ")
				}
			}
		}

		return ast.WalkContinue, nil
	})

	if err != nil {
		// Should not happen with our simple walker
		return sectionCount
	}

	return sectionCount
}
