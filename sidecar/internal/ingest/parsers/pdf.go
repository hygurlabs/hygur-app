// Package parsers provides parser implementations for various document formats.
package parsers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/ingest"
	"github.com/ledongthuc/pdf"
)

// ErrProtectedPDF indicates the PDF is password-protected and cannot be read.
var ErrProtectedPDF = errors.New("pdf: document is password protected")

// ErrInvalidPDF indicates the file is not a valid PDF.
var ErrInvalidPDF = errors.New("pdf: invalid or corrupted document")

// PDFParser extracts text content from PDF documents.
type PDFParser struct{}

// NewPDFParser creates a new PDF parser instance.
func NewPDFParser() *PDFParser {
	return &PDFParser{}
}

// SupportedExtensions returns the file extensions this parser handles.
func (p *PDFParser) SupportedExtensions() []string {
	return []string{".pdf"}
}

// Parse extracts text content and metadata from a PDF document.
// The reader content is fully buffered since PDF parsing requires random access.
func (p *PDFParser) Parse(ctx context.Context, r io.Reader) (string, ingest.Metadata, error) {
	// Check context before starting
	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	default:
	}

	// Read all content into a buffer (PDF requires seeking)
	data, err := io.ReadAll(r)
	if err != nil {
		return "", nil, fmt.Errorf("pdf: failed to read input: %w", err)
	}

	// Check for empty input
	if len(data) == 0 {
		return "", nil, ErrInvalidPDF
	}

	// Check context after reading
	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	default:
	}

	// Create a ReaderAt from the buffer
	readerAt := bytes.NewReader(data)

	// Open the PDF
	pdfReader, err := pdf.NewReader(readerAt, int64(len(data)))
	if err != nil {
		// Check for common error patterns
		errStr := err.Error()
		if strings.Contains(errStr, "encrypt") || strings.Contains(errStr, "password") {
			return "", nil, ErrProtectedPDF
		}
		return "", nil, fmt.Errorf("%w: %v", ErrInvalidPDF, err)
	}

	pageCount := pdfReader.NumPage()
	if pageCount == 0 {
		// Valid PDF but no pages
		return "", ingest.Metadata{"page_count": 0}, nil
	}

	// Extract text from each page
	var pageTexts []string
	for i := 1; i <= pageCount; i++ {
		// Check context between pages
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		default:
		}

		page := pdfReader.Page(i)
		if page.V.IsNull() {
			continue
		}

		text, err := page.GetPlainText(nil)
		if err != nil {
			// Log but continue - some pages might be images only
			continue
		}

		text = strings.TrimSpace(text)
		if text != "" {
			pageTexts = append(pageTexts, text)
		}
	}

	// Join pages with double newlines
	content := strings.Join(pageTexts, "\n\n")

	// Normalize the text
	content = ingest.NormalizeText(content)

	metadata := ingest.Metadata{
		"page_count": pageCount,
	}

	// Extract /CreationDate from PDF document info if available.
	// The pdf library exposes Trailer which holds the info dictionary.
	if info := pdfReader.Trailer().Key("Info"); !info.IsNull() {
		if creationDate := info.Key("CreationDate"); !creationDate.IsNull() {
			if s := creationDate.RawString(); s != "" {
				metadata["doc_date"] = s
			}
		}
	}

	// Sparse-text heuristic: if the extracted text averages fewer than 50
	// characters per page, the PDF is likely scanned/image-only. Attempt an
	// OCR fallback via pdftoppm + Tesseract.
	if isSparseText(content, pageCount) {
		slog.WarnContext(ctx, "pdf.parse: sparse text detected, attempting OCR fallback",
			"page_count", pageCount, "content_len", len(content))
		ocrText := p.ocrFallback(ctx, data)
		if ocrText != "" {
			content = ingest.NormalizeText(ocrText)
			metadata["ocr_attempted"] = true
		} else {
			metadata["ocr_attempted"] = false
		}
	}

	return content, metadata, nil
}

// isSparseText reports whether the extracted text is suspiciously thin,
// which is a reliable indicator of a scanned/image-only PDF.
// The threshold is 50 characters per page on average.
func isSparseText(text string, pageCount int) bool {
	if pageCount <= 0 {
		return false
	}
	return len([]rune(strings.TrimSpace(text)))/pageCount < 50
}

// ocrFallback converts each PDF page to a PNG with pdftoppm and runs
// Tesseract OCR on the resulting images. It is entirely fail-soft: any
// error is logged as a warning and an empty string is returned so the
// caller can continue with the (possibly empty) directly-extracted text.
func (p *PDFParser) ocrFallback(ctx context.Context, data []byte) (result string) {
	defer func() {
		if r := recover(); r != nil {
			slog.WarnContext(ctx, "pdf.ocr: recovered from panic", "panic", r)
			result = ""
		}
	}()

	// Guard: pdftoppm must be present.
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		slog.WarnContext(ctx, "pdf.ocr: pdftoppm not found in PATH, skipping OCR fallback")
		return ""
	}

	// Write the raw PDF bytes to a temp file so pdftoppm can seek through it.
	tmpDir, err := os.MkdirTemp("", "hygur-pdf-ocr-*")
	if err != nil {
		slog.WarnContext(ctx, "pdf.ocr: failed to create temp dir", "err", err)
		return ""
	}
	defer os.RemoveAll(tmpDir)

	pdfPath := filepath.Join(tmpDir, "input.pdf")
	if err := os.WriteFile(pdfPath, data, 0o600); err != nil {
		slog.WarnContext(ctx, "pdf.ocr: failed to write temp PDF", "err", err)
		return ""
	}

	// Run pdftoppm to rasterise every page at 150 dpi as PNG.
	outPrefix := filepath.Join(tmpDir, "page")
	ppmCtx, ppmCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer ppmCancel()

	cmd := exec.CommandContext(ppmCtx, "pdftoppm", "-r", "150", "-png", pdfPath, outPrefix)
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.WarnContext(ctx, "pdf.ocr: pdftoppm failed", "err", err, "output", string(out))
		return ""
	}

	// Collect the generated PNG files in page order.
	matches, err := filepath.Glob(outPrefix + "-*.png")
	if err != nil || len(matches) == 0 {
		slog.WarnContext(ctx, "pdf.ocr: no PNG pages produced by pdftoppm")
		return ""
	}
	sort.Strings(matches) // lexicographic order matches page order

	// OCR each page image using the same Tesseract pipeline as ImageParser.
	ip := NewImageParser("")
	var pageTexts []string
	for _, imgPath := range matches {
		// Respect context cancellation between pages.
		select {
		case <-ctx.Done():
			slog.WarnContext(ctx, "pdf.ocr: context cancelled during OCR", "err", ctx.Err())
			return ""
		default:
		}

		imgData, err := os.ReadFile(imgPath)
		if err != nil {
			slog.WarnContext(ctx, "pdf.ocr: failed to read PNG page", "path", imgPath, "err", err)
			continue
		}

		text := ip.tryTesseract(ctx, imgData)
		if text != "" {
			pageTexts = append(pageTexts, text)
		}
	}

	return strings.Join(pageTexts, "\n\n")
}
