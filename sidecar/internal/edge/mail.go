package edge

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/ingest/parsers"
	"github.com/hygur/sidecar/internal/mail"
)

// maxAttachmentTextBytes caps the extracted text of a single PDF attachment to
// keep the push payload bounded (the center chunks it anyway).
const maxAttachmentTextBytes = 100_000

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
	norm     *mail.ThreadNormalizer
	pdf      ingest.Parser // extracts embedded text from PDF attachments (no OCR)
}

// NewMailSync wires a mail puller to a push client. provider prefixes source_refs.
func NewMailSync(client *Client, provider string) *MailSync {
	if provider == "" {
		provider = "mail"
	}
	// Normalizer derives indexable text from each message, falling back to the
	// HTML part when there's no plain-text body (HTML-only mail — common for
	// statements/invoices). Headers are added by messageText, so skip metadata here.
	norm := mail.NewThreadNormalizer()
	norm.IncludeMetadata = false
	return &MailSync{client: client, provider: provider, norm: norm, pdf: parsers.NewPDFParserTextOnly()}
}

// MailStats reports a run's outcome; Newest is the latest message date pushed
// across all mailboxes (informational; per-folder watermarks live in the state).
type MailStats struct {
	Pushed  int
	Errors  int
	Threads int
	Newest  time.Time
}

// Run syncs each mailbox independently against its per-folder watermark in
// `state`: a folder ABSENT from the map (never synced) pulls its most recent
// `backfill` messages; a folder present pulls only messages newer than its
// watermark. The map is updated in place and returned so the caller persists it.
// Per-thread/message errors are counted, not fatal.
// spamFolderNames are the well-known spam/junk mailbox names. The MailConnector
// can't enumerate folders, so the spam cross-reference matches by name. Override
// via HYGUR_SPAM_FOLDERS (comma-separated) if a provider names them differently.
var spamFolderNames = []string{"Spam", "Junk"}

const spamScanLimit = 3000 // cap the up-front Spam-folder listing

// isSpamMailbox reports whether a mailbox name is a spam/junk folder.
func isSpamMailbox(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "spam") || strings.Contains(n, "junk")
}

// isCatchAllMailbox reports whether a mailbox is a catch-all that bundles spam in
// with everything else (Proton's "All Mail", or the provider default "").
func isCatchAllMailbox(name string) bool {
	return name == "" || strings.EqualFold(name, "All Mail")
}

// spamThreadIDs gathers the Spam/Junk thread ids ONLY when a catch-all mailbox is
// being synced (Proton surfaces spam inside "All Mail"). Fail-open: a missing or
// mis-named spam folder just yields an empty set, so legitimate mail is never
// dropped by mistake — the guard can only ever omit real spam, never good mail.
func (ms *MailSync) spamThreadIDs(ctx context.Context, conn mail.MailConnector, mailboxes []string) map[string]struct{} {
	out := map[string]struct{}{}
	catchAll := false
	for _, mb := range mailboxes {
		if isCatchAllMailbox(mb) {
			catchAll = true
			break
		}
	}
	if !catchAll {
		return out
	}
	names := spamFolderNames
	if env := strings.TrimSpace(os.Getenv("HYGUR_SPAM_FOLDERS")); env != "" {
		names = nil
		for _, p := range strings.Split(env, ",") {
			if p = strings.TrimSpace(p); p != "" {
				names = append(names, p)
			}
		}
	}
	for _, name := range names {
		th, err := conn.ListThreads(ctx, mail.ListOptions{MailboxID: name, Limit: spamScanLimit})
		if err != nil {
			continue // folder absent / not selectable → skip, no-op
		}
		for i := range th {
			out[th[i].ID] = struct{}{}
		}
	}
	return out
}

func (ms *MailSync) Run(ctx context.Context, conn mail.MailConnector, mailboxes []string, state FolderState, backfill int) (MailStats, FolderState, error) {
	st := MailStats{}
	if state == nil {
		state = FolderState{}
	}
	if len(mailboxes) == 0 {
		mailboxes = []string{""} // provider default (all)
	}

	// Spam guard: when a catch-all ("All Mail") is synced, gather the Spam/Junk
	// thread ids up front and skip them below — Proton flattens spam into All Mail.
	spamThreads := ms.spamThreadIDs(ctx, conn, mailboxes)

	for _, mbox := range mailboxes {
		if err := ctx.Err(); err != nil {
			return st, state, err
		}
		if isSpamMailbox(mbox) {
			continue // never ingest a spam/junk folder, even if it was selected
		}
		wm, known := state[mbox]
		opts := mail.ListOptions{MailboxID: mbox}
		if known && !wm.IsZero() {
			since := wm
			opts.Since = &since // incremental: only newer than the watermark
		} else {
			opts.Limit = backfill // first sync: the most recent N messages
		}
		threads, err := conn.ListThreads(ctx, opts)
		if err != nil {
			st.Errors++
			continue
		}
		newest := wm
		for i := range threads {
			if _, bad := spamThreads[threads[i].ID]; bad {
				continue // cross-referenced from the Spam folder — skip
			}
			st.Threads++
			msgs, err := conn.GetMessagesByThread(ctx, &threads[i])
			if err != nil {
				st.Errors++
				continue
			}
			for _, m := range msgs {
				if err := ctx.Err(); err != nil {
					return st, state, err
				}
				createdAt := ""
				if !m.Date.IsZero() {
					createdAt = m.Date.UTC().Format(time.RFC3339)
				}
				ref := ms.provider + ":" + m.ID
				pushedAny := false

				// 1) The mail body. Index it unless there's genuinely nothing — an
				// empty plain-text part alone is NOT a reason to drop a mail whose
				// subject (and HTML body) carry the content.
				body := ms.norm.NormalizeMessage(&m)
				if strings.TrimSpace(m.Subject) != "" || strings.TrimSpace(body) != "" {
					if _, err := ms.client.PushText(ctx, IngestText{
						Title:      m.Subject,
						Text:       messageText(m, body),
						SourceType: "mail",
						SourceRef:  ref,
						Author:     m.From,
						CreatedAt:  createdAt,
						Metadata:   map[string]any{"from": m.From, "mailbox": mbox, "provider": ms.provider},
					}); err != nil {
						st.Errors++
					} else {
						st.Pushed++
						pushedAny = true
					}
				}

				// 2) PDF attachments → separate knowledge items LINKED to the mail.
				// The title embeds the mail subject so searching the mail surfaces
				// its attachments too; metadata.parent records the link for the UI.
				for _, att := range m.Attachments {
					if err := ctx.Err(); err != nil {
						return st, state, err
					}
					atext := ms.attachmentText(ctx, att)
					if atext == "" {
						continue
					}
					if _, err := ms.client.PushText(ctx, IngestText{
						Title:      attachmentTitle(m.Subject, att.Filename),
						Text:       atext,
						SourceType: "mail",
						SourceRef:  ref + ":att:" + att.Filename,
						Author:     m.From,
						CreatedAt:  createdAt,
						Metadata: map[string]any{
							"from": m.From, "mailbox": mbox, "provider": ms.provider,
							"attachment": true, "filename": att.Filename,
							"parent": ref, "parent_subject": m.Subject,
						},
					}); err != nil {
						st.Errors++
					} else {
						st.Pushed++
						pushedAny = true
					}
				}

				if pushedAny && m.Date.After(newest) {
					newest = m.Date
				}
			}
		}
		// Advance this folder's watermark so the next run is incremental.
		if newest.After(wm) {
			state[mbox] = newest
			if newest.After(st.Newest) {
				st.Newest = newest
			}
		}
	}
	return st, state, nil
}

// Reconcile builds the authoritative set of message refs currently present on the
// server for this provider — the input to center-side deletion reconciliation. It
// enumerates every non-spam mailbox by Message-ID (envelope-only, no bodies) and,
// for a catch-all mailbox that bundles spam (Proton "All Mail"), subtracts the
// spam refs so a message marked spam AFTER it was indexed is treated as absent.
//
// complete is false (refs nil) when the connector can't enumerate by Message-ID,
// or ANY mailbox listing fails its integrity check. On an incomplete set the
// caller MUST NOT reconcile — absence is never inferred from a partial listing.
func (ms *MailSync) Reconcile(ctx context.Context, conn mail.MailConnector, mailboxes []string) (refs []string, complete bool) {
	lister, ok := conn.(mail.MessageIDLister)
	if !ok {
		return nil, false // connector can't enumerate cheaply → skip, fail-safe
	}
	if len(mailboxes) == 0 {
		mailboxes = []string{""} // provider default (all)
	}
	seen := map[string]struct{}{}
	for _, mbox := range mailboxes {
		if isSpamMailbox(mbox) {
			continue // never treat a spam folder as "present"
		}
		ids, _, err := lister.ListMessageIDs(ctx, mbox)
		if err != nil {
			return nil, false // integrity gate: a failed/partial listing aborts the pass
		}
		for _, id := range ids {
			if id != "" {
				seen[ms.provider+":"+id] = struct{}{}
			}
		}
	}
	// Catch-all bundles spam (Proton "All Mail"); subtract spam refs so a now-spam
	// message reads as absent and gets reconciled out.
	for ref := range ms.spamMessageRefs(ctx, lister, mailboxes) {
		delete(seen, ref)
	}
	refs = make([]string, 0, len(seen))
	for r := range seen {
		refs = append(refs, r)
	}
	return refs, true
}

// spamMessageRefs gathers the Spam/Junk message refs ONLY when a catch-all mailbox
// is being reconciled (Proton surfaces spam inside "All Mail"). Mirrors the
// ingest-time spam guard (spamThreadIDs) but at message granularity. Fail-open: a
// missing/mis-named spam folder yields an empty set, so good mail is never dropped.
func (ms *MailSync) spamMessageRefs(ctx context.Context, lister mail.MessageIDLister, mailboxes []string) map[string]struct{} {
	out := map[string]struct{}{}
	catchAll := false
	for _, mb := range mailboxes {
		if isCatchAllMailbox(mb) {
			catchAll = true
			break
		}
	}
	if !catchAll {
		return out
	}
	names := spamFolderNames
	if env := strings.TrimSpace(os.Getenv("HYGUR_SPAM_FOLDERS")); env != "" {
		names = nil
		for _, p := range strings.Split(env, ",") {
			if p = strings.TrimSpace(p); p != "" {
				names = append(names, p)
			}
		}
	}
	for _, name := range names {
		ids, _, err := lister.ListMessageIDs(ctx, name)
		if err != nil {
			continue
		}
		for _, id := range ids {
			if id != "" {
				out[ms.provider+":"+id] = struct{}{}
			}
		}
	}
	return out
}

// messageText assembles the plain text the center will index: subject + sender +
// date headers, then the normalized body (plain text, or stripped HTML for
// HTML-only mail). The subject is always included so a mail is findable by it
// even when the body is empty.
func messageText(m mail.Message, body string) string {
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
	b.WriteString(body)
	return b.String()
}

// attachmentText extracts indexable text from a PDF attachment (embedded text
// only — no OCR). Non-PDF, empty, or unparseable attachments yield "".
func (ms *MailSync) attachmentText(ctx context.Context, att mail.Attachment) string {
	if len(att.Data) == 0 || !isPDF(att) {
		return ""
	}
	text, _, err := ms.pdf.Parse(ctx, bytes.NewReader(att.Data))
	if err != nil {
		return ""
	}
	// The PDF parser returns RAW text; collapse it so the mail body keeps the
	// exact normalized attachment text it always carried (mail is left unchanged
	// by the notes/files raw_text change).
	text = strings.TrimSpace(ingest.NormalizeText(text))
	if len(text) > maxAttachmentTextBytes {
		text = text[:maxAttachmentTextBytes]
	}
	return text
}

func isPDF(att mail.Attachment) bool {
	return strings.EqualFold(att.MimeType, "application/pdf") ||
		strings.HasSuffix(strings.ToLower(att.Filename), ".pdf")
}

// attachmentTitle ties the attachment to its mail by title, so a search for the
// mail's subject also surfaces the attachment.
func attachmentTitle(subject, filename string) string {
	if strings.TrimSpace(subject) == "" {
		return "📎 " + filename
	}
	return subject + " — 📎 " + filename
}
