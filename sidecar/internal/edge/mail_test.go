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
				// HTML-only mail (no plain-text part): indexed via stripped HTML so
				// it stays findable — common for statements/invoices.
				{ID: "m2", Subject: "Statement", From: "bank@example.com", HTMLBody: "<html><body><p>Balance: 99 EUR</p></body></html>", Date: date},
				// Wholly empty (no subject, no body, no HTML): the only case skipped.
				{ID: "m3", From: "noise@y.com", Body: "   "},
			},
		},
	}
	ms := NewMailSync(NewClient(srv.URL, "tok"), "proton")
	st, err := ms.Run(context.Background(), conn, []string{"INBOX"}, time.Time{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Pushed != 2 {
		t.Fatalf("pushed = %d, want 2 (only the wholly-empty mail skipped)", st.Pushed)
	}
	if len(*got) != 2 {
		t.Fatalf("server received %d", len(*got))
	}
	byRef := map[string]IngestText{}
	for _, g := range *got {
		byRef[g.SourceRef] = g
		if g.SourceType != "mail" {
			t.Errorf("bad type: %+v", g)
		}
	}
	m1, ok := byRef["proton:m1"]
	if !ok {
		t.Fatalf("m1 not pushed: %+v", *got)
	}
	if m1.CreatedAt != date.Format(time.RFC3339) {
		t.Errorf("created_at = %q, want %q", m1.CreatedAt, date.Format(time.RFC3339))
	}
	if !strings.Contains(m1.Text, "Invoice") || !strings.Contains(m1.Text, "Total: 42 EUR") {
		t.Errorf("m1 text missing subject/body: %q", m1.Text)
	}
	m2, ok := byRef["proton:m2"]
	if !ok {
		t.Fatalf("m2 (HTML-only) not pushed: %+v", *got)
	}
	if !strings.Contains(m2.Text, "Statement") || !strings.Contains(m2.Text, "Balance: 99 EUR") {
		t.Errorf("m2 HTML body not indexed: %q", m2.Text)
	}
	if _, pushed := byRef["proton:m3"]; pushed {
		t.Errorf("m3 (wholly empty) should be skipped")
	}
	if !st.Newest.Equal(date) {
		t.Errorf("watermark = %v, want %v", st.Newest, date)
	}
}
