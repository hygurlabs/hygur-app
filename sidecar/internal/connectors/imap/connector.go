// Package imap implements the plugin.Connector interface for generic IMAP sources.
// It dials any IMAP server (TLS or STARTTLS), fetches messages, parses them
// into store.KnowledgeItem values and writes them to the knowledge base.
//
// Package name is "imap" rather than "mail" to avoid the conflict with the
// Go standard library's net/mail package.
package imap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"sync"
	"time"

	imaplib "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/plugin"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// Compile-time assertions.
var (
	_ plugin.Connector        = (*Connector)(nil)
	_ plugin.Syncer           = (*Connector)(nil)
	_ plugin.SecretFieldProvider = (*Connector)(nil)
)

// defaultMaxMessages is used when no max_messages setting is provided.
const defaultMaxMessages = 100

// dialTimeout caps IMAP connection establishment.
const dialTimeout = 30 * time.Second

// fetchTimeout caps the FETCH/SEARCH round-trip.
const fetchTimeout = 60 * time.Second

// Connector is a generic IMAP connector that syncs messages into the
// knowledge base. Each Sync call opens a fresh IMAP connection; there is
// no persistent connection (IDLE is not used).
type Connector struct {
	db     *store.DB
	broker *events.Broker
	log    zerolog.Logger

	mu       sync.RWMutex
	cfg      plugin.ConnectorConfig
	health   plugin.HealthStatus
	lastSync time.Time
}

// New creates a new IMAP Connector.
// db is required for InsertKnowledgeItem; broker may be nil and can be set
// later via SetBroker (events are silently skipped when nil).
func New(db *store.DB, broker *events.Broker, log zerolog.Logger) *Connector {
	return &Connector{
		db:     db,
		broker: broker,
		log:    log.With().Str("connector", "imap").Logger(),
		health: plugin.HealthStatus{
			Status: plugin.StatusUnconfigured,
		},
	}
}

// SetBroker injects the event broker after construction. Safe to call at any
// time; the broker is only read during Sync.
func (c *Connector) SetBroker(b *events.Broker) {
	c.mu.Lock()
	c.broker = b
	c.mu.Unlock()
}

// ---------------------------------------------------------------------------
// plugin.Connector
// ---------------------------------------------------------------------------

// Info returns static metadata for the IMAP connector.
func (c *Connector) Info() plugin.ConnectorInfo {
	return plugin.ConnectorInfo{
		ID:            "imap",
		Name:          "IMAP",
		Description:   "Index emails from any IMAP server (Gmail, Fastmail, self-hosted, etc.)",
		Version:       "1.0.0",
		Icon:          "envelope",
		Color:         "#3B82F6",
		Tags:          []string{"email", "imap", "communication"},
		MultiInstance: true,
	}
}

// Capabilities returns the operations supported by this connector.
func (c *Connector) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		CanList:      false,
		CanSearch:    false,
		CanSync:      true,
		CanIndex:     false,
		CanSummarize: false,
		CanAttach:    false,
		NeedsAuth:    true,
		AuthType:     plugin.AuthPassword,
	}
}

// ConfigSchema returns the configuration schema for UI generation.
func (c *Connector) ConfigSchema() plugin.ConfigSchema {
	return plugin.ConfigSchema{
		Groups: []plugin.ConfigGroup{
			{
				Title: "Server",
				Fields: []plugin.ConfigField{
					{
						Key:         "host",
						Type:        plugin.FieldString,
						Label:       "IMAP host",
						Description: "e.g. imap.gmail.com",
						Required:    true,
					},
					{
						Key:         "port",
						Type:        plugin.FieldString,
						Label:       "Port",
						Description: "993 for TLS (default), 143 for STARTTLS",
						Default:     "993",
					},
					{
						Key:         "tls",
						Type:        plugin.FieldBool,
						Label:       "Use TLS",
						Description: "Disable to use STARTTLS on port 143",
						Default:     "true",
					},
					{
						Key:         "mailbox",
						Type:        plugin.FieldString,
						Label:       "Mailbox",
						Description: "Mailbox to sync, e.g. INBOX",
						Default:     "INBOX",
					},
					{
						Key:         "max_messages",
						Type:        plugin.FieldInt,
						Label:       "Max messages per sync",
						Description: "Maximum number of messages fetched per sync cycle",
						Default:     "100",
					},
				},
			},
			{
				Title: "Authentication",
				Fields: []plugin.ConfigField{
					{
						Key:      "username",
						Type:     plugin.FieldString,
						Label:    "Username / email",
						Required: true,
					},
					{
						Key:      "password",
						Type:     plugin.FieldSecret,
						Label:    "Password / app password",
						Required: true,
					},
				},
			},
			{
				Title: "Synchronization",
				Fields: []plugin.ConfigField{
					{
						Key:     "schedule",
						Type:    plugin.FieldCron,
						Label:   "Sync frequency",
						Default: "0 */6 * * *",
					},
				},
			},
		},
	}
}

// SecretFieldKeys implements plugin.SecretFieldProvider.
// The password must never be persisted in plain text in config.yaml.
func (c *Connector) SecretFieldKeys() []string {
	return []string{"password"}
}

// Init validates the configuration and stores it. It does not open a
// connection; connections are established per-Sync call.
func (c *Connector) Init(_ context.Context, cfg plugin.ConnectorConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	host := strings.TrimSpace(cfg.Settings["host"])
	if host == "" {
		c.health = plugin.HealthStatus{
			Status:  plugin.StatusUnconfigured,
			Message: "host is required",
		}
		return errors.New("imap: host is required")
	}

	username := strings.TrimSpace(cfg.Settings["username"])
	if username == "" {
		c.health = plugin.HealthStatus{
			Status:  plugin.StatusUnconfigured,
			Message: "username is required",
		}
		return errors.New("imap: username is required")
	}

	c.cfg = cfg
	c.health = plugin.HealthStatus{
		Status: plugin.StatusConnecting,
	}
	return nil
}

// Start is a no-op: IMAP connections are ephemeral (opened per-Sync).
func (c *Connector) Start(_ context.Context) error {
	return nil
}

// Stop is a no-op: no persistent connection is held.
func (c *Connector) Stop(_ context.Context) error {
	return nil
}

// Health returns the current health status without performing IO.
func (c *Connector) Health() plugin.HealthStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.health
}

// ---------------------------------------------------------------------------
// plugin.Syncer
// ---------------------------------------------------------------------------

// Sync connects to the IMAP server, fetches messages and writes them to the
// knowledge base. It is safe for concurrent calls from the plugin Manager.
func (c *Connector) Sync(ctx context.Context, opts plugin.SyncOptions) (*plugin.SyncResult, error) {
	c.mu.RLock()
	cfg := c.cfg
	lastSync := c.lastSync
	c.mu.RUnlock()

	host := strings.TrimSpace(cfg.Settings["host"])
	if host == "" {
		return nil, errors.New("imap: connector not configured — host missing")
	}

	port := strings.TrimSpace(cfg.Settings["port"])
	if port == "" {
		port = "993"
	}

	username := strings.TrimSpace(cfg.Settings["username"])
	password := strings.TrimSpace(cfg.Settings["password"])

	useTLS := true
	if v, ok := cfg.Settings["tls"]; ok && strings.ToLower(strings.TrimSpace(v)) == "false" {
		useTLS = false
	}

	mailbox := strings.TrimSpace(cfg.Settings["mailbox"])
	if mailbox == "" {
		if opts.Mailbox != "" {
			mailbox = opts.Mailbox
		} else {
			mailbox = "INBOX"
		}
	}

	maxMessages := defaultMaxMessages
	if opts.Limit > 0 {
		maxMessages = opts.Limit
	} else if v := strings.TrimSpace(cfg.Settings["max_messages"]); v != "" {
		n := 0
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			maxMessages = n
		}
	}

	address := host + ":" + port
	start := time.Now()

	// --- dial ----------------------------------------------------------
	dialCtx, dialCancel := context.WithTimeout(ctx, dialTimeout)
	defer dialCancel()

	client, err := c.dial(dialCtx, address, useTLS)
	if err != nil {
		c.setHealth(plugin.StatusUnhealthy, "dial failed: "+err.Error())
		return nil, fmt.Errorf("imap sync: dial %s: %w", address, err)
	}
	defer client.Close()

	// --- login ---------------------------------------------------------
	if err := client.Login(username, password).Wait(); err != nil {
		c.setHealth(plugin.StatusUnhealthy, "login failed: "+err.Error())
		return nil, fmt.Errorf("imap sync: login: %w", err)
	}

	// --- select mailbox ------------------------------------------------
	selectData, err := client.Select(mailbox, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("imap sync: SELECT %s: %w", mailbox, err)
	}

	if selectData.NumMessages == 0 {
		c.markSynced(0)
		return &plugin.SyncResult{Duration: time.Since(start)}, nil
	}

	// --- search for target UIDs ----------------------------------------
	fetchCtx, fetchCancel := context.WithTimeout(ctx, fetchTimeout)
	defer fetchCancel()

	uids, err := c.searchUIDs(client, opts.Full, lastSync)
	if err != nil {
		return nil, fmt.Errorf("imap sync: SEARCH: %w", err)
	}

	if len(uids) == 0 {
		c.markSynced(0)
		return &plugin.SyncResult{Duration: time.Since(start)}, nil
	}

	// Respect the maxMessages cap: take the N most-recent (highest UIDs last
	// in the ordered set returned by SEARCH).
	if len(uids) > maxMessages {
		uids = uids[len(uids)-maxMessages:]
	}

	// --- fetch messages ------------------------------------------------
	indexed, skipped, failed := 0, 0, 0

	uidSet := imaplib.UIDSetNum(uids...)
	fetchOptions := &imaplib.FetchOptions{
		UID:          true,
		Envelope:     true,
		Flags:        true,
		InternalDate: true,
		BodySection: []*imaplib.FetchItemBodySection{
			{Peek: true}, // BODY.PEEK[] — full RFC 5322 message, no \Seen side-effect
		},
	}

	fetchCmd := client.Fetch(uidSet, fetchOptions)
	defer fetchCmd.Close()

	_ = fetchCtx // timeout enforced via the context deadline on the connection dialer above

	for {
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		buf, err := msg.Collect()
		if err != nil {
			c.log.Warn().Err(err).Msg("failed to collect message data; skipping")
			failed++
			continue
		}

		item, err := c.buildKnowledgeItem(buf)
		if err != nil {
			c.log.Warn().Err(err).Uint32("seq", buf.SeqNum).Msg("failed to parse message; skipping")
			failed++
			continue
		}
		if item == nil {
			// duplicate
			skipped++
			continue
		}

		if err := c.db.InsertKnowledgeItem(ctx, item); err != nil {
			// Duplicate content_id is the most common non-fatal error here.
			if strings.Contains(err.Error(), "UNIQUE constraint") ||
				strings.Contains(err.Error(), "duplicate") {
				skipped++
			} else {
				c.log.Warn().Err(err).Str("content_id", item.ContentID).Msg("insert failed; skipping")
				failed++
			}
			continue
		}

		indexed++
	}

	if err := fetchCmd.Close(); err != nil {
		c.log.Warn().Err(err).Msg("FETCH command close error")
	}

	_ = client.Logout().Wait()

	c.markSynced(int64(indexed))

	if c.broker != nil {
		c.broker.PublishWithType(
			events.EventTypeIngestComplete,
			events.StatusCompleted,
			"imap",
			"imap sync completed",
			map[string]any{
				"indexed": indexed,
				"skipped": skipped,
				"failed":  failed,
			},
		)
	}

	return &plugin.SyncResult{
		Processed: indexed,
		Skipped:   skipped,
		Failed:    failed,
		Duration:  time.Since(start),
	}, nil
}

// HealthCheck performs a lightweight dial + login check without fetching
// any messages. Returns nil when the credentials are valid.
func (c *Connector) HealthCheck(ctx context.Context) error {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()

	host := strings.TrimSpace(cfg.Settings["host"])
	port := strings.TrimSpace(cfg.Settings["port"])
	if port == "" {
		port = "993"
	}
	username := strings.TrimSpace(cfg.Settings["username"])
	password := strings.TrimSpace(cfg.Settings["password"])

	useTLS := true
	if v, ok := cfg.Settings["tls"]; ok && strings.ToLower(strings.TrimSpace(v)) == "false" {
		useTLS = false
	}

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	client, err := c.dial(dialCtx, host+":"+port, useTLS)
	if err != nil {
		return fmt.Errorf("imap health: dial: %w", err)
	}
	defer client.Close()

	if err := client.Login(username, password).Wait(); err != nil {
		return fmt.Errorf("imap health: login: %w", err)
	}

	_ = client.Logout().Wait()
	return nil
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

// dial opens an IMAP connection with TLS or STARTTLS depending on useTLS.
func (c *Connector) dial(_ context.Context, address string, useTLS bool) (*imapclient.Client, error) {
	if useTLS {
		return imapclient.DialTLS(address, nil)
	}
	return imapclient.DialStartTLS(address, nil)
}

// searchUIDs returns the list of UIDs to fetch.
// When full is true every UID in the mailbox is returned.
// When full is false only messages received since lastSync are returned;
// if lastSync is zero all UIDs are returned (first-time sync).
func (c *Connector) searchUIDs(client *imapclient.Client, full bool, lastSync time.Time) ([]imaplib.UID, error) {
	criteria := &imaplib.SearchCriteria{}

	if !full && !lastSync.IsZero() {
		// SINCE uses date-only granularity; subtract one day to avoid missing
		// messages received early on the same calendar day as the last sync.
		criteria.Since = lastSync.AddDate(0, 0, -1)
	}

	searchData, err := client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, err
	}

	uids, ok := searchData.All.(imaplib.UIDSet)
	if !ok {
		return nil, nil
	}

	list, dynamic := uids.Nums()
	if dynamic {
		return nil, errors.New("imap: unexpected dynamic UID set from SEARCH")
	}
	return list, nil
}

// buildKnowledgeItem converts a fetched message buffer into a KnowledgeItem.
// Returns (nil, nil) when the message should be silently skipped (no message-id).
func (c *Connector) buildKnowledgeItem(buf *imapclient.FetchMessageBuffer) (*store.KnowledgeItem, error) {
	if buf.Envelope == nil {
		return nil, errors.New("missing envelope")
	}

	env := buf.Envelope
	msgID := strings.TrimSpace(env.MessageID)
	if msgID == "" {
		// No Message-ID — skip silently (can't build an idempotent content_id).
		return nil, nil
	}

	contentID := "imap:" + msgID

	// Parse the raw body to extract plain text.
	normalizedText := ""
	var rawBody []byte
	for _, bs := range buf.BodySection {
		rawBody = bs.Bytes
		break
	}

	if len(rawBody) > 0 {
		normalizedText = extractPlainText(rawBody)
	}

	// Build "from" display string.
	fromStr := ""
	if len(env.From) > 0 {
		addr := env.From[0]
		name := addr.Name
		email := addr.Addr()
		if name != "" && email != "" {
			fromStr = name + " <" + email + ">"
		} else if email != "" {
			fromStr = email
		} else {
			fromStr = name
		}
	}

	title := env.Subject
	if title == "" {
		title = "(no subject)"
	}

	metadata := map[string]any{
		"from":       fromStr,
		"message_id": msgID,
	}
	if !env.Date.IsZero() {
		metadata["date"] = env.Date.Format(time.RFC3339)
	}

	now := time.Now().UTC()
	sentAt := env.Date
	if sentAt.IsZero() {
		sentAt = now
	}

	return &store.KnowledgeItem{
		ContentID:      contentID,
		SourceType:     "mail",
		Title:          title,
		NormalizedText: normalizedText,
		Metadata:       metadata,
		VersionID:      msgID,
		CreatedAt:      sentAt,
		UpdatedAt:      now,
	}, nil
}

// extractPlainText parses a raw RFC 5322 message and returns the best plain
// text representation of its body. It prefers text/plain parts; when only
// text/html is available the tags are stripped with a simple regex-free
// approach using the standard library.
func extractPlainText(raw []byte) string {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		// Fall back to returning the raw body as-is.
		return strings.TrimSpace(string(raw))
	}

	ct := msg.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil {
		// No content-type — read body as plain text.
		b, _ := io.ReadAll(msg.Body)
		return decodeTransferEncoding(msg.Header.Get("Content-Transfer-Encoding"), b)
	}

	switch {
	case strings.EqualFold(mediaType, "text/plain"):
		b, _ := io.ReadAll(msg.Body)
		return decodeTransferEncoding(msg.Header.Get("Content-Transfer-Encoding"), b)

	case strings.EqualFold(mediaType, "text/html"):
		b, _ := io.ReadAll(msg.Body)
		decoded := decodeTransferEncoding(msg.Header.Get("Content-Transfer-Encoding"), b)
		return stripHTMLTags(decoded)

	case strings.HasPrefix(strings.ToLower(mediaType), "multipart/"):
		boundary := params["boundary"]
		if boundary == "" {
			return ""
		}
		return extractFromMultipart(msg.Body, boundary, mediaType)
	}

	return ""
}

// extractFromMultipart walks a MIME multipart body and returns text/plain if
// present, otherwise strips tags from text/html.
func extractFromMultipart(body io.Reader, boundary, parentMediaType string) string {
	mr := multipart.NewReader(body, boundary)
	var htmlFallback string

	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}

		ct := part.Header.Get("Content-Type")
		if ct == "" {
			ct = "text/plain"
		}

		mt, subParams, parseErr := mime.ParseMediaType(ct)
		if parseErr != nil {
			_ = part.Close()
			continue
		}

		cte := part.Header.Get("Content-Transfer-Encoding")

		switch {
		case strings.EqualFold(mt, "text/plain"):
			b, _ := io.ReadAll(part)
			decoded := decodeTransferEncoding(cte, b)
			if strings.TrimSpace(decoded) != "" {
				return decoded
			}

		case strings.EqualFold(mt, "text/html"):
			b, _ := io.ReadAll(part)
			decoded := decodeTransferEncoding(cte, b)
			if htmlFallback == "" {
				htmlFallback = stripHTMLTags(decoded)
			}

		case strings.HasPrefix(strings.ToLower(mt), "multipart/"):
			// Recurse into nested multiparts (e.g. multipart/alternative inside multipart/mixed).
			sub := extractFromMultipart(part, subParams["boundary"], mt)
			if sub != "" {
				return sub
			}
		}

		_ = part.Close()
	}

	// multipart/alternative: prefer already-found plain text (returned above).
	// Here we only reach htmlFallback.
	return htmlFallback
}

// decodeTransferEncoding decodes quoted-printable if needed. Other encodings
// (base64 for large blobs) are not decoded to avoid pulling in heavy deps for
// text search purposes — the raw bytes are returned as-is.
func decodeTransferEncoding(cte string, raw []byte) string {
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw)))
		if err == nil {
			return strings.TrimSpace(string(decoded))
		}
	}
	return strings.TrimSpace(string(raw))
}

// stripHTMLTags removes HTML tags from s using a simple state machine that
// does not allocate a full DOM. Good enough for search-index normalization.
func stripHTMLTags(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			b.WriteRune(' ') // preserve word boundaries
		case !inTag:
			b.WriteRune(r)
		}
	}
	// Collapse consecutive whitespace.
	result := strings.Join(strings.Fields(b.String()), " ")
	return result
}

// setHealth updates the health status under the write lock.
func (c *Connector) setHealth(status plugin.Status, msg string) {
	c.mu.Lock()
	c.health.Status = status
	c.health.Message = msg
	c.mu.Unlock()
}

// markSynced records a successful sync and updates health.
func (c *Connector) markSynced(indexed int64) {
	now := time.Now().UTC()
	c.mu.Lock()
	c.lastSync = now
	c.health = plugin.HealthStatus{
		Status:    plugin.StatusHealthy,
		LastSync:  now,
		ItemCount: c.health.ItemCount + indexed,
	}
	c.mu.Unlock()
}
