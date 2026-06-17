package retrieval

import (
	"testing"

	"github.com/hygur/sidecar/internal/store"
)

func TestOwnerOrigin(t *testing.T) {
	sent := map[string]any{"gmail_labels": []any{"INBOX", "SENT"}}
	inbox := map[string]any{"gmail_labels": []any{"INBOX"}}
	sentStr := map[string]any{"gmail_labels": []string{"SENT"}}

	cases := []struct {
		name       string
		sourceType string
		meta       map[string]any
		want       OwnerOrigin
	}{
		{"note → owner", store.SourceTypeNote, nil, OriginOwner},
		{"file → owner", store.SourceTypeFile, nil, OriginOwner},
		{"task → owner", store.SourceTypeTask, nil, OriginOwner},
		{"decision → owner", store.SourceTypeDecision, nil, OriginOwner},
		{"SENT mail ([]any) → owner", store.SourceTypeMail, sent, OriginOwner},
		{"SENT mail ([]string) → owner", store.SourceTypeEmail, sentStr, OriginOwner},
		{"received mail → external (the Porto case)", store.SourceTypeMail, inbox, OriginExternal},
		{"mail without labels → external (conservative)", store.SourceTypeMail, nil, OriginExternal},
		{"event → external", store.SourceTypeEvent, nil, OriginExternal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ownerOrigin(c.sourceType, c.meta); got != c.want {
				t.Errorf("ownerOrigin(%q) = %q, want %q", c.sourceType, got, c.want)
			}
		})
	}
}
