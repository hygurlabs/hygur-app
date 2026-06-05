package edge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/mail"
)

// mockMail is a canned MailConnector for the edge puller test.
type mockMail struct {
	threads []mail.Thread
	msgs    map[string][]mail.Message
}

func (m *mockMail) Connect(context.Context) error  { return nil }
func (m *mockMail) Disconnect() error               { return nil }
func (m *mockMail) IsConnected() bool               { return true }
func (m *mockMail) ListThreads(_ context.Context, _ mail.ListOptions) ([]mail.Thread, error) {
	return m.threads, nil
}
func (m *mockMail) GetThread(_ context.Context, id string) (*mail.Thread, error) {
	for i := range m.threads {
		if m.threads[i].ID == id {
			return &m.threads[i], nil
		}
	}
	return nil, mail.ErrThreadNotFound
}
func (m *mockMail) GetMessages(_ context.Context, threadID string) ([]mail.Message, error) {
	return m.msgs[threadID], nil
}
func (m *mockMail) GetMessagesByThread(_ context.Context, t *mail.Thread) ([]mail.Message, error) {
	return m.msgs[t.ID], nil
}

func TestMailSync_PushesMessages(t *testing.T) {
	srv, got, _ := captureServer(t)
	date := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	conn := &mockMail{
		threads: []mail.Thread{{ID: "t1"}},
		msgs: map[string][]mail.Message{
			"t1": {
				{ID: "m1", Subject: "Invoice", From: "edf@example.com", Body: "Total: 42 EUR", Date: date},
				{ID: "m2", Subject: "Empty", From: "x@y.com", Body: "   "}, // skipped (blank)
			},
		},
	}
	ms := NewMailSync(NewClient(srv.URL, "tok"), "proton")
	st, err := ms.Run(context.Background(), conn, []string{"INBOX"}, time.Time{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Pushed != 1 {
		t.Fatalf("pushed = %d, want 1 (blank body skipped)", st.Pushed)
	}
	if len(*got) != 1 {
		t.Fatalf("server received %d", len(*got))
	}
	g := (*got)[0]
	if g.SourceRef != "proton:m1" || g.SourceType != "mail" {
		t.Errorf("bad ref/type: %+v", g)
	}
	if g.CreatedAt != date.Format(time.RFC3339) {
		t.Errorf("created_at = %q, want %q", g.CreatedAt, date.Format(time.RFC3339))
	}
	if !strings.Contains(g.Text, "Invoice") || !strings.Contains(g.Text, "Total: 42 EUR") {
		t.Errorf("text missing subject/body: %q", g.Text)
	}
	if !st.Newest.Equal(date) {
		t.Errorf("watermark = %v, want %v", st.Newest, date)
	}
}
