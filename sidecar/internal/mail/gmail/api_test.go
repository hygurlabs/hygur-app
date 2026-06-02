package gmail

import (
	"context"
	"testing"
	"time"

	mailpkg "github.com/hygur/sidecar/internal/mail"
	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"
)

func TestNewGmailConnector(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	if connector == nil {
		t.Fatal("expected non-nil connector")
	}
	if connector.config == nil {
		t.Fatal("expected non-nil config")
	}
	if connector.config.ClientID != "client-id" {
		t.Errorf("expected client ID 'client-id', got %q", connector.config.ClientID)
	}
	if connector.config.ClientSecret != "client-secret" {
		t.Errorf("expected client secret 'client-secret', got %q", connector.config.ClientSecret)
	}
	if connector.config.RedirectURL != "http://localhost/callback" {
		t.Errorf("expected redirect URL 'http://localhost/callback', got %q", connector.config.RedirectURL)
	}
	if len(connector.config.Scopes) != 1 || connector.config.Scopes[0] != gmail.GmailReadonlyScope {
		t.Errorf("expected readonly scope, got %v", connector.config.Scopes)
	}
	if connector.connected {
		t.Error("expected connector to be disconnected initially")
	}
}

func TestGmailConnector_SetToken(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	token := &oauth2.Token{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	}

	connector.SetToken(token)

	if connector.token == nil {
		t.Fatal("expected token to be set")
	}
	if connector.token.AccessToken != "access-token" {
		t.Errorf("expected access token 'access-token', got %q", connector.token.AccessToken)
	}
}

func TestGmailConnector_GetAuthURL(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	url := connector.GetAuthURL("test-state")

	if url == "" {
		t.Fatal("expected non-empty auth URL")
	}
	if !containsString(url, "client_id=client-id") {
		t.Error("auth URL should contain client_id")
	}
	if !containsString(url, "redirect_uri=http") {
		t.Error("auth URL should contain redirect_uri")
	}
	if !containsString(url, "state=test-state") {
		t.Error("auth URL should contain state")
	}
	if !containsString(url, "access_type=offline") {
		t.Error("auth URL should contain access_type=offline")
	}
}

func TestGmailConnector_IsConnected(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	if connector.IsConnected() {
		t.Error("expected IsConnected to return false initially")
	}

	// Manually set connected for testing
	connector.mu.Lock()
	connector.connected = true
	connector.mu.Unlock()

	if !connector.IsConnected() {
		t.Error("expected IsConnected to return true after setting connected")
	}
}

func TestGmailConnector_Disconnect(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	// Manually set connected for testing
	connector.mu.Lock()
	connector.connected = true
	connector.mu.Unlock()

	err := connector.Disconnect()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if connector.IsConnected() {
		t.Error("expected connector to be disconnected after Disconnect")
	}
}

func TestGmailConnector_Connect_NoToken(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	err := connector.Connect(context.Background())

	if err != mailpkg.ErrInvalidCredentials {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestGmailConnector_ListThreads_NotConnected(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	_, err := connector.ListThreads(context.Background(), mailpkg.ListOptions{})

	if err != mailpkg.ErrNotConnected {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}

func TestGmailConnector_GetThread_NotConnected(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	_, err := connector.GetThread(context.Background(), "thread-id")

	if err != mailpkg.ErrNotConnected {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}

func TestGmailConnector_GetMessages_NotConnected(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	_, err := connector.GetMessages(context.Background(), "thread-id")

	if err != mailpkg.ErrNotConnected {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}

func TestBuildQuery(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	tests := []struct {
		name     string
		opts     mailpkg.ListOptions
		expected string
	}{
		{
			name:     "empty options",
			opts:     mailpkg.ListOptions{},
			expected: "",
		},
		{
			name: "mailbox only",
			opts: mailpkg.ListOptions{
				MailboxID: "INBOX",
			},
			expected: "in:inbox",
		},
		{
			name: "since date",
			opts: mailpkg.ListOptions{
				Since: timePtr(time.Unix(1609459200, 0)), // 2021-01-01 00:00:00 UTC
			},
			expected: "after:1609459200",
		},
		{
			name: "before date",
			opts: mailpkg.ListOptions{
				Before: timePtr(time.Unix(1640995200, 0)), // 2022-01-01 00:00:00 UTC
			},
			expected: "before:1640995200",
		},
		{
			name: "all options",
			opts: mailpkg.ListOptions{
				Since:     timePtr(time.Unix(1609459200, 0)),
				Before:    timePtr(time.Unix(1640995200, 0)),
				MailboxID: "sent",
			},
			expected: "after:1609459200 before:1640995200 in:sent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := connector.buildQuery(tt.opts)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestMapMailboxToLabel(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	tests := []struct {
		mailbox  string
		expected string
	}{
		{"INBOX", "inbox"},
		{"inbox", "inbox"},
		{"Sent", "sent"},
		{"SENT", "sent"},
		{"Drafts", "drafts"},
		{"Trash", "trash"},
		{"Spam", "spam"},
		{"Starred", "starred"},
		{"Important", "important"},
		{"Archive", "all"},
		{"All Mail", "all"},
		{"CustomLabel", "CustomLabel"},
	}

	for _, tt := range tests {
		t.Run(tt.mailbox, func(t *testing.T) {
			result := connector.mapMailboxToLabel(tt.mailbox)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestExtractEmailAddress(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"user@example.com", "user@example.com"},
		{"John Doe <john@example.com>", "john@example.com"},
		{"<jane@example.com>", "jane@example.com"},
		{"\"John Doe\" <john@example.com>", "john@example.com"},
		{"not an email", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := extractEmailAddress(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestExtractEmailAddresses(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{
			"user@example.com",
			[]string{"user@example.com"},
		},
		{
			"John Doe <john@example.com>, Jane Doe <jane@example.com>",
			[]string{"john@example.com", "jane@example.com"},
		},
		{
			"<a@example.com>, <b@example.com>, <c@example.com>",
			[]string{"a@example.com", "b@example.com", "c@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := extractEmailAddresses(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d addresses, got %d", len(tt.expected), len(result))
				return
			}
			for i, addr := range result {
				if addr != tt.expected[i] {
					t.Errorf("expected %q at index %d, got %q", tt.expected[i], i, addr)
				}
			}
		})
	}
}

func TestDecodeBase64URL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple text",
			input:    "SGVsbG8gV29ybGQ=",
			expected: "Hello World",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := decodeBase64URL(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestConvertThread(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	// Create a mock Gmail thread
	gmailThread := &gmail.Thread{
		Id: "thread-123",
		Messages: []*gmail.Message{
			{
				Id:           "msg-1",
				InternalDate: 1609459200000, // 2021-01-01 00:00:00 UTC in ms
				LabelIds:     []string{"INBOX", "UNREAD"},
				Payload: &gmail.MessagePart{
					Headers: []*gmail.MessagePartHeader{
						{Name: "Subject", Value: "Test Subject"},
						{Name: "From", Value: "sender@example.com"},
						{Name: "To", Value: "recipient@example.com"},
					},
				},
			},
			{
				Id:           "msg-2",
				InternalDate: 1609545600000, // 2021-01-02 00:00:00 UTC in ms
				Payload: &gmail.MessagePart{
					Headers: []*gmail.MessagePartHeader{
						{Name: "Subject", Value: "Re: Test Subject"},
						{Name: "From", Value: "recipient@example.com"},
						{Name: "To", Value: "sender@example.com"},
					},
				},
			},
		},
	}

	thread := connector.convertThread(gmailThread)

	if thread.ID != "thread-123" {
		t.Errorf("expected thread ID 'thread-123', got %q", thread.ID)
	}
	if thread.Subject != "Test Subject" {
		t.Errorf("expected subject 'Test Subject', got %q", thread.Subject)
	}
	if thread.MessageCount != 2 {
		t.Errorf("expected 2 messages, got %d", thread.MessageCount)
	}
	if len(thread.Participants) != 2 {
		t.Errorf("expected 2 participants, got %d", len(thread.Participants))
	}
	if len(thread.Labels) != 2 {
		t.Errorf("expected 2 labels, got %d", len(thread.Labels))
	}

	// Check date range
	expectedOldest := time.Unix(1609459200, 0)
	expectedNewest := time.Unix(1609545600, 0)
	if !thread.DateRange[0].Equal(expectedOldest) {
		t.Errorf("expected oldest date %v, got %v", expectedOldest, thread.DateRange[0])
	}
	if !thread.DateRange[1].Equal(expectedNewest) {
		t.Errorf("expected newest date %v, got %v", expectedNewest, thread.DateRange[1])
	}
}

func TestConvertMessage(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	gmailMsg := &gmail.Message{
		Id:           "msg-123",
		InternalDate: 1609459200000,
		Payload: &gmail.MessagePart{
			MimeType: "text/plain",
			Headers: []*gmail.MessagePartHeader{
				{Name: "Subject", Value: "Test Subject"},
				{Name: "From", Value: "sender@example.com"},
				{Name: "To", Value: "recipient@example.com"},
				{Name: "Cc", Value: "cc@example.com"},
			},
			Body: &gmail.MessagePartBody{
				Data: "SGVsbG8gV29ybGQ=", // "Hello World" in base64
			},
		},
	}

	msg := connector.convertMessage(context.Background(), nil, gmailMsg, "thread-123")

	if msg.ID != "msg-123" {
		t.Errorf("expected message ID 'msg-123', got %q", msg.ID)
	}
	if msg.ThreadID != "thread-123" {
		t.Errorf("expected thread ID 'thread-123', got %q", msg.ThreadID)
	}
	if msg.Subject != "Test Subject" {
		t.Errorf("expected subject 'Test Subject', got %q", msg.Subject)
	}
	if msg.From != "sender@example.com" {
		t.Errorf("expected from 'sender@example.com', got %q", msg.From)
	}
	if len(msg.To) != 1 || msg.To[0] != "recipient@example.com" {
		t.Errorf("expected to ['recipient@example.com'], got %v", msg.To)
	}
	if len(msg.Cc) != 1 || msg.Cc[0] != "cc@example.com" {
		t.Errorf("expected cc ['cc@example.com'], got %v", msg.Cc)
	}
	if msg.Body != "Hello World" {
		t.Errorf("expected body 'Hello World', got %q", msg.Body)
	}
}

func TestExtractBody_Multipart(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	payload := &gmail.MessagePart{
		MimeType: "multipart/alternative",
		Parts: []*gmail.MessagePart{
			{
				MimeType: "text/plain",
				Body: &gmail.MessagePartBody{
					Data: "UGxhaW4gdGV4dA==", // "Plain text"
				},
			},
			{
				MimeType: "text/html",
				Body: &gmail.MessagePartBody{
					Data: "PGI+SFRNTDWVYD4=", // "<b>HTML</b>" (approximately)
				},
			},
		},
	}

	plain, html := connector.extractBody(payload)

	if plain != "Plain text" {
		t.Errorf("expected plain text 'Plain text', got %q", plain)
	}
	if html == "" {
		t.Error("expected non-empty HTML body")
	}
}

func TestExtractAttachments(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	payload := &gmail.MessagePart{
		MimeType: "multipart/mixed",
		Parts: []*gmail.MessagePart{
			{
				MimeType: "text/plain",
				Body: &gmail.MessagePartBody{
					Data: "VGV4dA==",
				},
			},
			{
				Filename: "document.pdf",
				MimeType: "application/pdf",
				Body: &gmail.MessagePartBody{
					AttachmentId: "attach-123",
					Size:         1024,
				},
			},
		},
	}

	attachments := connector.extractAttachments(payload)

	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].ID != "attach-123" {
		t.Errorf("expected attachment ID 'attach-123', got %q", attachments[0].ID)
	}
	if attachments[0].Filename != "document.pdf" {
		t.Errorf("expected filename 'document.pdf', got %q", attachments[0].Filename)
	}
	if attachments[0].MimeType != "application/pdf" {
		t.Errorf("expected mime type 'application/pdf', got %q", attachments[0].MimeType)
	}
	if attachments[0].Size != 1024 {
		t.Errorf("expected size 1024, got %d", attachments[0].Size)
	}
}

func TestHasAttachmentsInPart(t *testing.T) {
	tests := []struct {
		name     string
		part     *gmail.MessagePart
		expected bool
	}{
		{
			name:     "nil part",
			part:     nil,
			expected: false,
		},
		{
			name: "text only",
			part: &gmail.MessagePart{
				MimeType: "text/plain",
				Body:     &gmail.MessagePartBody{Data: "test"},
			},
			expected: false,
		},
		{
			name: "with attachment",
			part: &gmail.MessagePart{
				Filename: "file.pdf",
				MimeType: "application/pdf",
				Body: &gmail.MessagePartBody{
					AttachmentId: "123",
				},
			},
			expected: true,
		},
		{
			name: "nested attachment",
			part: &gmail.MessagePart{
				MimeType: "multipart/mixed",
				Parts: []*gmail.MessagePart{
					{
						MimeType: "text/plain",
						Body:     &gmail.MessagePartBody{Data: "test"},
					},
					{
						Filename: "file.pdf",
						MimeType: "application/pdf",
						Body: &gmail.MessagePartBody{
							AttachmentId: "123",
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasAttachmentsInPart(tt.part)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestGmailConnector_ConcurrentAccess(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	// Test concurrent SetToken and IsConnected
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			connector.SetToken(&oauth2.Token{AccessToken: "test"})
			done <- true
		}()
		go func() {
			_ = connector.IsConnected()
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}
}

// Helper functions

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func TestGmailConnector_ListLabels_NotConnected(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	_, err := connector.ListLabels(context.Background())

	if err != mailpkg.ErrNotConnected {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}

func TestGmailConnector_ImplementsLabelLister(t *testing.T) {
	connector := NewGmailConnector("client-id", "client-secret", "http://localhost/callback")

	// Verify that GmailConnector implements LabelLister interface
	var _ mailpkg.LabelLister = connector
}
