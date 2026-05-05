package mail

import (
	"testing"
	"time"
)

func TestListOptionsDefaults(t *testing.T) {
	opts := ListOptions{}

	if opts.Limit != 0 {
		t.Errorf("expected default Limit to be 0, got %d", opts.Limit)
	}
	if opts.Offset != 0 {
		t.Errorf("expected default Offset to be 0, got %d", opts.Offset)
	}
	if opts.Since != nil {
		t.Error("expected default Since to be nil")
	}
	if opts.Before != nil {
		t.Error("expected default Before to be nil")
	}
	if opts.MailboxID != "" {
		t.Errorf("expected default MailboxID to be empty, got %q", opts.MailboxID)
	}
}

func TestListOptionsWithValues(t *testing.T) {
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)

	opts := ListOptions{
		Limit:     50,
		Offset:    10,
		Since:     &yesterday,
		Before:    &now,
		MailboxID: "INBOX",
	}

	if opts.Limit != 50 {
		t.Errorf("expected Limit to be 50, got %d", opts.Limit)
	}
	if opts.Offset != 10 {
		t.Errorf("expected Offset to be 10, got %d", opts.Offset)
	}
	if opts.Since == nil || !opts.Since.Equal(yesterday) {
		t.Error("expected Since to match yesterday")
	}
	if opts.Before == nil || !opts.Before.Equal(now) {
		t.Error("expected Before to match now")
	}
	if opts.MailboxID != "INBOX" {
		t.Errorf("expected MailboxID to be INBOX, got %q", opts.MailboxID)
	}
}

func TestThreadStruct(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-1 * time.Hour)

	thread := Thread{
		ID:             "thread-123",
		Subject:        "Test Subject",
		Participants:   []string{"alice@example.com", "bob@example.com"},
		DateRange:      [2]time.Time{earlier, now},
		MessageCount:   5,
		HasAttachments: true,
		Labels:         []string{"INBOX", "Important"},
	}

	if thread.ID != "thread-123" {
		t.Errorf("expected ID to be thread-123, got %q", thread.ID)
	}
	if thread.Subject != "Test Subject" {
		t.Errorf("expected Subject to be Test Subject, got %q", thread.Subject)
	}
	if len(thread.Participants) != 2 {
		t.Errorf("expected 2 participants, got %d", len(thread.Participants))
	}
	if !thread.DateRange[0].Equal(earlier) {
		t.Error("expected DateRange[0] to be earlier time")
	}
	if !thread.DateRange[1].Equal(now) {
		t.Error("expected DateRange[1] to be now")
	}
	if thread.MessageCount != 5 {
		t.Errorf("expected MessageCount to be 5, got %d", thread.MessageCount)
	}
	if !thread.HasAttachments {
		t.Error("expected HasAttachments to be true")
	}
	if len(thread.Labels) != 2 {
		t.Errorf("expected 2 labels, got %d", len(thread.Labels))
	}
}

func TestMessageStruct(t *testing.T) {
	now := time.Now()

	msg := Message{
		ID:       "msg-456",
		ThreadID: "thread-123",
		From:     "alice@example.com",
		To:       []string{"bob@example.com"},
		Cc:       []string{"carol@example.com"},
		Date:     now,
		Subject:  "Test Message",
		Body:     "Plain text body",
		HTMLBody: "<p>HTML body</p>",
		Attachments: []Attachment{
			{
				ID:       "att-789",
				Filename: "document.pdf",
				MimeType: "application/pdf",
				Size:     1024,
			},
		},
	}

	if msg.ID != "msg-456" {
		t.Errorf("expected ID to be msg-456, got %q", msg.ID)
	}
	if msg.ThreadID != "thread-123" {
		t.Errorf("expected ThreadID to be thread-123, got %q", msg.ThreadID)
	}
	if msg.From != "alice@example.com" {
		t.Errorf("expected From to be alice@example.com, got %q", msg.From)
	}
	if len(msg.To) != 1 || msg.To[0] != "bob@example.com" {
		t.Errorf("expected To to contain bob@example.com, got %v", msg.To)
	}
	if len(msg.Cc) != 1 || msg.Cc[0] != "carol@example.com" {
		t.Errorf("expected Cc to contain carol@example.com, got %v", msg.Cc)
	}
	if !msg.Date.Equal(now) {
		t.Error("expected Date to match now")
	}
	if msg.Subject != "Test Message" {
		t.Errorf("expected Subject to be Test Message, got %q", msg.Subject)
	}
	if msg.Body != "Plain text body" {
		t.Errorf("expected Body to be plain text, got %q", msg.Body)
	}
	if msg.HTMLBody != "<p>HTML body</p>" {
		t.Errorf("expected HTMLBody to be HTML, got %q", msg.HTMLBody)
	}
	if len(msg.Attachments) != 1 {
		t.Errorf("expected 1 attachment, got %d", len(msg.Attachments))
	}
}

func TestAttachmentStruct(t *testing.T) {
	att := Attachment{
		ID:       "att-001",
		Filename: "report.xlsx",
		MimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		Size:     2048576, // 2MB
	}

	if att.ID != "att-001" {
		t.Errorf("expected ID to be att-001, got %q", att.ID)
	}
	if att.Filename != "report.xlsx" {
		t.Errorf("expected Filename to be report.xlsx, got %q", att.Filename)
	}
	if att.MimeType != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Errorf("unexpected MimeType: %q", att.MimeType)
	}
	if att.Size != 2048576 {
		t.Errorf("expected Size to be 2048576, got %d", att.Size)
	}
}

func TestThreadWithEmptyParticipants(t *testing.T) {
	thread := Thread{
		ID:           "empty-thread",
		Subject:      "No participants",
		Participants: []string{},
	}

	if len(thread.Participants) != 0 {
		t.Errorf("expected empty Participants, got %d", len(thread.Participants))
	}
}

func TestMessageWithNoAttachments(t *testing.T) {
	msg := Message{
		ID:          "simple-msg",
		Body:        "Simple message",
		Attachments: nil,
	}

	if msg.Attachments != nil {
		t.Error("expected nil Attachments")
	}
}

func TestMessageWithEmptyAttachments(t *testing.T) {
	msg := Message{
		ID:          "msg-no-att",
		Body:        "Message without attachments",
		Attachments: []Attachment{},
	}

	if len(msg.Attachments) != 0 {
		t.Errorf("expected empty Attachments slice, got %d", len(msg.Attachments))
	}
}
