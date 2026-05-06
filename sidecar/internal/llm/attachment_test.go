package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageMarshal_NoAttachments_PlainStringContent(t *testing.T) {
	m := Message{Role: "user", Content: "hello"}

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// content must be a string, not an array — this is the OpenAI rest
	// shape every existing model accepts.
	var probe struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatalf("unmarshal probe: %v", err)
	}
	if probe.Role != "user" {
		t.Errorf("role = %q, want user", probe.Role)
	}
	if !strings.HasPrefix(string(probe.Content), `"`) {
		t.Errorf("content should be a JSON string, got %s", probe.Content)
	}
	if strings.Contains(string(b), `"attachments"`) {
		t.Errorf("attachments field should be omitted when empty, got %s", b)
	}
}

func TestMessageMarshal_ImageAttachment_EmitsMultimodalArray(t *testing.T) {
	m := Message{
		Role:    "user",
		Content: "what's in this image?",
		Attachments: []Attachment{
			{Type: AttachmentTypeImage, MimeType: "image/png", Data: "AAA="},
		},
	}

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var probe struct {
		Role    string `json:"role"`
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text,omitempty"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url,omitempty"`
		} `json:"content"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatalf("unmarshal probe: %v\n%s", err, b)
	}
	if len(probe.Content) != 2 {
		t.Fatalf("expected 2 content parts, got %d: %s", len(probe.Content), b)
	}
	if probe.Content[0].Type != "text" || probe.Content[0].Text != "what's in this image?" {
		t.Errorf("first part should be text, got %+v", probe.Content[0])
	}
	if probe.Content[1].Type != "image_url" {
		t.Errorf("second part should be image_url, got %s", probe.Content[1].Type)
	}
	want := "data:image/png;base64,AAA="
	if probe.Content[1].ImageURL.URL != want {
		t.Errorf("image url = %q, want %q", probe.Content[1].ImageURL.URL, want)
	}
}

func TestMessageMarshal_AudioAttachment_EmitsInputAudioBlock(t *testing.T) {
	m := Message{
		Role:    "user",
		Content: "transcribe",
		Attachments: []Attachment{
			{Type: AttachmentTypeAudio, Data: "BBB=", Format: "wav"},
		},
	}

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var probe struct {
		Content []struct {
			Type       string `json:"type"`
			InputAudio struct {
				Data   string `json:"data"`
				Format string `json:"format"`
			} `json:"input_audio,omitempty"`
		} `json:"content"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatalf("unmarshal probe: %v\n%s", err, b)
	}
	if len(probe.Content) != 2 {
		t.Fatalf("expected 2 content parts, got %d: %s", len(probe.Content), b)
	}
	if probe.Content[1].Type != "input_audio" {
		t.Errorf("second part should be input_audio, got %s", probe.Content[1].Type)
	}
	if probe.Content[1].InputAudio.Data != "BBB=" || probe.Content[1].InputAudio.Format != "wav" {
		t.Errorf("audio block = %+v", probe.Content[1].InputAudio)
	}
}

func TestMessageMarshal_DocumentAttachment_FallsBackToTextStub(t *testing.T) {
	// Documents should be resolved to text by the handler before the LLM
	// client sees them. If one slips through (a bug), MarshalJSON must
	// still produce valid OpenAI content — emit a [document:<title>] stub
	// so the model isn't fed an unknown block type.
	m := Message{
		Role:    "user",
		Content: "summarise the doc",
		Attachments: []Attachment{
			{Type: AttachmentTypeDocument, ContentID: "note:abc", Title: "My Note"},
		},
	}

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if !strings.Contains(string(b), "[document:My Note]") {
		t.Errorf("document fallback should mention title, got %s", b)
	}
	if strings.Contains(string(b), `"attachments"`) {
		t.Errorf("attachments must not leak to LLM wire format, got %s", b)
	}
}

func TestMessageMarshal_AttachmentsFieldNeverReachesLLM(t *testing.T) {
	// Critical invariant: regardless of attachment count, the OpenAI wire
	// must never contain a top-level `attachments` field — that's a Hygur
	// internal artifact the model wouldn't understand.
	cases := []Message{
		{Role: "user", Content: "no attachments"},
		{
			Role:    "user",
			Content: "with image",
			Attachments: []Attachment{
				{Type: AttachmentTypeImage, MimeType: "image/jpeg", Data: "ZZ"},
			},
		},
	}
	for i, m := range cases {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("case %d marshal: %v", i, err)
		}
		if strings.Contains(string(b), `"attachments"`) {
			t.Errorf("case %d: attachments leaked: %s", i, b)
		}
	}
}

func TestMessageMarshal_PreservesToolFields_WithAttachments(t *testing.T) {
	m := Message{
		Role:       "tool",
		Content:    "result",
		ToolCallID: "call_1",
		Name:       "my_tool",
		Attachments: []Attachment{
			{Type: AttachmentTypeImage, MimeType: "image/png", Data: "X"},
		},
	}

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"tool_call_id":"call_1"`) {
		t.Errorf("tool_call_id missing: %s", s)
	}
	if !strings.Contains(s, `"name":"my_tool"`) {
		t.Errorf("name missing: %s", s)
	}
}

func TestAttachmentRoundTrip_HygurAPIShape(t *testing.T) {
	// Inbound: Swift POSTs a chat request with an attachment. The handler
	// decodes into llm.Message and must populate Attachments correctly.
	in := []byte(`{"role":"user","content":"hi","attachments":[{"type":"image","mime_type":"image/png","data":"AAA="}]}`)

	var m Message
	if err := json.Unmarshal(in, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(m.Attachments))
	}
	att := m.Attachments[0]
	if att.Type != AttachmentTypeImage {
		t.Errorf("type = %q, want image", att.Type)
	}
	if att.MimeType != "image/png" || att.Data != "AAA=" {
		t.Errorf("attachment fields wrong: %+v", att)
	}
}
