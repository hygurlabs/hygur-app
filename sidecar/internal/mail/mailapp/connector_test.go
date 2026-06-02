package mailapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	mailpkg "github.com/hygur/sidecar/internal/mail"
)

// fakeRunner returns canned outputs and lets tests inspect the captured args.
type fakeRunner struct {
	responses map[string][]byte // keyed by script identity (first 60 chars)
	err       error
	calls     []capturedCall
}

type capturedCall struct {
	scriptHead string
	args       map[string]any
}

func (f *fakeRunner) run(_ context.Context, script string, args map[string]any) ([]byte, error) {
	head := script
	if len(head) > 60 {
		head = head[:60]
	}
	f.calls = append(f.calls, capturedCall{scriptHead: head, args: args})
	if f.err != nil {
		return nil, f.err
	}
	for key, payload := range f.responses {
		if len(key) > 0 && len(script) >= len(key) && script[:len(key)] == key {
			return payload, nil
		}
	}
	return f.responses["*"], nil
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{responses: make(map[string][]byte)}
}

// helper to set the health response keyed on the embedded health.js script.
func (f *fakeRunner) onHealth(running bool, accountCount int) {
	out, _ := json.Marshal(map[string]any{
		"running": running, "accountCount": accountCount, "mailboxCount": accountCount,
	})
	f.responses[scriptHealth[:60]] = out
}

func (f *fakeRunner) onListThreads(threads []rawThread) {
	out, _ := json.Marshal(threads)
	f.responses[scriptListThreads[:60]] = out
}

func (f *fakeRunner) onGetMessages(msgs []rawMessage) {
	out, _ := json.Marshal(msgs)
	f.responses[scriptGetMessages[:60]] = out
}

func (f *fakeRunner) onListAccounts(accts []Account) {
	out, _ := json.Marshal(accts)
	f.responses[scriptListAccounts[:60]] = out
}

// ---------------------------------------------------------------------------

func TestConnect_RejectsMissingMailApp(t *testing.T) {
	r := newFakeRunner()
	r.onHealth(false, 0)
	c := NewConnector("acct-1", "Test").withRunner(r)

	err := c.Connect(context.Background())
	if !errors.Is(err, mailpkg.ErrMailAppNotRunning) {
		t.Fatalf("want ErrMailAppNotRunning, got %v", err)
	}
	if c.IsConnected() {
		t.Fatal("connector should not be marked connected")
	}
}

func TestConnect_SurfacesAccountReadError(t *testing.T) {
	// Mail.app running but account enumeration threw: the script reports
	// accountCount 0 plus an error string. Connect must surface that cause
	// rather than the misleading flat "no accounts configured".
	r := newFakeRunner()
	out, _ := json.Marshal(map[string]any{
		"running": true, "accountCount": 0, "mailboxCount": 0,
		"error": "Error: Application isn't running (-600)",
	})
	r.responses[scriptHealth[:60]] = out
	c := NewConnector("acct-1", "Test").withRunner(r)

	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected an error when 0 accounts are reported")
	}
	if got := err.Error(); !strings.Contains(got, "cannot read accounts") || !strings.Contains(got, "(-600)") {
		t.Fatalf("error should surface the underlying cause, got %q", got)
	}
}

func TestConnect_ZeroAccountsHint(t *testing.T) {
	r := newFakeRunner()
	r.onHealth(true, 0) // running, genuinely 0 accounts, no error
	c := NewConnector("acct-1", "Test").withRunner(r)

	err := c.Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Automation") {
		t.Fatalf("expected an actionable Automation hint, got %v", err)
	}
}

func TestConnect_PropagatesAutomationDenied(t *testing.T) {
	r := newFakeRunner()
	r.err = mailpkg.ErrAutomationDenied
	c := NewConnector("acct-1", "Test").withRunner(r)

	err := c.Connect(context.Background())
	if !errors.Is(err, mailpkg.ErrAutomationDenied) {
		t.Fatalf("want ErrAutomationDenied, got %v", err)
	}
}

func TestConnect_OK(t *testing.T) {
	r := newFakeRunner()
	r.onHealth(true, 3)
	c := NewConnector("acct-1", "Test").withRunner(r)

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !c.IsConnected() {
		t.Fatal("connector should be connected")
	}
}

func TestListThreads_NotConnected(t *testing.T) {
	r := newFakeRunner()
	c := NewConnector("acct-1", "Test").withRunner(r)
	_, err := c.ListThreads(context.Background(), mailpkg.ListOptions{})
	if !errors.Is(err, mailpkg.ErrNotConnected) {
		t.Fatalf("want ErrNotConnected, got %v", err)
	}
}

func TestListThreads_ParsesAndPropagatesArgs(t *testing.T) {
	r := newFakeRunner()
	r.onHealth(true, 1)
	r.onListThreads([]rawThread{
		{
			ID: "msg-1@example.com", Subject: "Hello", Participants: []string{"alice@example.com"},
			DateStart: "2026-04-01T08:00:00Z", DateEnd: "2026-04-01T08:00:00Z",
			MessageCount: 1, MessageIDs: []uint32{42}, Mailbox: "INBOX",
		},
	})
	c := NewConnector("acct-1", "Test").withRunner(r)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	since := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	threads, err := c.ListThreads(context.Background(), mailpkg.ListOptions{
		MailboxID: "Sent", Since: &since, Limit: 10, Offset: 5,
	})
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(threads) != 1 {
		t.Fatalf("want 1 thread, got %d", len(threads))
	}
	if threads[0].ID != "msg-1@example.com" {
		t.Fatalf("unexpected thread ID: %s", threads[0].ID)
	}
	if threads[0].Subject != "Hello" {
		t.Fatalf("unexpected subject")
	}
	if len(threads[0].MessageUIDs) != 1 || threads[0].MessageUIDs[0] != 42 {
		t.Fatalf("unexpected MessageUIDs")
	}
	if threads[0].Mailbox != "INBOX" {
		t.Fatalf("unexpected mailbox: %s", threads[0].Mailbox)
	}

	// Verify args propagation on the list_threads call.
	var listCall *capturedCall
	for i := range r.calls {
		if r.calls[i].args["mailboxName"] != nil {
			listCall = &r.calls[i]
			break
		}
	}
	if listCall == nil {
		t.Fatal("no list_threads call captured")
	}
	if listCall.args["mailboxName"] != "Sent" {
		t.Fatalf("mailboxName not propagated, got %v", listCall.args["mailboxName"])
	}
	if listCall.args["since"] != "2026-04-01T00:00:00Z" {
		t.Fatalf("since not propagated, got %v", listCall.args["since"])
	}
	if listCall.args["limit"] != 10 {
		t.Fatalf("limit not propagated, got %v", listCall.args["limit"])
	}
	if listCall.args["accountId"] != "acct-1" {
		t.Fatalf("accountId not propagated, got %v", listCall.args["accountId"])
	}
}

func TestGetMessagesByThread_FetchesAndMaps(t *testing.T) {
	r := newFakeRunner()
	r.onHealth(true, 1)
	r.onGetMessages([]rawMessage{
		{
			ID: 42, MsgID: "msg-1@example.com", Subject: "Hello",
			From: "alice@example.com", Date: "2026-04-01T08:00:00Z",
			Body: "  Hello body  ",
			Attachments: []rawAttachment{
				{Filename: "a.pdf", MimeType: "application/pdf", Size: 1024},
			},
		},
	})
	c := NewConnector("acct-1", "Test").withRunner(r)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	thread := &mailpkg.Thread{
		ID: "msg-1@example.com", MessageUIDs: []uint32{42}, Mailbox: "INBOX",
	}
	msgs, err := c.GetMessagesByThread(context.Background(), thread)
	if err != nil {
		t.Fatalf("GetMessagesByThread: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	m := msgs[0]
	if m.ID != "msg-1@example.com" {
		t.Fatalf("unexpected message ID: %s", m.ID)
	}
	if m.ThreadID != thread.ID {
		t.Fatalf("ThreadID not threaded through: %s", m.ThreadID)
	}
	if m.Body != "Hello body" {
		t.Fatalf("body not trimmed: %q", m.Body)
	}
	if m.From != "alice@example.com" {
		t.Fatalf("From not preserved")
	}
	if len(m.Attachments) != 1 || m.Attachments[0].Filename != "a.pdf" {
		t.Fatalf("attachments not mapped")
	}
	if m.Date.IsZero() || m.Date.Year() != 2026 {
		t.Fatalf("date not parsed, got %v", m.Date)
	}
}

func TestGetMessagesByThread_RejectsEmptyThread(t *testing.T) {
	r := newFakeRunner()
	r.onHealth(true, 1)
	c := NewConnector("acct-1", "Test").withRunner(r)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	_, err := c.GetMessagesByThread(context.Background(), &mailpkg.Thread{ID: "x"})
	if !errors.Is(err, mailpkg.ErrThreadNotFound) {
		t.Fatalf("want ErrThreadNotFound on empty thread, got %v", err)
	}
}

func TestGetThread_LooksUpInListing(t *testing.T) {
	r := newFakeRunner()
	r.onHealth(true, 1)
	r.onListThreads([]rawThread{
		{ID: "a", Subject: "A", DateStart: "2026-04-01T00:00:00Z", DateEnd: "2026-04-01T00:00:00Z", MessageIDs: []uint32{1}},
		{ID: "b", Subject: "B", DateStart: "2026-04-02T00:00:00Z", DateEnd: "2026-04-02T00:00:00Z", MessageIDs: []uint32{2}},
	})
	c := NewConnector("acct-1", "Test").withRunner(r)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	th, err := c.GetThread(context.Background(), "b")
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if th.Subject != "B" {
		t.Fatalf("wrong thread returned: %s", th.Subject)
	}

	_, err = c.GetThread(context.Background(), "missing")
	if !errors.Is(err, mailpkg.ErrThreadNotFound) {
		t.Fatalf("want ErrThreadNotFound, got %v", err)
	}
}

func TestDiscoverAccounts(t *testing.T) {
	r := newFakeRunner()
	r.onListAccounts([]Account{
		{ID: "uuid-1", Name: "iCloud", EmailAddresses: []string{"a@icloud.com"}, MailboxNames: []string{"INBOX"}},
	})

	out, err := discoverAccountsWith(context.Background(), r)
	if err != nil {
		t.Fatalf("DiscoverAccounts: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 account, got %d", len(out))
	}
	if out[0].PrimaryEmail() != "a@icloud.com" {
		t.Fatalf("PrimaryEmail wrong: %s", out[0].PrimaryEmail())
	}
}

func TestClassifyOsascriptError(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		want   error
	}{
		{"automation denied", "12:1: execution error: blah (-1743)", mailpkg.ErrAutomationDenied},
		{"app not running", "12:1: execution error: blah (-600)", mailpkg.ErrMailAppNotRunning},
		{"timeout", "12:1: execution error: blah (-1712)", mailpkg.ErrTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := classifyOsascriptError(errors.New("exit 1"), tc.stderr)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}
