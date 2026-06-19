package handlers

import (
	"strings"
	"testing"

	"github.com/hygur/sidecar/internal/llm"
)

// TestChatGuidanceCarriesVoice asserts the chat system guidance (Couche A) carries
// the shared prose-voice block on top of the base persona.
func TestChatGuidanceCarriesVoice(t *testing.T) {
	out := injectFormatGuidance([]llm.Message{{Role: "user", Content: "hi"}})
	if len(out) == 0 || out[0].Role != "system" {
		t.Fatal("expected a system message at the top of the chat turn")
	}
	if !strings.Contains(out[0].Content, llm.ProseVoiceGuidance) {
		t.Error("chat system guidance is missing the prose voice block")
	}
}
