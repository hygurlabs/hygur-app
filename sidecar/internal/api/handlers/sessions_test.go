package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// TestSessionsHandler_GetReturnsAttachments validates the read-path wiring:
// store attachments → SessionsHandler.Get → DTO with inline base64 + the
// available flag (false once an audio recording has been purged).
func TestSessionsHandler_GetReturnsAttachments(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now()

	if err := db.CreateChatSession(ctx, &store.ChatSession{
		SessionID: "s1", Title: "t", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateChatSession: %v", err)
	}
	if err := db.AppendChatMessage(ctx, &store.ChatMessage{
		MessageID: "m1", SessionID: "s1", Role: "user", Content: "regarde",
	}); err != nil {
		t.Fatalf("AppendChatMessage: %v", err)
	}
	img := []byte{0x89, 0x50, 0x4e, 0x47}
	if err := db.AppendChatMessageAttachments(ctx, "m1", []store.ChatAttachment{
		{Type: "image", Title: "p.png", MimeType: "image/png", Data: img, ByteSize: len(img)},
		// A purged audio: row present, but no bytes (Data nil).
		{Type: "audio", Title: "v.wav", Format: "wav", Data: nil, ByteSize: 4096},
	}); err != nil {
		t.Fatalf("AppendChatMessageAttachments: %v", err)
	}

	handler := NewSessionsHandler(db, zerolog.Nop())
	req := httptest.NewRequest(http.MethodGet, "/sessions/s1", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "s1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	handler.Get(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var resp SessionDetailDTO
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(resp.Messages))
	}
	atts := resp.Messages[0].Attachments
	if len(atts) != 2 {
		t.Fatalf("attachments = %d, want 2", len(atts))
	}

	// Image: available with inline base64 matching the stored bytes.
	if atts[0].Type != "image" || !atts[0].Available {
		t.Errorf("image attachment: %+v", atts[0])
	}
	if got, _ := base64.StdEncoding.DecodeString(atts[0].Data); string(got) != string(img) {
		t.Errorf("image base64 mismatch: %q", atts[0].Data)
	}
	// Purged audio: not available, no data, metadata retained.
	if atts[1].Type != "audio" || atts[1].Available || atts[1].Data != "" {
		t.Errorf("purged audio should be unavailable with no data: %+v", atts[1])
	}
	if atts[1].ByteSize != 4096 {
		t.Errorf("purged audio should keep byte_size, got %d", atts[1].ByteSize)
	}
}

// TestLatestUserMediaAttachments validates the persist-path extraction: only
// the most recent user turn's image/audio are taken, base64 decoded; documents
// are ignored (they're KB references, persisted by id elsewhere).
func TestLatestUserMediaAttachments(t *testing.T) {
	imgB64 := base64.StdEncoding.EncodeToString([]byte{1, 2, 3})
	audB64 := base64.StdEncoding.EncodeToString([]byte{4, 5, 6, 7})
	messages := []llm.Message{
		{Role: "user", Content: "old", Attachments: []llm.Attachment{
			{Type: llm.AttachmentTypeImage, Data: base64.StdEncoding.EncodeToString([]byte{9})},
		}},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "new", Attachments: []llm.Attachment{
			{Type: llm.AttachmentTypeImage, MimeType: "image/png", Data: imgB64, Title: "p.png"},
			{Type: llm.AttachmentTypeAudio, Format: "wav", Data: audB64, Title: "v.wav"},
			{Type: llm.AttachmentTypeDocument, ContentID: "doc:1"},
		}},
	}
	got := latestUserMediaAttachments(messages)
	if len(got) != 2 {
		t.Fatalf("got %d attachments, want 2 (image+audio, doc ignored)", len(got))
	}
	if got[0].Type != "image" || got[0].MimeType != "image/png" || got[0].ByteSize != 3 || got[0].Title != "p.png" {
		t.Errorf("image extraction: %+v", got[0])
	}
	if got[1].Type != "audio" || got[1].Format != "wav" || got[1].ByteSize != 4 || got[1].Title != "v.wav" {
		t.Errorf("audio extraction: %+v", got[1])
	}
}
