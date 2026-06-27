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

// defaultWhisperModel is the model name sent to the Whisper transcription endpoint.
const defaultWhisperModel = "whisper-1"

// asrPromptTemplate is Google's prescribed Gemma ASR prompt (gemma-4-12B-it
// model card §audio). %[1]s is the language name (both source and target for
// pure transcription).
const asrPromptTemplate = `Transcribe the following speech segment in %[1]s into %[1]s text.

Follow these specific instructions for formatting the answer:
* Only output the transcription, with no newlines.
* When transcribing numbers, write the digits, i.e. write 1.7 and not one point seven, and write 3 instead of three.`

// AudioParser transcribes audio. When a multimodal chat model is configured
// (e.g. gemma-4-12B-it, which does native ASR), it sends the audio to the
// chat-completions endpoint with Google's ASR prompt. Otherwise it falls back
// to a Whisper-compatible /v1/audio/transcriptions endpoint. All errors are
// fail-soft: it logs warnings and returns empty text rather than failing.
type AudioParser struct {
	llmBaseURL   string
	model        string // chat model for ASR (empty → Whisper fallback only)
	whisperModel string // model id for /v1/audio/transcriptions (empty → "whisper-1")
	apiKey       string // Bearer for hosted backends (Infomaniak); empty → no auth (Sparky/local)
}

// NewAudioParser creates an AudioParser using the Whisper transcription path.
func NewAudioParser(llmBaseURL string) *AudioParser {
	return &AudioParser{llmBaseURL: strings.TrimSuffix(llmBaseURL, "/")}
}

// NewAudioParserWithModel creates an AudioParser that transcribes via the
// chat-completions endpoint of an audio-capable model (Gemma), falling back to
// Whisper if the chat ASR fails.
func NewAudioParserWithModel(endpoint, model string) *AudioParser {
	return &AudioParser{llmBaseURL: strings.TrimSuffix(endpoint, "/"), model: model}
}

// WithAuth sets the Bearer API key sent to the audio endpoints. Hosted backends
// (Infomaniak) require it; empty = no auth (the local/Sparky default).
func (p *AudioParser) WithAuth(apiKey string) *AudioParser { p.apiKey = apiKey; return p }

// WithWhisperModel overrides the transcription model id sent to
// /v1/audio/transcriptions (e.g. Infomaniak "Whisper V3"); empty keeps "whisper-1".
func (p *AudioParser) WithWhisperModel(m string) *AudioParser {
	if m != "" {
		p.whisperModel = m
	}
	return p
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
		slog.WarnContext(ctx, "audio.parse: no endpoint configured, skipping transcription")
		return "", meta, nil
	}

	data, err := io.ReadAll(r)
	if err != nil {
		slog.WarnContext(ctx, "audio.parse: failed to read audio", "err", err)
		return "", meta, nil
	}
	if len(data) == 0 {
		return "", meta, nil
	}

	// Primary path: native ASR via a multimodal chat model (Gemma) with Google's
	// ASR prompt + an input_audio block (audio after the text prompt).
	if p.model != "" {
		text, err := transcribeViaChat(ctx, p.llmBaseURL, p.model, data, sniffAudioFormat(data), p.apiKey)
		if err == nil && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text), meta, nil
		}
		slog.WarnContext(ctx, "audio.parse: chat ASR failed; trying Whisper fallback", "err", err)
	}

	// Fallback: Whisper-compatible /v1/audio/transcriptions (multipart).
	tmpFile, err := os.CreateTemp("", "hygur-audio-*.tmp")
	if err != nil {
		slog.WarnContext(ctx, "audio.parse: failed to create temp file", "err", err)
		return "", meta, nil
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		slog.WarnContext(ctx, "audio.parse: failed to write temp file", "err", err)
		return "", meta, nil
	}
	tmpFile.Close()

	text, err := transcribeFile(ctx, p.llmBaseURL, tmpPath, p.whisperModel, p.apiKey)
	if err != nil {
		slog.WarnContext(ctx, "audio.parse: transcription failed", "err", err)
		return "", meta, nil
	}
	return text, meta, nil
}

// transcribeViaChat sends audio to a multimodal chat model (e.g. gemma-4-12B-it)
// using Google's ASR prompt and an OpenAI input_audio block (audio AFTER the
// text prompt, per the multimodal-ordering guidance). Returns the transcription.
func transcribeViaChat(ctx context.Context, endpoint, model string, data []byte, format, apiKey string) (string, error) {
	lang := os.Getenv("HYGUR_ASR_LANGUAGE")
	if lang == "" {
		lang = "French"
	}
	payload := map[string]any{
		"model":       model,
		"max_tokens":  4096,
		"temperature": 0,
		// Reasoning models otherwise burn the budget "thinking"; ASR is extraction.
		"chat_template_kwargs": map[string]any{"enable_thinking": false},
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": fmt.Sprintf(asrPromptTemplate, lang)},
				{"type": "input_audio", "input_audio": map[string]string{
					"data":   base64.StdEncoding.EncodeToString(data),
					"format": format,
				}},
			},
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("audio chat ASR: marshal: %w", err)
	}
	url := strings.TrimSuffix(endpoint, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("audio chat ASR: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return "", fmt.Errorf("audio chat ASR: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("audio chat ASR returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("audio chat ASR: decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("audio chat ASR: empty response")
	}
	return out.Choices[0].Message.Content, nil
}

// sniffAudioFormat guesses the container from magic bytes for the input_audio
// `format` hint. Defaults to "mp3".
func sniffAudioFormat(data []byte) string {
	switch {
	case len(data) >= 8 && string(data[4:8]) == "ftyp":
		return "m4a"
	case len(data) >= 4 && string(data[0:4]) == "RIFF":
		return "wav"
	case len(data) >= 4 && string(data[0:4]) == "OggS":
		return "ogg"
	case len(data) >= 3 && string(data[0:3]) == "ID3":
		return "mp3"
	case len(data) >= 2 && data[0] == 0xFF && data[1]&0xE0 == 0xE0:
		return "mp3"
	default:
		return "mp3"
	}
}

// transcribeFile posts the audio file at path to the Whisper transcription
// endpoint. It returns the transcribed text or a wrapped error.
func transcribeFile(ctx context.Context, baseURL, path, whisperModel, apiKey string) (string, error) {
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

	// Add model field (configurable; e.g. Infomaniak "Whisper V3").
	model := whisperModel
	if model == "" {
		model = defaultWhisperModel
	}
	if err := mw.WriteField("model", model); err != nil {
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
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

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
