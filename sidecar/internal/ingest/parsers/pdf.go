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
	pdfcpuapi "github.com/pdfcpu/pdfcpu/pkg/api"
)

// ErrProtectedPDF indicates the PDF is password-protected and cannot be read.
var ErrProtectedPDF = errors.New("pdf: document is password protected")

// ErrInvalidPDF indicates the file is not a valid PDF.
var ErrInvalidPDF = errors.New("pdf: invalid or corrupted document")

// PDFParser extracts text content from PDF documents.
type PDFParser struct {
	visionEndpoint string
	visionModel    string
	// disableOCR skips the (expensive) OCR fallback entirely. Used for bulk
	// mail-attachment indexing: OCRing scanned attachments via the vision model
	// would hammer the inference model and make mail sync crawl. Direct text
	// extraction (the common case) still works.
	disableOCR bool
}

// NewPDFParser creates a new PDF parser instance. The OCR fallback's vision
// endpoint/model default to HYGUR_VISION_ENDPOINT / HYGUR_VISION_MODEL (same
// convention as ImageParser), so a config→env bridge enables vision OCR for
// scanned PDFs without threading the values through every call site.
func NewPDFParser() *PDFParser {
	return &PDFParser{
		visionEndpoint: os.Getenv("HYGUR_VISION_ENDPOINT"),
		visionModel:    os.Getenv("HYGUR_VISION_MODEL"),
	}
}

// NewPDFParserTextOnly returns a parser that extracts embedded text but never
// runs the OCR fallback — for bulk mail-attachment indexing where vision OCR is
// far too costly per attachment. Text-based PDFs still index fully.
func NewPDFParserTextOnly() *PDFParser {
	return &PDFParser{disableOCR: true}
}

// SupportedExtensions returns the file extensions this parser handles.
func (p *PDFParser) SupportedExtensions() []string {
	return []string{".pdf"}
}

// Parse extracts text content and metadata from a PDF document.
// The reader content is fully buffered since PDF parsing requires random access.
//
// Extraction strategy (foundational fix for the spaced-glyph garbage the
// ledongthuc/pdf library emitted on the TARA « Contractor Agreement » PDF):
//  1. PRIMARY: pdftotext (poppler) — a robust layout-aware extractor that
//     reconstructs words from individually-positioned glyphs. Runs as an
//     external, memory-safe subprocess.
//  2. FAIL-SOFT FALLBACK: the pure-Go ledongthuc/pdf library (panic-recovered),
//     so extraction still works when poppler is absent from the image.
//
// The BETTER of the two by quality score wins (poppler on ties), so a healthy
// ledongthuc result is never discarded for an empty/garbled poppler one and
// vice-versa. A per-extraction confidence signal is always stamped into meta
// (extract_method / extract_confidence / extract_low_confidence) so the engine
// can KNOW an extraction is untrustworthy instead of trusting it blindly.
func (p *PDFParser) Parse(ctx context.Context, r io.Reader) (content string, meta ingest.Metadata, err error) {
	// Recover so a bad document degrades to an error rather than crashing the
	// process — mail-attachment indexing runs Parse in goroutines, where an
	// unrecovered panic takes down the whole sidecar. The ledongthuc extraction
	// has its own inner recover; this is the outer backstop.
	defer func() {
		if rec := recover(); rec != nil {
			slog.WarnContext(ctx, "pdf.parse: recovered from panic in PDF library", "panic", rec)
			content, meta, err = "", nil, fmt.Errorf("%w: panic: %v", ErrInvalidPDF, rec)
		}
	}()

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

	// PRIMARY: poppler. Memory-safe (external process), robust on the glyph
	// positioning that defeats ledongthuc. Empty when poppler is absent or fails.
	popplerText := strings.TrimSpace(extractViaPdftotext(ctx, data))

	// FALLBACK: pure-Go ledongthuc, which also yields page_count + /CreationDate.
	ledongText, pageCount, docDate, ledongErr := extractViaLedongthuc(ctx, data)
	ledongText = strings.TrimSpace(ledongText)

	// If poppler produced nothing AND ledongthuc hard-failed (not just empty),
	// surface the original error semantics (protected / invalid).
	if popplerText == "" && ledongErr != nil {
		if errors.Is(ledongErr, ErrProtectedPDF) {
			return "", nil, ErrProtectedPDF
		}
		if errors.Is(ledongErr, errNoPages) {
			return "", ingest.Metadata{"page_count": 0}, nil
		}
		return "", nil, ledongErr
	}

	// Choose the better extraction by quality; poppler wins ties (it is primary
	// and layout-aware). This guarantees we never replace a clean ledongthuc
	// result with garbled/empty poppler output, nor the reverse.
	popplerQ := ingest.AssessTextQuality(popplerText)
	ledongQ := ingest.AssessTextQuality(ledongText)

	content = popplerText
	method := "pdftotext"
	quality := popplerQ
	if popplerText == "" || ledongQ.Score > popplerQ.Score {
		content = ledongText
		method = "ledongthuc"
		quality = ledongQ
	}

	metadata := ingest.Metadata{}
	if pageCount > 0 {
		metadata["page_count"] = pageCount
	}
	if docDate != "" {
		metadata["doc_date"] = docDate
	}

	// OCR fallback fires when the best embedded-text extraction is either sparse
	// (scanned/image-only PDF) OR garbled (spaced-glyph shape poppler couldn't
	// fix either), UNLESS OCR is disabled. The disableOCR gate keeps the bulk
	// sync path off the critical path: sync never OCRs synchronously (a
	// per-attachment vision call would saturate the inference model); instead it
	// STAMPS low_confidence so an async re-extraction pass can OCR later.
	if !p.disableOCR && (isSparseText(content, pageCount) || quality.Garbled) {
		slog.WarnContext(ctx, "pdf.parse: sparse or garbled text, attempting OCR fallback",
			"page_count", pageCount, "content_len", len(content), "garbled", quality.Garbled)
		ocrText := strings.TrimSpace(p.ocrFallback(ctx, data))
		if ocrQ := ingest.AssessTextQuality(ocrText); ocrText != "" && ocrQ.Score > quality.Score {
			content = ocrText
			method = "ocr"
			quality = ocrQ
			metadata["ocr_attempted"] = true
		} else {
			metadata["ocr_attempted"] = false
		}
	}

	metadata["extract_method"] = method
	metadata["extract_confidence"] = quality.Score
	metadata["extract_low_confidence"] = quality.LowConfidence

	return content, metadata, nil
}

// errNoPages signals a structurally valid PDF that contains zero pages.
var errNoPages = errors.New("pdf: no pages")

// extractViaLedongthuc extracts text with the pure-Go ledongthuc/pdf library and
// returns the page count and /CreationDate too. It recovers internally so a
// library panic becomes an error (ErrInvalidPDF) instead of unwinding through
// the caller — keeping the poppler result usable when only ledongthuc panics.
func extractViaLedongthuc(ctx context.Context, data []byte) (text string, pageCount int, docDate string, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.WarnContext(ctx, "pdf.ledongthuc: recovered from panic", "panic", rec)
			text, pageCount, docDate, err = "", 0, "", fmt.Errorf("%w: panic: %v", ErrInvalidPDF, rec)
		}
	}()

	pdfReader, rerr := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if rerr != nil {
		errStr := rerr.Error()
		if strings.Contains(errStr, "encrypt") || strings.Contains(errStr, "password") {
			return "", 0, "", ErrProtectedPDF
		}
		return "", 0, "", fmt.Errorf("%w: %v", ErrInvalidPDF, rerr)
	}

	pageCount = pdfReader.NumPage()
	if pageCount == 0 {
		return "", 0, "", errNoPages
	}

	var pageTexts []string
	for i := 1; i <= pageCount; i++ {
		select {
		case <-ctx.Done():
			return "", 0, "", ctx.Err()
		default:
		}
		page := pdfReader.Page(i)
		if page.V.IsNull() {
			continue
		}
		t, perr := page.GetPlainText(nil)
		if perr != nil {
			continue // some pages might be image-only
		}
		if t = strings.TrimSpace(t); t != "" {
			pageTexts = append(pageTexts, t)
		}
	}

	if info := pdfReader.Trailer().Key("Info"); !info.IsNull() {
		if creationDate := info.Key("CreationDate"); !creationDate.IsNull() {
			if s := creationDate.RawString(); s != "" {
				docDate = s
			}
		}
	}

	// Join pages with double newlines. This is the RAW extracted text (line
	// breaks + case preserved); the ingest layer derives normalized_text.
	return strings.Join(pageTexts, "\n\n"), pageCount, docDate, nil
}

// pdftotextTimeout caps a single poppler extraction. A legitimate PDF extracts
// in well under a second; this only fires for pathological inputs.
const pdftotextTimeout = 30 * time.Second

// extractViaPdftotext extracts text with pdftotext (poppler) as an external,
// memory-safe subprocess. It is the ROBUST primary extractor: unlike the pure-Go
// library it reconstructs words from individually-positioned glyphs (the
// spaced-glyph artifact). Returns "" when pdftotext is absent or fails, so it is
// never a hard dependency — the ledongthuc fallback still works.
func extractViaPdftotext(ctx context.Context, data []byte) string {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		slog.WarnContext(ctx, "pdf.parse: pdftotext not in PATH, using pure-Go fallback")
		return ""
	}
	tmpDir, err := os.MkdirTemp("", "hygur-pdftotext-*")
	if err != nil {
		return ""
	}
	defer os.RemoveAll(tmpDir)

	inPath := filepath.Join(tmpDir, "in.pdf")
	if err := os.WriteFile(inPath, data, 0o600); err != nil {
		return ""
	}

	cctx, cancel := context.WithTimeout(ctx, pdftotextTimeout)
	defer cancel()

	// -q quiet, -enc UTF-8 for correct accents, -eol unix, "-" writes to stdout.
	cmd := exec.CommandContext(cctx, "pdftotext", "-q", "-enc", "UTF-8", "-eol", "unix", inPath, "-")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		slog.WarnContext(ctx, "pdf.parse: pdftotext failed, using pure-Go fallback", "err", err)
		return ""
	}
	return out.String()
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

// ocrFallback OCRs a scanned/image-only PDF. It first tries a pure-Go path:
// extract the images embedded in the PDF (pdfcpu — for scanned docs each page
// is one image) and OCR them via the vision model, so OCR works with NO system
// binary (portable). If that yields nothing and pdftoppm (poppler) happens to
// be installed, it falls back to rendering pages — which also covers vector /
// text-only-sparse pages and codecs pdfcpu can't decode. Entirely fail-soft.
func (p *PDFParser) ocrFallback(ctx context.Context, data []byte) (result string) {
	defer func() {
		if r := recover(); r != nil {
			slog.WarnContext(ctx, "pdf.ocr: recovered from panic", "panic", r)
			result = ""
		}
	}()

	if txt := p.ocrViaEmbeddedImages(ctx, data); strings.TrimSpace(txt) != "" {
		return txt
	}
	return p.ocrViaPdftoppm(ctx, data)
}

// ocrViaEmbeddedImages extracts the images embedded in the PDF (pure Go via
// pdfcpu) and OCRs each via ImageParser (Tesseract → vision model). No system
// dependency — the portable default. Returns "" when no images are extractable.
func (p *PDFParser) ocrViaEmbeddedImages(ctx context.Context, data []byte) string {
	imagesByPage, err := pdfcpuapi.ExtractImagesRaw(bytes.NewReader(data), nil, nil)
	if err != nil {
		slog.WarnContext(ctx, "pdf.ocr: pdfcpu image extraction failed", "err", err)
		return ""
	}
	ip := NewImageParserWithModel(p.visionEndpoint, p.visionModel)
	var pageTexts []string
	for _, page := range imagesByPage {
		for _, img := range page {
			select {
			case <-ctx.Done():
				return strings.Join(pageTexts, "\n\n")
			default:
			}
			if img.Reader == nil {
				continue
			}
			b, err := io.ReadAll(img.Reader)
			if err != nil || len(b) == 0 {
				continue
			}
			if text, _, _ := ip.Parse(ctx, bytes.NewReader(b)); strings.TrimSpace(text) != "" {
				pageTexts = append(pageTexts, strings.TrimSpace(text))
			}
		}
	}
	return strings.Join(pageTexts, "\n\n")
}

// ocrViaPdftoppm renders each page to PNG with pdftoppm (poppler) and OCRs it.
// Opportunistic: returns "" when pdftoppm is absent, so it's never a hard dep.
func (p *PDFParser) ocrViaPdftoppm(ctx context.Context, data []byte) string {
	// Guard: pdftoppm must be present.
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		slog.WarnContext(ctx, "pdf.ocr: pdftoppm not in PATH, skipping render fallback")
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

	// OCR each page image via ImageParser: Tesseract if installed, else the
	// configured vision model (e.g. nemotron-omni) — so OCR works with no system
	// Tesseract, keeping the app portable.
	ip := NewImageParserWithModel(p.visionEndpoint, p.visionModel)
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

		text, _, _ := ip.Parse(ctx, bytes.NewReader(imgData))
		if text = strings.TrimSpace(text); text != "" {
			pageTexts = append(pageTexts, text)
		}
	}

	return strings.Join(pageTexts, "\n\n")
}
