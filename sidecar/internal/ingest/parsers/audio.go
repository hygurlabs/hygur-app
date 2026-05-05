// Package parsers provides parser implementations for various document formats.
package parsers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/ingest"
)

// audioExtensions lists the file extensions handled by AudioParser.
var audioExtensions = []string{".mp3", ".m4a", ".wav", ".ogg"}

// defaultWhisperModel is the model name sent to the transcription endpoint.
const defaultWhisperModel = "whisper-1"

// AudioParser transcribes audio files using a Whisper-compatible endpoint
// (LM Studio or OpenAI). All errors are fail-soft: the parser never returns
// a fatal error — it logs warnings and returns an empty NormalizedText instead.
type AudioParser struct {
	llmBaseURL string
}

// NewAudioParser creates a new AudioParser.
// llmBaseURL is the LM Studio base URL (e.g. "http://localhost:1234").
// The parser also honours the HYGUR_WHISPER_LANG environment variable for
// the transcription language (default "fr").
func NewAudioParser(llmBaseURL string) *AudioParser {
	return &AudioParser{llmBaseURL: strings.TrimSuffix(llmBaseURL, "/")}
}

// SupportedExtensions returns the file extensions this parser handles.
func (p *AudioParser) SupportedExtensions() []string {
	return audioExtensions
}

// Parse implements ingest.Parser. The reader content is written to a temp
// file so it can be sent as multipart/form-data to the transcription
// endpoint. Errors are logged as warnings; an empty string is returned
// rather than propagating the error.
func (p *AudioParser) Parse(ctx context.Context, r io.Reader) (string, ingest.Metadata, error) {
	meta := ingest.Metadata{"source_type": "audio"}

	if p.llmBaseURL == "" {
		slog.WarnContext(ctx, "audio.parse: no LLM base URL configured, skipping transcription")
		return "", meta, nil
	}

	// Write reader to a temp file with a recognizable extension so that the
	// server can determine the audio format.
	tmpFile, err := os.CreateTemp("", "hygur-audio-*.tmp")
	if err != nil {
		slog.WarnContext(ctx, "audio.parse: failed to create temp file", "err", err)
		return "", meta, nil
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, r); err != nil {
		tmpFile.Close()
		slog.WarnContext(ctx, "audio.parse: failed to write temp file", "err", err)
		return "", meta, nil
	}
	tmpFile.Close()

	text, err := transcribeFile(ctx, p.llmBaseURL, tmpPath)
	if err != nil {
		slog.WarnContext(ctx, "audio.parse: transcription failed", "err", err)
		return "", meta, nil
	}

	return text, meta, nil
}

// ParseAudio is the path-based convenience API described in the sprint spec.
// It opens the file at path and posts it to llmBaseURL/v1/audio/transcriptions.
// Errors are fail-soft: an empty ParsedDocument is returned on failure.
func ParseAudio(ctx context.Context, path string, llmBaseURL string) (ingest.Metadata, error) {
	meta := ingest.Metadata{"source_type": "audio"}
	if llmBaseURL == "" {
		slog.WarnContext(ctx, "audio: no LLM base URL, skipping", "path", path)
		return meta, nil
	}

	text, err := transcribeFile(ctx, strings.TrimSuffix(llmBaseURL, "/"), path)
	if err != nil {
		slog.WarnContext(ctx, "audio: transcription failed", "path", path, "err", err)
		return meta, nil
	}
	meta["text"] = text
	return meta, nil
}

// transcribeFile posts the audio file at path to the Whisper transcription
// endpoint. It returns the transcribed text or a wrapped error.
func transcribeFile(ctx context.Context, baseURL, path string) (string, error) {
	lang := os.Getenv("HYGUR_WHISPER_LANG")
	if lang == "" {
		lang = "fr"
	}

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("audio: open %s: %w", path, err)
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// Add model field.
	if err := mw.WriteField("model", defaultWhisperModel); err != nil {
		return "", fmt.Errorf("audio: write model field: %w", err)
	}

	// Add language field.
	if err := mw.WriteField("language", lang); err != nil {
		return "", fmt.Errorf("audio: write language field: %w", err)
	}

	// Add file field — use the original filename so the server knows the format.
	fw, err := mw.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return "", fmt.Errorf("audio: create form file: %w", err)
	}
	if _, err := io.Copy(fw, f); err != nil {
		return "", fmt.Errorf("audio: copy file content: %w", err)
	}

	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("audio: close multipart writer: %w", err)
	}

	endpoint := baseURL + "/v1/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return "", fmt.Errorf("audio: create request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("audio: HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("audio: server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("audio: decode response: %w", err)
	}

	return strings.TrimSpace(result.Text), nil
}
