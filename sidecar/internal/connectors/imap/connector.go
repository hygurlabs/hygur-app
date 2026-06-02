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
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"os"
	"strings"
	"sync"
	"time"

	imaplib "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/llm"
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
	emb    *llm.EmbeddingService
	broker *events.Broker
	log    zerolog.Logger

	mu       sync.RWMutex
	cfg      plugin.ConnectorConfig
	health   plugin.HealthStatus
	lastSync time.Time
	// fullResync forces the next sync to ignore the watermark (full fetch),
	// set when the user adds folders at runtime so their history is backfilled.
	// Distinguishes a deliberate reset from a fresh-process startup (where
	// lastSync is also zero but should be seeded from the DB, not full-fetched).
	fullResync bool
}

// New creates a new IMAP Connector.
// db is required for InsertKnowledgeItem; emb chunks+embeds each message so it
// is searchable (may be nil — items are stored without embeddings then); broker
// may be nil and can be set later via SetBroker (events skipped when nil).
func New(db *store.DB, emb *llm.EmbeddingService, broker *events.Broker, log zerolog.Logger) *Connector {
	return &Connector{
		db:     db,
		emb:    emb,
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
						Type:        plugin.FieldMultiEnum,
						Label:       "Folders to sync",
						Description: "Connect, then pick the folders to sync (loaded from the server). Defaults to INBOX.",
						Default:     "INBOX",
						// No static options: the UI populates them from
						// GET /connectors/{id}/mailboxes once connected.
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

	// If the folder selection grew AT RUNTIME, force a full sync so the newly
	// added folders are backfilled (their archived history is before the SINCE
	// watermark and would never be fetched otherwise — the "nothing to sync"
	// surprise). Guard on a non-empty previous config: on a fresh-process
	// startup the previous cfg is empty, which must NOT be mistaken for "folders
	// grew" — that would re-fetch the whole mailbox on every restart. Start()
	// seeds the watermark from the DB in that case instead. Removing folders
	// needs no reset.
	oldFolders := splitFolders(c.cfg.Settings["mailbox"])
	if len(oldFolders) > 0 && hasNewFolder(oldFolders, splitFolders(cfg.Settings["mailbox"])) {
		c.lastSync = time.Time{}
		c.fullResync = true
	}

	c.cfg = cfg
	c.health = plugin.HealthStatus{
		Status: plugin.StatusConnecting,
	}
	return nil
}

// hasNewFolder reports whether next contains any folder not present in prev.
func hasNewFolder(prev, next []string) bool {
	seen := make(map[string]struct{}, len(prev))
	for _, f := range prev {
		seen[f] = struct{}{}
	}
	for _, f := range next {
		if _, ok := seen[f]; !ok {
			return true
		}
	}
	return false
}

// Start seeds the persisted item count so the connector list shows the real
// indexed total immediately after a restart, before the first sync runs.
// When the connector already has indexed mail it is marked healthy (green)
// rather than sitting at the grey "connecting" status until the next sync —
// having data proves it has connected before; a real dial/login failure during
// Sync flips it back to Unhealthy. Connections are ephemeral (opened per-Sync).
func (c *Connector) Start(ctx context.Context) error {
	if c.db != nil {
		if n, latest, err := c.db.CountAndLatestBySourceTypes(ctx, []string{"mail"}); err == nil {
			c.mu.Lock()
			c.health.ItemCount = n
			if n > 0 && c.health.Status == plugin.StatusConnecting {
				c.health.Status = plugin.StatusHealthy
			}
			// Restore the sync watermark from persisted state so a restart does
			// an INCREMENTAL sync (only mail since the last index) instead of
			// re-fetching the whole mailbox. Skip when a full resync is pending
			// (folders were just added — those need a full backfill).
			if !c.fullResync && c.lastSync.IsZero() && !latest.IsZero() {
				c.lastSync = latest
			}
			c.mu.Unlock()
		}
	}
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

	// Folders to sync: comma-separated list (multi-select in the UI). Falls back
	// to opts.Mailbox, then INBOX.
	folders := splitFolders(cfg.Settings["mailbox"])
	if len(folders) == 0 {
		if opts.Mailbox != "" {
			folders = []string{opts.Mailbox}
		} else {
			folders = []string{"INBOX"}
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

	// --- per-folder: select, search, fetch, index ----------------------
	fetchCtx, fetchCancel := context.WithTimeout(ctx, fetchTimeout)
	defer fetchCancel()
	_ = fetchCtx // timeout enforced via the connection dialer deadline above

	indexed, skipped, failed := 0, 0, 0

	// Pre-pass: resolve the UIDs to fetch per folder so the progress bar can
	// report an accurate processed/total. SELECT+SEARCH are cheap; the slow
	// FETCH happens once, in the second pass below.
	type folderWork struct {
		mailbox string
		uids    []imaplib.UID
	}
	var work []folderWork
	total := 0
	for _, mailbox := range folders {
		selectData, err := client.Select(mailbox, nil).Wait()
		if err != nil {
			c.log.Warn().Err(err).Str("mailbox", mailbox).Msg("SELECT failed; skipping folder")
			failed++
			continue
		}
		if selectData.NumMessages == 0 {
			continue
		}
		uids, err := c.searchUIDs(client, opts.Full, lastSync)
		if err != nil {
			c.log.Warn().Err(err).Str("mailbox", mailbox).Msg("SEARCH failed; skipping folder")
			failed++
			continue
		}
		if len(uids) == 0 {
			continue
		}
		// Respect the maxMessages cap per folder: take the N most-recent.
		if len(uids) > maxMessages {
			uids = uids[len(uids)-maxMessages:]
		}
		work = append(work, folderWork{mailbox: mailbox, uids: uids})
		total += len(uids)
	}

	// Progress emitter: drives the WebUI/status-bar sync bar, which reads
	// processed/total/eta off EventTypeSync. Without this, IMAP syncs showed no
	// progress bar (only the EmailIndexer path emitted these events).
	processed := 0
	emitProgress := func() {
		if c.broker == nil {
			return
		}
		eta := 0.0
		if processed > 0 && total > processed {
			if el := time.Since(start).Seconds(); el > 0 {
				eta = float64(total-processed) / (float64(processed) / el)
			}
		}
		c.broker.PublishWithType(events.EventTypeSync, events.StatusRunning, "imap", "syncing mail",
			map[string]any{"processed": processed, "total": total, "eta_seconds": eta})
	}
	if total > 0 {
		emitProgress() // 0/total so the bar appears immediately
	}

	// Fetch + index pass.
	for _, w := range work {
		if _, err := client.Select(w.mailbox, nil).Wait(); err != nil {
			c.log.Warn().Err(err).Str("mailbox", w.mailbox).Msg("re-SELECT failed; skipping folder")
			continue
		}

		uidSet := imaplib.UIDSetNum(w.uids...)
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
		for {
			msg := fetchCmd.Next()
			if msg == nil {
				break
			}
			processed++
			if processed%10 == 0 {
				emitProgress()
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
				// no Message-ID — can't build an idempotent content_id
				skipped++
				continue
			}

			insertErr := c.db.InsertKnowledgeItem(ctx, item)
			isDup := insertErr != nil &&
				(strings.Contains(insertErr.Error(), "UNIQUE constraint") ||
					strings.Contains(insertErr.Error(), "duplicate"))
			if insertErr != nil && !isDup {
				c.log.Warn().Err(insertErr).Str("content_id", item.ContentID).Msg("insert failed; skipping")
				failed++
				continue
			}

			// Re-clean: when an item already exists but our extraction has since
			// improved (e.g. base64 parts now decoded), the freshly-extracted text
			// differs from what's stored. Update it in place and force a re-embed —
			// this rolls cleaner text through on a re-sync WITHOUT deleting anything
			// (no data loss for items outside this fetch window).
			refreshed := false
			if isDup && strings.TrimSpace(item.NormalizedText) != "" {
				if stored, gerr := c.db.GetKnowledgeItem(ctx, item.ContentID); gerr == nil &&
					stored != nil && stored.NormalizedText != item.NormalizedText {
					if uerr := c.db.UpdateKnowledgeItem(ctx, item); uerr == nil {
						refreshed = true
					}
				}
			}

			// Chunk + embed so the message is retrievable by vector search — a raw
			// knowledge_items row alone is invisible to RAG. Embed new items, items
			// still missing chunks (backfill), or items just re-cleaned above.
			// IndexSections is idempotent: it replaces any prior chunks.
			if c.emb != nil && strings.TrimSpace(item.NormalizedText) != "" {
				if n, cerr := c.db.CountChunksForItem(ctx, item.ContentID); cerr == nil && (n == 0 || refreshed) {
					if _, _, eerr := ingest.IndexSections(ctx, c.db, c.emb, item.ContentID, item.NormalizedText, ingest.DefaultChunkTokenBudget, time.Now().UTC()); eerr != nil {
						c.log.Warn().Err(eerr).Str("content_id", item.ContentID).Msg("embed failed; item stored without vectors")
					}
				}
			}

			if isDup {
				skipped++
			} else {
				indexed++
			}
		}
		if err := fetchCmd.Close(); err != nil {
			c.log.Warn().Err(err).Str("mailbox", w.mailbox).Msg("FETCH command close error")
		}
	}
	emitProgress() // final tick

	_ = client.Logout().Wait()

	c.markSynced(ctx, int64(indexed))

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

// ListMailboxes dials + logs in and returns the server's folder list, for the
// "folders to sync" picker in the config UI.
func (c *Connector) ListMailboxes(ctx context.Context) ([]string, error) {
	c.mu.RLock()
	cfg := c.cfg
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

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	client, err := c.dial(dialCtx, host+":"+port, useTLS)
	if err != nil {
		return nil, fmt.Errorf("imap mailboxes: dial: %w", err)
	}
	defer client.Close()

	if err := client.Login(username, password).Wait(); err != nil {
		return nil, fmt.Errorf("imap mailboxes: login: %w", err)
	}
	// NOTE: `defer client.Logout().Wait()` would evaluate client.Logout()
	// IMMEDIATELY (Go evaluates deferred call arguments at defer time), sending
	// LOGOUT right after login — the server then closes the connection and the
	// LIST below reads "unexpected EOF". Wrap in a closure so LOGOUT runs at
	// return, AFTER the LIST completes.
	defer func() { _ = client.Logout().Wait() }()

	listCmd := client.List("", "*", nil)
	names := []string{}
	for {
		data := listCmd.Next()
		if data == nil {
			break
		}
		if data.Mailbox != "" {
			names = append(names, data.Mailbox)
		}
	}
	if err := listCmd.Close(); err != nil {
		// Some servers (notably Gmail over TLS) drop the connection late in a
		// large LIST response — go-imap surfaces this as an unexpected EOF. If we
		// already parsed folders, return them rather than failing the whole
		// picker; the user can still select what we got.
		if len(names) > 0 {
			c.log.Warn().Err(err).Int("folders", len(names)).Msg("imap LIST ended early; returning partial folder list")
			return names, nil
		}
		return nil, fmt.Errorf("imap mailboxes: LIST: %w", err)
	}
	return names, nil
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

// splitFolders splits a comma-separated mailbox config value into trimmed,
// non-empty folder names.
func splitFolders(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// imapDebugWriter tees the raw IMAP protocol to stderr (→ the sidecar log) when
// HYGUR_IMAP_DEBUG is set, redacting the LOGIN line so the password is never
// logged. Diagnostic only.
type imapDebugWriter struct{ w io.Writer }

func (d imapDebugWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(" LOGIN ")) {
		_, _ = io.WriteString(d.w, "C: <redacted LOGIN>\r\n")
		return len(p), nil
	}
	_, _ = d.w.Write(p)
	return len(p), nil
}

// imapDialOptions returns go-imap options, enabling protocol debug logging when
// HYGUR_IMAP_DEBUG is set.
func imapDialOptions() *imapclient.Options {
	if os.Getenv("HYGUR_IMAP_DEBUG") == "" {
		return nil
	}
	return &imapclient.Options{DebugWriter: imapDebugWriter{w: os.Stderr}}
}

// dial opens an IMAP connection with TLS or STARTTLS depending on useTLS.
func (c *Connector) dial(_ context.Context, address string, useTLS bool) (*imapclient.Client, error) {
	if useTLS {
		return imapclient.DialTLS(address, imapDialOptions())
	}
	return imapclient.DialStartTLS(address, imapDialOptions())
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

	// UIDSet.Nums returns (uids, ok): ok is TRUE for a normal static set and
	// false only for a dynamic set (one containing "*", which a SEARCH response
	// must never be). The previous code inverted this and rejected every valid
	// result as "dynamic", so no UIDs were ever fetched (item_count stayed 0).
	list, ok := uids.Nums()
	if !ok {
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
		// "mail_date" is the canonical key the retrieval/date stack reads (same as
		// the Proton/Gmail pipeline). Stamp the message's real sent date so it
		// drives recency, date-range filtering and the date shown to the LLM —
		// never the ingestion timestamp.
		d := env.Date.Format(time.RFC3339)
		metadata["mail_date"] = d
		metadata["date"] = d // back-compat with items already indexed under "date"
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

// decodeTransferEncoding decodes the part body per its Content-Transfer-Encoding.
// This is only ever called on text/plain and text/html parts, so decoding
// base64 yields readable text — NOT decoding it (the previous behaviour) leaked
// raw base64 blobs into the index, polluting search and bloating the LLM
// context. quoted-printable and base64 are both handled; anything else is
// already text and returned as-is.
func decodeTransferEncoding(cte string, raw []byte) string {
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "quoted-printable":
		if decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw))); err == nil {
			return strings.TrimSpace(string(decoded))
		}
	case "base64":
		// MIME base64 wraps lines at ~76 cols; strip all whitespace first since
		// the std decoder rejects it.
		clean := strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, string(raw))
		if decoded, err := base64.StdEncoding.DecodeString(clean); err == nil {
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

// markSynced records a successful sync and updates health. ItemCount reports
// the TRUE persisted total (queried from the DB), not a per-process delta:
// items re-seen on later syncs are duplicates (skipped), so a cumulative
// counter reads 0 after a restart even though hundreds are indexed.
func (c *Connector) markSynced(ctx context.Context, indexed int64) {
	now := time.Now().UTC()
	total := int64(-1)
	if c.db != nil {
		if n, err := c.db.CountKnowledgeItemsBySourceTypes(ctx, []string{"mail"}); err == nil {
			total = int64(n)
		}
	}
	c.mu.Lock()
	c.lastSync = now
	c.fullResync = false // backfill done; subsequent syncs are incremental
	itemCount := c.health.ItemCount + indexed
	if total >= 0 {
		itemCount = total
	}
	c.health = plugin.HealthStatus{
		Status:    plugin.StatusHealthy,
		LastSync:  now,
		ItemCount: itemCount,
	}
	c.mu.Unlock()
}
