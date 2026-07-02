package edge

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/mail"
)

// stubParser stands in for the PDF parser so attachment linking can be tested
// without a real PDF byte stream.
type stubParser struct{ text string }

func (s stubParser) SupportedExtensions() []string { return []string{".pdf"} }
func (s stubParser) Parse(context.Context, io.Reader) (string, ingest.Metadata, error) {
	return s.text, nil, nil
}

// mockMail is a canned MailConnector for the edge puller test.
type mockMail struct {
	threads  []mail.Thread
	msgs     map[string][]mail.Message
	lastOpts []mail.ListOptions // records each ListThreads call
}

func (m *mockMail) Connect(context.Context) error { return nil }
func (m *mockMail) Disconnect() error             { return nil }
func (m *mockMail) IsConnected() bool             { return true }
func (m *mockMail) ListThreads(_ context.Context, opts mail.ListOptions) ([]mail.Thread, error) {
	m.lastOpts = append(m.lastOpts, opts)
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
	st, state, err := ms.Run(context.Background(), conn, []string{"INBOX"}, nil, 200)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !state["INBOX"].Equal(date) {
		t.Errorf("folder watermark = %v, want %v", state["INBOX"], date)
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

// TestMailSync_BackfillThenIncremental verifies the per-folder model: an unknown
// folder fetches the most recent N (Limit set, no Since); a folder already in the
// state syncs incrementally (Since set, no Limit).
func TestMailSync_BackfillThenIncremental(t *testing.T) {
	srv, _, _ := captureServer(t)
	date := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	conn := &mockMail{
		threads: []mail.Thread{{ID: "t1"}},
		msgs:    map[string][]mail.Message{"t1": {{ID: "m1", Subject: "Hi", Body: "body", Date: date}}},
	}
	ms := NewMailSync(NewClient(srv.URL, "tok"), "proton")

	// First sync: folder unknown → backfill of N, no Since.
	_, state, err := ms.Run(context.Background(), conn, []string{"INBOX"}, nil, 150)
	if err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	o1 := conn.lastOpts[len(conn.lastOpts)-1]
	if o1.Limit != 150 || o1.Since != nil {
		t.Errorf("backfill: got Limit=%d Since=%v, want Limit=150 Since=nil", o1.Limit, o1.Since)
	}
	if !state["INBOX"].Equal(date) {
		t.Fatalf("watermark not recorded: %v", state["INBOX"])
	}

	// Second sync: folder known → incremental, Since = watermark, no Limit.
	conn.lastOpts = nil
	_, _, err = ms.Run(context.Background(), conn, []string{"INBOX"}, state, 150)
	if err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	o2 := conn.lastOpts[len(conn.lastOpts)-1]
	if o2.Limit != 0 || o2.Since == nil || !o2.Since.Equal(date) {
		t.Errorf("incremental: got Limit=%d Since=%v, want Limit=0 Since=%v", o2.Limit, o2.Since, date)
	}
}

// TestMailSync_LinksPDFAttachment verifies a PDF attachment is pushed as its own
// knowledge item, titled with the mail subject (so a subject search surfaces it)
// and carrying parent metadata that records the link to the mail.
func TestMailSync_LinksPDFAttachment(t *testing.T) {
	srv, got, _ := captureServer(t)
	date := time.Date(2026, 4, 15, 18, 23, 0, 0, time.UTC)
	conn := &mockMail{
		threads: []mail.Thread{{ID: "t1"}},
		msgs: map[string][]mail.Message{
			"t1": {{
				ID: "m1", Subject: "Déclaration TVA [FID1266295]", From: "acct@example.com",
				HTMLBody: "<p>Votre déclaration</p>", Date: date,
				Attachments: []mail.Attachment{
					{Filename: "declaration.pdf", MimeType: "application/pdf", Data: []byte("%PDF-1.4 fake")},
					{Filename: "logo.png", MimeType: "image/png", Data: []byte("PNG")}, // ignored
				},
			}},
		},
	}
	ms := NewMailSync(NewClient(srv.URL, "tok"), "proton")
	ms.pdf = stubParser{text: "TVA à payer: 1234,56 EUR — FID1266295"}

	st, _, err := ms.Run(context.Background(), conn, []string{"INBOX"}, nil, 200)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Pushed != 2 { // mail body + the PDF; the PNG is ignored
		t.Fatalf("pushed = %d, want 2 (mail + pdf)", st.Pushed)
	}
	byRef := map[string]IngestText{}
	for _, g := range *got {
		byRef[g.SourceRef] = g
	}
	att, ok := byRef["proton:m1:att:declaration.pdf"]
	if !ok {
		t.Fatalf("PDF attachment not pushed: %+v", *got)
	}
	if !strings.Contains(att.Title, "FID1266295") || !strings.Contains(att.Title, "declaration.pdf") {
		t.Errorf("attachment title doesn't link to the mail: %q", att.Title)
	}
	// The attachment is a mail-typed item, so its text is normalized (collapsed +
	// lowercased) exactly as before — mail is unaffected by the raw_text change.
	if !strings.Contains(att.Text, "1234,56 eur") {
		t.Errorf("attachment text not the extracted PDF: %q", att.Text)
	}
	if att.Metadata["parent"] != "proton:m1" || att.Metadata["attachment"] != true {
		t.Errorf("attachment metadata missing link: %+v", att.Metadata)
	}
	if _, ok := byRef["proton:m1:att:logo.png"]; ok {
		t.Errorf("non-PDF attachment should be ignored")
	}
}
