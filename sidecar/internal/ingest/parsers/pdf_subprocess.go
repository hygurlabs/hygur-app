package parsers

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// PDFExtractSubcommand is the argv[1] value that switches this binary into the
// isolated PDF-text-extraction mode. main() must intercept it before any normal
// startup. Kept here so main() and the client reference one literal.
const PDFExtractSubcommand = "pdf-extract"

// pdfChildHeapCapBytes bounds the child process's heap. The pure-Go PDF parser
// (github.com/ledongthuc/pdf) can allocate tens of GB on a malformed PDF or a
// decompression bomb — readArray appends unboundedly while a corrupt
// FlateDecode stream inflates. Running the parse in a child that force-exits
// past this cap keeps that blast radius entirely off the sidecar.
const pdfChildHeapCapBytes = 512 << 20

// pdfChildWatchInterval is how often the child samples its heap. Peak ≈ cap +
// (alloc-rate × interval); at ~2 GB/s that's ~512 MiB + ~200 MiB, comfortably
// under the sidecar's 1 GiB target — and the whole child is freed on exit.
const pdfChildWatchInterval = 100 * time.Millisecond

// DefaultPDFExtractTimeout caps a single isolated extraction. A legitimate PDF
// parses in well under a second; this only fires for pathological inputs.
const DefaultPDFExtractTimeout = 30 * time.Second

// DefaultPDFOCRTimeout caps a single isolated OCR extraction. OCR renders every
// page (pdftoppm) then runs tesseract/vision per page, so it is far slower than
// text extraction — minutes, not milliseconds. Only used on the operator-triggered
// re-index path (never bulk sync), so a generous cap is safe.
const DefaultPDFOCRTimeout = 5 * time.Minute

// pdfOCREnvVar, when set to "1" in the child's environment, switches the isolated
// extractor to the OCR-capable parser (NewPDFParser) instead of the text-only one.
// Set by ExtractPDFTextIsolatedOCR; unset on the default (bulk) path so sync never OCRs.
const pdfOCREnvVar = "HYGUR_PDF_OCR"

// RunPDFExtractSubprocess is the entrypoint for the isolated child. It reads PDF
// bytes from stdin, extracts text, writes the text to stdout and exits 0. Text-only
// by default; when HYGUR_PDF_OCR=1 it runs the OCR fallback (scanned/image-only PDFs).
// Any failure exits non-zero so the parent skips the attachment. A heap watchdog
// force-exits if the parse balloons, so a parse bomb can never reach the host's
// memory ceiling. This function never returns (it always os.Exit).
func RunPDFExtractSubprocess() {
	// Heap watchdog: a malformed PDF can make the parser allocate without bound;
	// the goroutine running Parse can't be killed, so the whole process bails.
	go func() {
		var ms runtime.MemStats
		t := time.NewTicker(pdfChildWatchInterval)
		defer t.Stop()
		for range t.C {
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > pdfChildHeapCapBytes {
				os.Exit(3)
			}
		}
	}()

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(1)
	}
	// OCR-capable parser only when explicitly requested (operator re-index); the
	// default (bulk sync) stays text-only so per-attachment vision/tesseract calls
	// never saturate the inference model. The child inherits the vision endpoint env.
	parser := NewPDFParserTextOnly()
	if os.Getenv(pdfOCREnvVar) == "1" {
		parser = NewPDFParser()
	}
	text, _, perr := parser.Parse(context.Background(), bytes.NewReader(data))
	if perr != nil {
		os.Exit(2)
	}
	_, _ = os.Stdout.WriteString(text)
	os.Exit(0)
}

// ExtractPDFTextIsolatedOCR is ExtractPDFTextIsolated with the OCR fallback ENABLED
// in the child (HYGUR_PDF_OCR=1) — for the operator-triggered re-index of scanned /
// image-only attachments (e.g. an insurance relevé whose plate lives only in a scan).
// Still fully process-isolated + heap-capped; only the timeout is larger (OCR is slow).
func ExtractPDFTextIsolatedOCR(ctx context.Context, data []byte, timeout time.Duration) string {
	if timeout <= 0 {
		timeout = DefaultPDFOCRTimeout
	}
	return extractPDFTextIsolated(ctx, data, timeout, true)
}

// ExtractPDFTextIsolated extracts text from `data` in a child process (this same
// binary re-invoked with PDFExtractSubcommand), bounded by `timeout`. It returns
// "" on any failure — extraction is best-effort. The child is SIGKILLed if it
// exceeds the timeout, freeing all of its memory. Memory is bounded regardless
// of the input by the child's heap watchdog; this is the only safe way to run
// the fragile pure-Go PDF parser over untrusted mail attachments.
func ExtractPDFTextIsolated(ctx context.Context, data []byte, timeout time.Duration) string {
	if timeout <= 0 {
		timeout = DefaultPDFExtractTimeout
	}
	return extractPDFTextIsolated(ctx, data, timeout, false)
}

// extractPDFTextIsolated is the shared implementation; ocr toggles the child's OCR
// fallback via HYGUR_PDF_OCR. Both variants are process-isolated and heap-capped.
func extractPDFTextIsolated(ctx context.Context, data []byte, timeout time.Duration, ocr bool) string {
	if len(data) == 0 {
		return ""
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, exe, PDFExtractSubcommand)
	cmd.Stdin = bytes.NewReader(data)
	// Don't let the child inherit pprof binding or other side effects.
	cmd.Env = append(os.Environ(), "HYGUR_PPROF=", "HYGUR_MEM_LIMIT_MIB=0")
	if ocr {
		cmd.Env = append(cmd.Env, pdfOCREnvVar+"=1")
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return ""
	}
	return out.String()
}
