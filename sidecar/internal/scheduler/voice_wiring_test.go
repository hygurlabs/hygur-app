package scheduler

import (
	"strings"
	"testing"

	"github.com/hygur/sidecar/internal/llm"
)

// TestProseVoiceWiring asserts the shared voice block reaches every user-facing
// prose prompt and stays out of the strict JSON/extraction prompts.
func TestProseVoiceWiring(t *testing.T) {
	voiced := map[string]string{
		"reply_draft":       replyDraftSystemPrompt,
		"chronicle":         chronicleSystemPrompt,
		"chronicle_closing": chronicleClosingPrompt,
		"positions":         positionsSystemPrompt,
		"followup_report":   followupReportSystemPrompt,
		"daily_brief":       dailyBriefSystemPrompt,
	}
	for name, p := range voiced {
		if !strings.Contains(p, llm.ProseVoiceGuidance) {
			t.Errorf("prose prompt %q is missing the shared voice block", name)
		}
	}
	// JSON/extraction prompts must stay strict and unpadded.
	if strings.Contains(followupSystemPrompt, llm.ProseVoiceGuidance) {
		t.Error("followupSystemPrompt (JSON) must not carry the prose voice block")
	}
}
