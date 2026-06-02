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

// RunPDFExtractSubprocess is the entrypoint for the isolated child. It reads PDF
// bytes from stdin, extracts text (no OCR), writes the text to stdout and exits
// 0. Any failure exits non-zero so the parent skips the attachment. A heap
// watchdog force-exits if the parse balloons, so a parse bomb can never reach
// the host's memory ceiling. This function never returns (it always os.Exit).
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
	text, _, perr := NewPDFParserTextOnly().Parse(context.Background(), bytes.NewReader(data))
	if perr != nil {
		os.Exit(2)
	}
	_, _ = os.Stdout.WriteString(text)
	os.Exit(0)
}

// ExtractPDFTextIsolated extracts text from `data` in a child process (this same
// binary re-invoked with PDFExtractSubcommand), bounded by `timeout`. It returns
// "" on any failure — extraction is best-effort. The child is SIGKILLed if it
// exceeds the timeout, freeing all of its memory. Memory is bounded regardless
// of the input by the child's heap watchdog; this is the only safe way to run
// the fragile pure-Go PDF parser over untrusted mail attachments.
func ExtractPDFTextIsolated(ctx context.Context, data []byte, timeout time.Duration) string {
	if len(data) == 0 {
		return ""
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if timeout <= 0 {
		timeout = DefaultPDFExtractTimeout
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, exe, PDFExtractSubcommand)
	cmd.Stdin = bytes.NewReader(data)
	// Don't let the child inherit pprof binding or other side effects.
	cmd.Env = append(os.Environ(), "HYGUR_PPROF=", "HYGUR_MEM_LIMIT_MIB=0")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return ""
	}
	return out.String()
}
