// Package mailapp implements a mailpkg.MailConnector that reads messages from
// the user's local Mail.app via Apple Events (JXA scripts run through
// /usr/bin/osascript). It requires no credentials and no Full Disk Access — it
// only needs the user to grant the Hygur process Automation permission for
// Mail.app the first time it is used.
package mailapp

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"sync"
	"time"

	mailpkg "github.com/hygur/sidecar/internal/mail"
)

//go:embed scripts/health.js
var scriptHealth string

//go:embed scripts/list_accounts.js
var scriptListAccounts string

//go:embed scripts/discover_mailboxes.js
var scriptDiscoverMailboxes string

//go:embed scripts/list_threads.js
var scriptListThreads string

//go:embed scripts/get_messages.js
var scriptGetMessages string

// Connector is the Mail.app implementation of mailpkg.MailConnector.
//
// One Connector instance corresponds to one Mail.app account (identified by
// Mail.app's persistent UUID). The default mailbox name is "INBOX" but can be
// overridden via the MailboxID field of mailpkg.ListOptions on a per-call
// basis.
type Connector struct {
	accountID    string // Mail.app account UUID, e.g. "87C92CD1-...-..."
	accountName  string
	defaultMbox  string

	mu        sync.Mutex
	connected bool
	r         runner
}

// NewConnector returns a Connector for the given Mail.app account UUID.
// accountName is informational only (used for logging). The connector
// targets "INBOX" by default; callers can override via ListOptions.MailboxID.
func NewConnector(accountID, accountName string) *Connector {
	return &Connector{
		accountID:   accountID,
		accountName: accountName,
		defaultMbox: "INBOX",
		r:           newOsascriptRunner(),
	}
}

// withRunner is a test helper that swaps the underlying osascript runner.
func (c *Connector) withRunner(r runner) *Connector { c.r = r; return c }

// Connect runs the health-check script. It verifies that Mail.app is running
// and that the host process has Automation permission. Failures are mapped to
// mailpkg sentinel errors via classifyOsascriptError.
func (c *Connector) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var hr struct {
		Running       bool   `json:"running"`
		AccountCount  int    `json:"accountCount"`
		MailboxCount  int    `json:"mailboxCount"`
		Error         string `json:"error,omitempty"`
	}
	if err := runJSON(ctx, c.r, scriptHealth, nil, &hr); err != nil {
		return err
	}
	if !hr.Running {
		return mailpkg.ErrMailAppNotRunning
	}
	if hr.AccountCount == 0 {
		return fmt.Errorf("mail.app: no accounts configured")
	}
	c.connected = true
	return nil
}

// Disconnect releases the connector. There is no live socket to close — this
// just flips the local flag.
func (c *Connector) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = false
	return nil
}

// IsConnected reports whether Connect succeeded since the last Disconnect.
func (c *Connector) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// ListThreads lists threads for this connector's account and the requested
// mailbox (defaulting to INBOX). Threads are derived by grouping messages
// with the same normalised subject within the mailbox; each Thread.ID is the
// RFC822 Message-Id of one of the messages in the group.
func (c *Connector) ListThreads(ctx context.Context, opts mailpkg.ListOptions) ([]mailpkg.Thread, error) {
	if !c.IsConnected() {
		return nil, mailpkg.ErrNotConnected
	}

	mbox := opts.MailboxID
	if mbox == "" {
		mbox = c.defaultMbox
	}

	args := map[string]any{
		"accountId":   c.accountID,
		"mailboxName": mbox,
	}
	if opts.Since != nil {
		args["since"] = opts.Since.UTC().Format(time.RFC3339)
	}
	if opts.Before != nil {
		args["before"] = opts.Before.UTC().Format(time.RFC3339)
	}
	if opts.Limit > 0 {
		args["limit"] = opts.Limit
	}
	if opts.Offset > 0 {
		args["offset"] = opts.Offset
	}

	var raw []rawThread
	if err := runJSON(ctx, c.r, scriptListThreads, args, &raw); err != nil {
		return nil, err
	}

	out := make([]mailpkg.Thread, 0, len(raw))
	for _, t := range raw {
		thread, err := t.toThread(mbox)
		if err != nil {
			continue
		}
		out = append(out, thread)
	}
	return out, nil
}

// GetThread returns metadata for a single thread, identified by the Thread.ID
// previously produced by ListThreads. The implementation re-runs ListThreads
// on the default mailbox and filters — Mail.app does not expose threads as
// first-class objects, so a fresh scan is required.
func (c *Connector) GetThread(ctx context.Context, threadID string) (*mailpkg.Thread, error) {
	if !c.IsConnected() {
		return nil, mailpkg.ErrNotConnected
	}
	threads, err := c.ListThreads(ctx, mailpkg.ListOptions{MailboxID: c.defaultMbox})
	if err != nil {
		return nil, err
	}
	for i := range threads {
		if threads[i].ID == threadID {
			return &threads[i], nil
		}
	}
	return nil, mailpkg.ErrThreadNotFound
}

// GetMessages returns the messages of a thread by id. Callers that already
// hold a Thread struct should prefer GetMessagesByThread, which avoids the
// extra ListThreads round-trip.
func (c *Connector) GetMessages(ctx context.Context, threadID string) ([]mailpkg.Message, error) {
	thread, err := c.GetThread(ctx, threadID)
	if err != nil {
		return nil, err
	}
	return c.GetMessagesByThread(ctx, thread)
}

// GetMessagesByThread fetches all messages of the given thread using the
// Mail.app integer IDs cached in Thread.MessageUIDs. This is the preferred
// path — a single Apple Events call regardless of thread size.
func (c *Connector) GetMessagesByThread(ctx context.Context, thread *mailpkg.Thread) ([]mailpkg.Message, error) {
	if !c.IsConnected() {
		return nil, mailpkg.ErrNotConnected
	}
	if thread == nil || len(thread.MessageUIDs) == 0 {
		return nil, mailpkg.ErrThreadNotFound
	}

	mbox := thread.Mailbox
	if mbox == "" {
		mbox = c.defaultMbox
	}

	ids := make([]uint32, 0, len(thread.MessageUIDs))
	ids = append(ids, thread.MessageUIDs...)

	args := map[string]any{
		"ids":         ids,
		"accountId":   c.accountID,
		"mailboxName": mbox,
	}

	var raw []rawMessage
	if err := runJSON(ctx, c.r, scriptGetMessages, args, &raw); err != nil {
		return nil, err
	}

	out := make([]mailpkg.Message, 0, len(raw))
	for _, m := range raw {
		if m.Error != "" {
			continue
		}
		out = append(out, m.toMessage(thread.ID))
	}
	return out, nil
}

// rawThread mirrors the JSON output of scripts/list_threads.js.
type rawThread struct {
	ID           string   `json:"id"`
	Subject      string   `json:"subject"`
	Participants []string `json:"participants"`
	DateStart    string   `json:"dateStart"`
	DateEnd      string   `json:"dateEnd"`
	MessageCount int      `json:"messageCount"`
	MessageIDs   []uint32 `json:"messageIds"`
	Mailbox      string   `json:"mailbox"`
}

func (r rawThread) toThread(defaultMbox string) (mailpkg.Thread, error) {
	start, err := time.Parse(time.RFC3339, r.DateStart)
	if err != nil {
		return mailpkg.Thread{}, fmt.Errorf("parse dateStart: %w", err)
	}
	end, err := time.Parse(time.RFC3339, r.DateEnd)
	if err != nil {
		return mailpkg.Thread{}, fmt.Errorf("parse dateEnd: %w", err)
	}
	mbox := r.Mailbox
	if mbox == "" {
		mbox = defaultMbox
	}
	return mailpkg.Thread{
		ID:           r.ID,
		Subject:      r.Subject,
		Participants: r.Participants,
		DateRange:    [2]time.Time{start, end},
		MessageCount: r.MessageCount,
		MessageUIDs:  r.MessageIDs,
		Mailbox:      mbox,
	}, nil
}

// rawMessage mirrors the JSON output of scripts/get_messages.js.
type rawMessage struct {
	ID          uint32         `json:"id"`
	MsgID       string         `json:"msgId"`
	Subject     string         `json:"subject"`
	From        string         `json:"from"`
	ReplyTo     string         `json:"replyTo"`
	Date        string         `json:"date"`
	Body        string         `json:"body"`
	Source      string         `json:"source"`
	Attachments []rawAttachment `json:"attachments"`
	AccountID   string         `json:"accountId"`
	Mailbox     string         `json:"mailbox"`
	Error       string         `json:"error,omitempty"`
}

type rawAttachment struct {
	Filename string `json:"filename"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
}

func (r rawMessage) toMessage(threadID string) mailpkg.Message {
	var date time.Time
	if r.Date != "" {
		if t, err := time.Parse(time.RFC3339, r.Date); err == nil {
			date = t
		}
	}

	atts := make([]mailpkg.Attachment, 0, len(r.Attachments))
	for i, a := range r.Attachments {
		atts = append(atts, mailpkg.Attachment{
			ID:       fmt.Sprintf("%d-%d", r.ID, i),
			Filename: a.Filename,
			MimeType: a.MimeType,
			Size:     a.Size,
		})
	}

	id := r.MsgID
	if id == "" {
		id = fmt.Sprintf("mailapp:%d", r.ID)
	}

	return mailpkg.Message{
		ID:          id,
		ThreadID:    threadID,
		From:        r.From,
		Date:        date,
		Subject:     r.Subject,
		Body:        strings.TrimSpace(r.Body),
		Attachments: atts,
	}
}
