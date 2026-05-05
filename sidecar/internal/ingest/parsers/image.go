// Package parsers provides parser implementations for various document formats.
package parsers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/ingest"
)

// imageExtensions lists the file extensions handled by ImageParser.
var imageExtensions = []string{".png", ".jpg", ".jpeg", ".heic", ".webp"}

// ImageParser extracts text from image files via OCR (Tesseract) with a
// LM Studio vision model fallback. All errors are fail-soft: the parser
// never returns a fatal error — it logs warnings and returns an empty
// NormalizedText instead.
type ImageParser struct {
	visionEndpoint string
}

// NewImageParser creates a new ImageParser.
// visionEndpoint is the LM Studio vision API base URL (e.g. "http://localhost:1234").
// If empty, the HYGUR_VISION_ENDPOINT environment variable is used.
func NewImageParser(visionEndpoint string) *ImageParser {
	ep := visionEndpoint
	if ep == "" {
		ep = os.Getenv("HYGUR_VISION_ENDPOINT")
	}
	return &ImageParser{visionEndpoint: ep}
}

// SupportedExtensions returns the file extensions this parser handles.
func (p *ImageParser) SupportedExtensions() []string {
	return imageExtensions
}

// Parse implements ingest.Parser. It reads the image bytes from r and
// attempts OCR via Tesseract then, on failure, via the LM Studio vision
// endpoint. Errors are logged as warnings; an empty NormalizedText is
// returned rather than propagating the error.
func (p *ImageParser) Parse(ctx context.Context, r io.Reader) (string, ingest.Metadata, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		slog.WarnContext(ctx, "image.parse: failed to read image bytes", "err", err)
		return "", ingest.Metadata{"source_type": "image"}, nil
	}

	text := p.tryTesseract(ctx, data)
	if text == "" {
		text = p.tryVision(ctx, data)
	}

	return text, ingest.Metadata{"source_type": "image"}, nil
}

// tryTesseract attempts OCR using the Tesseract CLI. Returns empty string on
// any error, including Tesseract not being installed. Uses recover() to guard
// against any panic from the subprocess interaction.
func (p *ImageParser) tryTesseract(ctx context.Context, data []byte) (result string) {
	defer func() {
		if r := recover(); r != nil {
			slog.WarnContext(ctx, "image.tesseract: recovered from panic", "panic", r)
			result = ""
		}
	}()

	// Check Tesseract availability before attempting to use it.
	if _, err := exec.LookPath("tesseract"); err != nil {
		slog.WarnContext(ctx, "image.tesseract: not found in PATH, skipping OCR")
		return ""
	}

	// Write image to a temp file that Tesseract can read.
	tmpFile, err := os.CreateTemp("", "hygur-ocr-*")
	if err != nil {
		slog.WarnContext(ctx, "image.tesseract: failed to create temp file", "err", err)
		return ""
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		slog.WarnContext(ctx, "image.tesseract: failed to write temp file", "err", err)
		return ""
	}
	tmpFile.Close()

	// Determine language based on filename heuristic (caller provides path via
	// context or we default to eng+fra).
	lang := detectTesseractLang(ctx, tmpPath)

	// Tesseract writes output to stdout when passed "-" as output file.
	tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(tCtx, "tesseract", tmpPath, "stdout", "-l", lang)
	out, err := cmd.Output()
	if err != nil {
		slog.WarnContext(ctx, "image.tesseract: OCR failed", "err", err)
		return ""
	}

	return cleanOCRText(string(out))
}

// detectTesseractLang chooses the Tesseract language string. Defaults to
// "eng+fra"; swaps to "fra+eng" if the path contains "fr" as a path segment.
func detectTesseractLang(_ context.Context, path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts {
		if strings.EqualFold(part, "fr") {
			return "fra+eng"
		}
	}
	return "eng+fra"
}

// cleanOCRText removes very short lines (noise) and trims whitespace.
func cleanOCRText(raw string) string {
	lines := strings.Split(raw, "\n")
	var kept []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len([]rune(trimmed)) >= 3 {
			kept = append(kept, trimmed)
		}
	}
	return strings.Join(kept, "\n")
}

// tryVision posts the image to the LM Studio vision endpoint and returns the
// transcribed text. Returns empty string if the endpoint is not configured or
// on any HTTP error.
func (p *ImageParser) tryVision(ctx context.Context, data []byte) string {
	if p.visionEndpoint == "" {
		return ""
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	prompt := "Transcris tout le texte visible dans cette image. Aucun commentaire."

	payload := map[string]any{
		"model": "vision",
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": prompt},
					{"type": "image_url", "image_url": map[string]string{
						"url": "data:image/png;base64," + encoded,
					}},
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		slog.WarnContext(ctx, "image.vision: failed to marshal request", "err", err)
		return ""
	}

	endpoint := strings.TrimSuffix(p.visionEndpoint, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		slog.WarnContext(ctx, "image.vision: failed to create request", "err", err)
		return ""
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "image.vision: request failed", "err", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.WarnContext(ctx, "image.vision: non-200 response", "status", resp.StatusCode)
		return ""
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.WarnContext(ctx, "image.vision: failed to decode response", "err", err)
		return ""
	}

	if len(result.Choices) == 0 {
		return ""
	}

	return strings.TrimSpace(result.Choices[0].Message.Content)
}

// ParseImage is a convenience function for path-based callers (e.g. tests or
// direct invocations). It opens the file at path and delegates to ImageParser.
func ParseImage(ctx context.Context, path string) (ingest.Metadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return ingest.Metadata{"source_type": "image"}, fmt.Errorf("image: open %s: %w", path, err)
	}
	defer f.Close()

	p := NewImageParser("")
	_, meta, _ := p.Parse(ctx, f)
	return meta, nil
}
