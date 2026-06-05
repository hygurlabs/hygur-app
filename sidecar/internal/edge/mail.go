package edge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/mail"
)

// MailSync is the edge mail-puller (C7-E5, approach B): given an already-connected
// mail.MailConnector (e.g. Proton Bridge over local IMAP), it lists threads since
// the watermark, extracts each message's plain text on-device, and PUSHES it to
// the central server. It deliberately bypasses the local EmailIndexer (store +
// embeddings) — only text crosses the wire; the center indexes + embeds.
//
// Idempotent per message (source_ref "<provider>:<id>"); the IMAP SINCE filter is
// date-granular, so a re-fetched same-day message is a server-side "duplicate"
// no-op. Watermark = the newest message Date pushed.
type MailSync struct {
	client   *Client
	provider string // "proton" | "mailapp" → source_ref prefix
}

// NewMailSync wires a mail puller to a push client. provider prefixes source_refs.
func NewMailSync(client *Client, provider string) *MailSync {
	if provider == "" {
		provider = "mail"
	}
	return &MailSync{client: client, provider: provider}
}

// MailStats reports a run's outcome; Newest is the latest message date pushed
// (the new watermark).
type MailStats struct {
	Pushed  int
	Errors  int
	Threads int
	Newest  time.Time
}

// Run pulls threads (across mailboxes) modified since `since`, extracts message
// text, and pushes each. Per-thread/message errors are counted, not fatal.
func (ms *MailSync) Run(ctx context.Context, conn mail.MailConnector, mailboxes []string, since time.Time) (MailStats, error) {
	st := MailStats{Newest: since}
	if len(mailboxes) == 0 {
		mailboxes = []string{""} // provider default (all)
	}
	var sincePtr *time.Time
	if !since.IsZero() {
		sincePtr = &since
	}

	for _, mbox := range mailboxes {
		if err := ctx.Err(); err != nil {
			return st, err
		}
		threads, err := conn.ListThreads(ctx, mail.ListOptions{Since: sincePtr, MailboxID: mbox})
		if err != nil {
			st.Errors++
			continue
		}
		for i := range threads {
			st.Threads++
			msgs, err := conn.GetMessagesByThread(ctx, &threads[i])
			if err != nil {
				st.Errors++
				continue
			}
			for _, m := range msgs {
				if err := ctx.Err(); err != nil {
					return st, err
				}
				if strings.TrimSpace(m.Body) == "" {
					continue // nothing to index beyond headers (HTML-only fallback = later)
				}
				text := messageText(m)
				in := IngestText{
					Title:      m.Subject,
					Text:       text,
					SourceType: "mail",
					SourceRef:  ms.provider + ":" + m.ID,
					Author:     m.From,
					Metadata:   map[string]any{"from": m.From, "mailbox": mbox, "provider": ms.provider},
				}
				if !m.Date.IsZero() {
					in.CreatedAt = m.Date.UTC().Format(time.RFC3339)
				}
				if _, err := ms.client.PushText(ctx, in); err != nil {
					st.Errors++
					continue
				}
				st.Pushed++
				if m.Date.After(st.Newest) {
					st.Newest = m.Date
				}
			}
		}
	}
	return st, nil
}

// messageText assembles the plain text the center will index: subject + sender +
// body (the connector already provides Body as plain text).
func messageText(m mail.Message) string {
	var b strings.Builder
	if m.Subject != "" {
		fmt.Fprintf(&b, "Subject: %s\n", m.Subject)
	}
	if m.From != "" {
		fmt.Fprintf(&b, "From: %s\n", m.From)
	}
	if !m.Date.IsZero() {
		fmt.Fprintf(&b, "Date: %s\n", m.Date.Format(time.RFC3339))
	}
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	b.WriteString(m.Body)
	return b.String()
}
