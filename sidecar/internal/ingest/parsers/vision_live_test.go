package parsers

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestLiveVisionOCR exercises the real vision endpoint end-to-end through the
// exact code path the sidecar uses (ImageParser.Parse → tryVision). Gated
// behind HYGUR_LIVE_VISION_IMAGE so it never runs in CI. Point the env at a
// running multimodal model and an image containing known text, e.g.:
//
//	HYGUR_VISION_ENDPOINT=http://<vision-host>:8082 \
//	HYGUR_VISION_MODEL=nemotron-omni \
//	HYGUR_LIVE_VISION_IMAGE=/tmp/ocr_test.png \
//	go test -tags sqlite_fts5 -run TestLiveVisionOCR -v ./internal/ingest/parsers/
func TestLiveVisionOCR(t *testing.T) {
	img := os.Getenv("HYGUR_LIVE_VISION_IMAGE")
	if img == "" {
		t.Skip("set HYGUR_LIVE_VISION_IMAGE to run the live vision OCR test")
	}
	f, err := os.Open(img)
	if err != nil {
		t.Fatalf("open image: %v", err)
	}
	defer f.Close()

	// NewImageParser("") reads HYGUR_VISION_ENDPOINT / HYGUR_VISION_MODEL.
	// Tesseract is expected absent here, so Parse falls through to the vision model.
	p := NewImageParser("")
	text, _, _ := p.Parse(context.Background(), f)
	t.Logf("vision OCR output: %q", text)
	if strings.TrimSpace(text) == "" {
		t.Fatal("expected non-empty OCR text from the vision model")
	}
}

// TestLiveVisionPDFOCR exercises the full pure-Go scanned-PDF path end-to-end:
// PDFParser.Parse → sparse-text heuristic → ocrViaEmbeddedImages (pdfcpu image
// extraction, no pdftoppm/Tesseract) → vision model. Gated behind
// HYGUR_LIVE_VISION_PDF (path to an image-only PDF).
//
//	HYGUR_VISION_ENDPOINT=http://<vision-host>:8082 \
//	HYGUR_VISION_MODEL=nemotron-omni \
//	HYGUR_LIVE_VISION_PDF=/tmp/scan.pdf \
//	go test -tags sqlite_fts5 -run TestLiveVisionPDFOCR -v ./internal/ingest/parsers/
func TestLiveVisionPDFOCR(t *testing.T) {
	pdfPath := os.Getenv("HYGUR_LIVE_VISION_PDF")
	if pdfPath == "" {
		t.Skip("set HYGUR_LIVE_VISION_PDF to run the live scanned-PDF OCR test")
	}
	f, err := os.Open(pdfPath)
	if err != nil {
		t.Fatalf("open pdf: %v", err)
	}
	defer f.Close()

	p := NewPDFParser() // reads HYGUR_VISION_ENDPOINT / HYGUR_VISION_MODEL
	text, meta, err := p.Parse(context.Background(), f)
	if err != nil {
		t.Fatalf("parse pdf: %v", err)
	}
	t.Logf("scanned-PDF OCR output: %q (meta=%v)", text, meta)
	if strings.TrimSpace(text) == "" {
		t.Fatal("expected OCR text from the scanned PDF via pdfcpu + vision")
	}
}
