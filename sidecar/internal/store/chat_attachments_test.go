package store

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// seedSession creates a session + one user message, returning its message_id.
func seedSession(t *testing.T, db *DB, sessionID, msgID string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	if err := db.CreateChatSession(ctx, &ChatSession{
		SessionID: sessionID, Title: "t", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateChatSession: %v", err)
	}
	if err := db.AppendChatMessage(ctx, &ChatMessage{
		MessageID: msgID, SessionID: sessionID, Role: "user", Content: "hi",
	}); err != nil {
		t.Fatalf("AppendChatMessage: %v", err)
	}
}

func TestChatAttachmentsRoundTrip(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	seedSession(t, db, "s1", "m1")

	img := []byte{0x89, 0x50, 0x4e, 0x47} // PNG magic-ish
	aud := bytes.Repeat([]byte{0x01}, 1024)
	atts := []ChatAttachment{
		{Type: "image", Title: "photo.png", MimeType: "image/png", Data: img, ByteSize: len(img)},
		{Type: "audio", Title: "note.wav", Format: "wav", Data: aud, ByteSize: len(aud)},
	}
	if err := db.AppendChatMessageAttachments(ctx, "m1", atts); err != nil {
		t.Fatalf("AppendChatMessageAttachments: %v", err)
	}

	got, err := db.ListChatMessageAttachments(ctx, "s1")
	if err != nil {
		t.Fatalf("ListChatMessageAttachments: %v", err)
	}
	rows := got["m1"]
	if len(rows) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(rows))
	}
	if rows[0].Type != "image" || rows[0].MimeType != "image/png" || !bytes.Equal(rows[0].Data, img) {
		t.Errorf("image round-trip mismatch: %+v", rows[0])
	}
	if rows[0].Title != "photo.png" {
		t.Errorf("image title not preserved: %q", rows[0].Title)
	}
	if rows[1].Type != "audio" || rows[1].Format != "wav" || !bytes.Equal(rows[1].Data, aud) {
		t.Errorf("audio round-trip mismatch: %+v", rows[1])
	}
}

func TestChatAttachmentsAudioCapPurgesOldest(t *testing.T) {
	old := maxAudioAttachmentBytes
	maxAudioAttachmentBytes = 1500 // bytes
	defer func() { maxAudioAttachmentBytes = old }()

	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// Three messages, each with a 1KB audio, appended in order. Each append
	// stamps created_at with time.Now() (sub-second), so insert order is the
	// retention order. After the third, total = 3KB > 1.5KB cap, so the two
	// oldest get purged (data NULL) and only the newest is kept.
	for i, id := range []string{"a", "b", "c"} {
		seedSession(t, db, "s-"+id, "m-"+id)
		if err := db.AppendChatMessageAttachments(ctx, "m-"+id, []ChatAttachment{
			{Type: "audio", Format: "wav", Data: bytes.Repeat([]byte{byte(i)}, 1024), ByteSize: 1024},
		}); err != nil {
			t.Fatalf("append audio %s: %v", id, err)
		}
	}

	// The newest (m-c) must still have its bytes; the oldest (m-a) must be purged.
	a := db.mustAttachment(ctx, t, "s-a", "m-a")
	if a.Data != nil {
		t.Errorf("oldest audio should be purged (nil data), got %d bytes", len(a.Data))
	}
	if a.ByteSize != 1024 {
		t.Errorf("purged row should keep byte_size metadata, got %d", a.ByteSize)
	}
	c := db.mustAttachment(ctx, t, "s-c", "m-c")
	if c.Data == nil {
		t.Errorf("newest audio should be retained, got nil data")
	}
}

// mustAttachment returns the first attachment of a message (test helper).
func (d *DB) mustAttachment(ctx context.Context, t *testing.T, sessionID, msgID string) ChatAttachment {
	t.Helper()
	m, err := d.ListChatMessageAttachments(ctx, sessionID)
	if err != nil {
		t.Fatalf("ListChatMessageAttachments(%s): %v", sessionID, err)
	}
	rows := m[msgID]
	if len(rows) == 0 {
		t.Fatalf("no attachments for %s", msgID)
	}
	return rows[0]
}
