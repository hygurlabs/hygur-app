package tools

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/mail"
)

// mockMailConnector is a test implementation of mail.MailConnector.
type mockMailConnector struct {
	connected   bool
	threads     map[string]*mail.Thread
	messages    map[string][]mail.Message
	connectErr  error
	messagesErr error
}

func newMockMailConnector() *mockMailConnector {
	return &mockMailConnector{
		connected: true,
		threads:   make(map[string]*mail.Thread),
		messages:  make(map[string][]mail.Message),
	}
}

func (m *mockMailConnector) Connect(ctx context.Context) error {
	if m.connectErr != nil {
		return m.connectErr
	}
	m.connected = true
	return nil
}

func (m *mockMailConnector) Disconnect() error {
	m.connected = false
	return nil
}

func (m *mockMailConnector) IsConnected() bool {
	return m.connected
}

func (m *mockMailConnector) ListThreads(ctx context.Context, opts mail.ListOptions) ([]mail.Thread, error) {
	var threads []mail.Thread
	for _, t := range m.threads {
		threads = append(threads, *t)
	}
	return threads, nil
}

func (m *mockMailConnector) GetThread(ctx context.Context, threadID string) (*mail.Thread, error) {
	t, ok := m.threads[threadID]
	if !ok {
		return nil, errors.New("thread not found")
	}
	return t, nil
}

func (m *mockMailConnector) GetMessages(ctx context.Context, threadID string) ([]mail.Message, error) {
	if m.messagesErr != nil {
		return nil, m.messagesErr
	}
	msgs, ok := m.messages[threadID]
	if !ok {
		return nil, errors.New("thread not found")
	}
	return msgs, nil
}

func (m *mockMailConnector) GetMessagesByThread(ctx context.Context, thread *mail.Thread) ([]mail.Message, error) {
	if thread == nil {
		return nil, errors.New("thread is nil")
	}
	return m.GetMessages(ctx, thread.ID)
}

func TestListAttachmentsTool_Run(t *testing.T) {
	// Setup mock connector with test data
	connector := newMockMailConnector()
	connector.messages["thread-123"] = []mail.Message{
		{
			ID:       "msg-1",
			ThreadID: "thread-123",
			From:     "alice@example.com",
			Date:     time.Now(),
			Subject:  "Document attached",
			Attachments: []mail.Attachment{
				{
					ID:       "att-1",
					Filename: "report.pdf",
					MimeType: "application/pdf",
					Size:     1024,
				},
				{
					ID:       "att-2",
					Filename: "data.xlsx",
					MimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
					Size:     2048,
				},
			},
		},
		{
			ID:       "msg-2",
			ThreadID: "thread-123",
			From:     "bob@example.com",
			Date:     time.Now(),
			Subject:  "Re: Document attached",
			Attachments: []mail.Attachment{
				{
					ID:       "att-3",
					Filename: "feedback.docx",
					MimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
					Size:     512,
				},
			},
		},
	}

	connectors := map[string]mail.MailConnector{
		"gmail": connector,
	}

	tool := NewListAttachmentsTool(connectors)

	// Test successful listing
	ctx := context.Background()
	resp, err := tool.Run(ctx, ListAttachmentsRequest{
		ThreadID: "thread-123",
		Source:   "gmail",
	})

	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	if resp.ThreadID != "thread-123" {
		t.Errorf("ThreadID = %q, want %q", resp.ThreadID, "thread-123")
	}

	if resp.Source != "gmail" {
		t.Errorf("Source = %q, want %q", resp.Source, "gmail")
	}

	if len(resp.Attachments) != 3 {
		t.Fatalf("len(Attachments) = %d, want 3", len(resp.Attachments))
	}

	// Verify first attachment
	if resp.Attachments[0].ID != "att-1" {
		t.Errorf("Attachments[0].ID = %q, want %q", resp.Attachments[0].ID, "att-1")
	}
	if resp.Attachments[0].Filename != "report.pdf" {
		t.Errorf("Attachments[0].Filename = %q, want %q", resp.Attachments[0].Filename, "report.pdf")
	}
	if resp.Attachments[0].MIMEType != "application/pdf" {
		t.Errorf("Attachments[0].MIMEType = %q, want %q", resp.Attachments[0].MIMEType, "application/pdf")
	}
	if resp.Attachments[0].Size != 1024 {
		t.Errorf("Attachments[0].Size = %d, want %d", resp.Attachments[0].Size, 1024)
	}

	// Verify third attachment (from second message)
	if resp.Attachments[2].ID != "att-3" {
		t.Errorf("Attachments[2].ID = %q, want %q", resp.Attachments[2].ID, "att-3")
	}
}

func TestListAttachmentsTool_Run_NoAttachments(t *testing.T) {
	connector := newMockMailConnector()
	connector.messages["thread-empty"] = []mail.Message{
		{
			ID:          "msg-1",
			ThreadID:    "thread-empty",
			From:        "alice@example.com",
			Date:        time.Now(),
			Subject:     "No attachments here",
			Attachments: nil,
		},
	}

	connectors := map[string]mail.MailConnector{
		"proton": connector,
	}

	tool := NewListAttachmentsTool(connectors)

	ctx := context.Background()
	resp, err := tool.Run(ctx, ListAttachmentsRequest{
		ThreadID: "thread-empty",
		Source:   "proton",
	})

	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	if len(resp.Attachments) != 0 {
		t.Errorf("len(Attachments) = %d, want 0", len(resp.Attachments))
	}

	// Verify we return an empty slice, not nil (for proper JSON serialization)
	if resp.Attachments == nil {
		t.Error("Attachments should be empty slice, not nil")
	}
}

func TestListAttachmentsTool_Run_MissingThreadID(t *testing.T) {
	connector := newMockMailConnector()
	connectors := map[string]mail.MailConnector{
		"gmail": connector,
	}

	tool := NewListAttachmentsTool(connectors)

	ctx := context.Background()
	_, err := tool.Run(ctx, ListAttachmentsRequest{
		ThreadID: "",
		Source:   "gmail",
	})

	if err == nil {
		t.Fatal("Run() should fail with empty thread_id")
	}
}

func TestListAttachmentsTool_Run_MissingSource(t *testing.T) {
	connector := newMockMailConnector()
	connectors := map[string]mail.MailConnector{
		"gmail": connector,
	}

	tool := NewListAttachmentsTool(connectors)

	ctx := context.Background()
	_, err := tool.Run(ctx, ListAttachmentsRequest{
		ThreadID: "thread-123",
		Source:   "",
	})

	if err == nil {
		t.Fatal("Run() should fail with empty source")
	}
}

func TestListAttachmentsTool_Run_SourceNotFound(t *testing.T) {
	connector := newMockMailConnector()
	connectors := map[string]mail.MailConnector{
		"gmail": connector,
	}

	tool := NewListAttachmentsTool(connectors)

	ctx := context.Background()
	_, err := tool.Run(ctx, ListAttachmentsRequest{
		ThreadID: "thread-123",
		Source:   "proton", // Not registered
	})

	if err == nil {
		t.Fatal("Run() should fail with unknown source")
	}
}

func TestListAttachmentsTool_Run_SourceNotConnected(t *testing.T) {
	connector := newMockMailConnector()
	connector.connected = false
	connectors := map[string]mail.MailConnector{
		"gmail": connector,
	}

	tool := NewListAttachmentsTool(connectors)

	ctx := context.Background()
	_, err := tool.Run(ctx, ListAttachmentsRequest{
		ThreadID: "thread-123",
		Source:   "gmail",
	})

	if err == nil {
		t.Fatal("Run() should fail when source is not connected")
	}
}

func TestListAttachmentsTool_Run_ThreadNotFound(t *testing.T) {
	connector := newMockMailConnector()
	connector.messages["other-thread"] = []mail.Message{}
	connectors := map[string]mail.MailConnector{
		"gmail": connector,
	}

	tool := NewListAttachmentsTool(connectors)

	ctx := context.Background()
	_, err := tool.Run(ctx, ListAttachmentsRequest{
		ThreadID: "nonexistent-thread",
		Source:   "gmail",
	})

	if err == nil {
		t.Fatal("Run() should fail when thread is not found")
	}
}

func TestListAttachmentsTool_Run_GetMessagesError(t *testing.T) {
	connector := newMockMailConnector()
	connector.messagesErr = errors.New("connection timeout")
	connectors := map[string]mail.MailConnector{
		"gmail": connector,
	}

	tool := NewListAttachmentsTool(connectors)

	ctx := context.Background()
	_, err := tool.Run(ctx, ListAttachmentsRequest{
		ThreadID: "thread-123",
		Source:   "gmail",
	})

	if err == nil {
		t.Fatal("Run() should fail when GetMessages returns error")
	}
}

func TestListAttachmentsTool_Run_MultipleConnectors(t *testing.T) {
	gmailConnector := newMockMailConnector()
	gmailConnector.messages["gmail-thread"] = []mail.Message{
		{
			ID:       "msg-gmail",
			ThreadID: "gmail-thread",
			Attachments: []mail.Attachment{
				{ID: "gmail-att", Filename: "gmail.pdf", MimeType: "application/pdf", Size: 100},
			},
		},
	}

	protonConnector := newMockMailConnector()
	protonConnector.messages["proton-thread"] = []mail.Message{
		{
			ID:       "msg-proton",
			ThreadID: "proton-thread",
			Attachments: []mail.Attachment{
				{ID: "proton-att", Filename: "proton.pdf", MimeType: "application/pdf", Size: 200},
			},
		},
	}

	connectors := map[string]mail.MailConnector{
		"gmail":  gmailConnector,
		"proton": protonConnector,
	}

	tool := NewListAttachmentsTool(connectors)
	ctx := context.Background()

	// Test Gmail
	gmailResp, err := tool.Run(ctx, ListAttachmentsRequest{
		ThreadID: "gmail-thread",
		Source:   "gmail",
	})
	if err != nil {
		t.Fatalf("Run() for Gmail failed: %v", err)
	}
	if len(gmailResp.Attachments) != 1 || gmailResp.Attachments[0].ID != "gmail-att" {
		t.Error("Gmail attachments mismatch")
	}

	// Test Proton
	protonResp, err := tool.Run(ctx, ListAttachmentsRequest{
		ThreadID: "proton-thread",
		Source:   "proton",
	})
	if err != nil {
		t.Fatalf("Run() for Proton failed: %v", err)
	}
	if len(protonResp.Attachments) != 1 || protonResp.Attachments[0].ID != "proton-att" {
		t.Error("Proton attachments mismatch")
	}
}
