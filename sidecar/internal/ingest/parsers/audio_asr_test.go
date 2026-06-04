package parsers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSniffAudioFormat(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"m4a ftyp", append([]byte{0, 0, 0, 0x20}, []byte("ftypM4A ")...), "m4a"},
		{"wav RIFF", []byte("RIFF....WAVEfmt "), "wav"},
		{"ogg", []byte("OggS............"), "ogg"},
		{"mp3 ID3", []byte("ID3\x03\x00\x00\x00\x00"), "mp3"},
		{"unknown", []byte("zzzzzzzz"), "mp3"},
	}
	for _, c := range cases {
		if got := sniffAudioFormat(c.data); got != c.want {
			t.Errorf("%s: sniffAudioFormat = %q, want %q", c.name, got, c.want)
		}
	}
}

// transcribeViaChat must send Google's ASR prompt then the audio (audio AFTER
// text) to /v1/chat/completions and return the assistant transcription.
func TestTranscribeViaChat_PromptThenAudio(t *testing.T) {
	var gotText, gotAudioFmt string
	var order []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content []map[string]any `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) == 1 {
			for _, part := range body.Messages[0].Content {
				typ, _ := part["type"].(string)
				order = append(order, typ)
				if typ == "text" {
					gotText, _ = part["text"].(string)
				}
				if typ == "input_audio" {
					if ia, ok := part["input_audio"].(map[string]any); ok {
						gotAudioFmt, _ = ia["format"].(string)
					}
				}
			}
		}
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"bonjour le monde"}}]}`))
	}))
	defer srv.Close()

	out, err := transcribeViaChat(context.Background(), srv.URL, "gemma4-12b", []byte("\x00\x00\x00\x20ftypM4A audio-bytes"), "m4a")
	if err != nil {
		t.Fatalf("transcribeViaChat: %v", err)
	}
	if out != "bonjour le monde" {
		t.Errorf("transcription = %q, want %q", out, "bonjour le monde")
	}
	if len(order) != 2 || order[0] != "text" || order[1] != "input_audio" {
		t.Errorf("content order = %v, want [text input_audio] (audio after text)", order)
	}
	if gotAudioFmt != "m4a" {
		t.Errorf("audio format = %q, want m4a", gotAudioFmt)
	}
	if !contains(gotText, "Transcribe") || !contains(gotText, "no newlines") {
		t.Errorf("prompt missing Google ASR instructions: %q", gotText)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
