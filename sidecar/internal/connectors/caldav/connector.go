package caldav

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/plugin"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

var (
	_ plugin.Connector           = (*Connector)(nil)
	_ plugin.Syncer              = (*Connector)(nil)
	_ plugin.SecretFieldProvider = (*Connector)(nil)
)

const (
	fetchTimeout    = 30 * time.Second
	defaultMaxItems = 1000
)

// Connector syncs an online calendar (CalDAV per-calendar ICS export or a
// public iCal/webcal .ics URL) into the knowledge base as source_type="event".
// Each Sync GETs the feed; no persistent connection is held. Multi-instance so
// the user can add as many calendars as they want.
type Connector struct {
	db     *store.DB
	emb    *llm.EmbeddingService
	broker *events.Broker
	log    zerolog.Logger
	http   *http.Client

	mu       sync.RWMutex
	cfg      plugin.ConnectorConfig
	health   plugin.HealthStatus
	lastSync time.Time
}

// New creates a CalDAV/ICS connector. db is required; emb may be nil (events
// are then stored without embeddings — still listed, but not RAG-searchable).
func New(db *store.DB, emb *llm.EmbeddingService, broker *events.Broker, log zerolog.Logger) *Connector {
	return &Connector{
		db:     db,
		emb:    emb,
		broker: broker,
		log:    log.With().Str("connector", "caldav").Logger(),
		http:   &http.Client{Timeout: fetchTimeout},
		health: plugin.HealthStatus{Status: plugin.StatusUnconfigured},
	}
}

func (c *Connector) SetBroker(b *events.Broker) {
	c.mu.Lock()
	c.broker = b
	c.mu.Unlock()
}

func (c *Connector) Info() plugin.ConnectorInfo {
	return plugin.ConnectorInfo{
		ID:            "caldav",
		Name:          "CalDAV",
		Description:   "Sync an online calendar (CalDAV export or iCal/webcal .ics URL) into your knowledge base.",
		Version:       "1.0.0",
		Icon:          "calendar",
		Color:         "#2e6a57",
		Tags:          []string{"calendar", "caldav", "ical", "events"},
		MultiInstance: true,
	}
}

func (c *Connector) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		CanSync:   true,
		NeedsAuth: false, // basic auth optional (public ICS feeds need none)
		AuthType:  plugin.AuthPassword,
	}
}

func (c *Connector) ConfigSchema() plugin.ConfigSchema {
	return plugin.ConfigSchema{
		Groups: []plugin.ConfigGroup{
			{
				Title: "Calendar",
				Fields: []plugin.ConfigField{
					{
						Key:         "url",
						Type:        plugin.FieldString,
						Label:       "Calendar URL",
						Description: "CalDAV per-calendar export or a public iCal/webcal .ics URL",
						Required:    true,
					},
				},
			},
			{
				Title: "Authentication (optional)",
				Fields: []plugin.ConfigField{
					{Key: "username", Type: plugin.FieldString, Label: "Username"},
					{Key: "password", Type: plugin.FieldSecret, Label: "Password / app password"},
				},
			},
			{
				Title: "Synchronization",
				Fields: []plugin.ConfigField{
					{Key: "schedule", Type: plugin.FieldCron, Label: "Sync frequency", Default: "0 */6 * * *"},
				},
			},
		},
	}
}

func (c *Connector) SecretFieldKeys() []string { return []string{"password"} }

func (c *Connector) Init(_ context.Context, cfg plugin.ConnectorConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(cfg.Settings["url"]) == "" {
		c.health = plugin.HealthStatus{Status: plugin.StatusUnconfigured, Message: "calendar URL is required"}
		return errors.New("caldav: url is required")
	}
	c.cfg = cfg
	c.health = plugin.HealthStatus{Status: plugin.StatusConnecting}
	return nil
}

func (c *Connector) Start(_ context.Context) error { return nil }
func (c *Connector) Stop(_ context.Context) error  { return nil }

func (c *Connector) Health() plugin.HealthStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.health
}

// Sync fetches the calendar feed and ingests its events.
func (c *Connector) Sync(ctx context.Context, opts plugin.SyncOptions) (*plugin.SyncResult, error) {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()

	url := normalizeURL(strings.TrimSpace(cfg.Settings["url"]))
	if url == "" {
		return nil, errors.New("caldav: not configured — url missing")
	}
	start := time.Now()

	body, err := c.fetch(ctx, url, cfg.Settings["username"], cfg.Settings["password"])
	if err != nil {
		c.setHealth(plugin.StatusUnhealthy, "fetch failed: "+err.Error())
		return nil, fmt.Errorf("caldav sync: %w", err)
	}

	evs := ParseICS(body)
	max := defaultMaxItems
	if opts.Limit > 0 {
		max = opts.Limit
	}

	indexed, skipped, failed := 0, 0, 0
	seen := map[string]struct{}{}
	for _, ev := range evs {
		if indexed >= max {
			break
		}
		contentID := eventContentID(url, ev)
		if _, dup := seen[contentID]; dup {
			skipped++
			continue
		}
		seen[contentID] = struct{}{}
		if err := c.ingest(ctx, contentID, ev); err != nil {
			c.log.Debug().Err(err).Str("uid", ev.UID).Msg("event ingest failed")
			failed++
			continue
		}
		indexed++
	}

	c.markSynced(int64(indexed))
	if c.broker != nil {
		c.broker.PublishWithType(events.EventTypeIngestComplete, events.StatusCompleted, "caldav",
			"calendar sync completed",
			map[string]any{"indexed": indexed, "skipped": skipped, "failed": failed})
	}
	return &plugin.SyncResult{Processed: indexed, Skipped: skipped, Failed: failed, Duration: time.Since(start)}, nil
}

func (c *Connector) HealthCheck(ctx context.Context) error {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	url := normalizeURL(strings.TrimSpace(cfg.Settings["url"]))
	if url == "" {
		return errors.New("caldav: url missing")
	}
	_, err := c.fetch(ctx, url, cfg.Settings["username"], cfg.Settings["password"])
	return err
}

func (c *Connector) fetch(ctx context.Context, url, username, password string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/calendar, application/calendar+xml;q=0.9, */*;q=0.5")
	req.Header.Set("User-Agent", "Hygur/1.0")
	if strings.TrimSpace(username) != "" {
		req.SetBasicAuth(strings.TrimSpace(username), password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20)) // 32 MiB cap
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ingest stores one event as a knowledge_item (delete-then-insert at a stable
// id) and embeds it best-effort so it's searchable + feeds the agenda.
func (c *Connector) ingest(ctx context.Context, contentID string, ev Event) error {
	title := strings.TrimSpace(ev.Summary)
	if title == "" {
		title = "(événement sans titre)"
	}

	var sb strings.Builder
	sb.WriteString(title)
	if !ev.Start.IsZero() {
		sb.WriteString("\nQuand : ")
		if ev.AllDay {
			sb.WriteString(ev.Start.Format("2006-01-02"))
		} else {
			sb.WriteString(ev.Start.Format("2006-01-02 15:04"))
		}
		if !ev.End.IsZero() {
			sb.WriteString(" → ")
			sb.WriteString(ev.End.Format("2006-01-02 15:04"))
		}
	}
	if ev.Location != "" {
		sb.WriteString("\nLieu : ")
		sb.WriteString(ev.Location)
	}
	if ev.Organizer != "" {
		sb.WriteString("\nOrganisateur : ")
		sb.WriteString(ev.Organizer)
	}
	if len(ev.Attendees) > 0 {
		sb.WriteString("\nParticipants : ")
		sb.WriteString(strings.Join(ev.Attendees, ", "))
	}
	if ev.Description != "" {
		sb.WriteString("\n\n")
		sb.WriteString(ev.Description)
	}
	normalized := sb.String()

	metadata := map[string]any{
		"source":   "caldav",
		"uid":      ev.UID,
		"location": ev.Location,
		"all_day":  ev.AllDay,
	}
	if ev.Organizer != "" {
		metadata["organizer"] = ev.Organizer
	}
	if len(ev.Attendees) > 0 {
		metadata["attendees"] = ev.Attendees
	}
	now := time.Now().UTC()
	created := now
	if !ev.Start.IsZero() {
		metadata["start"] = ev.Start.UTC().Format(time.RFC3339)
		metadata["canonical_date"] = ev.Start.UTC().Format(time.RFC3339)
		created = ev.Start.UTC()
	}
	if !ev.End.IsZero() {
		metadata["end"] = ev.End.UTC().Format(time.RFC3339)
	}

	hash := sha1.Sum([]byte(normalized))
	item := &store.KnowledgeItem{
		ContentID:      contentID,
		SourceType:     "event",
		Title:          title,
		NormalizedText: normalized,
		Metadata:       metadata,
		VersionID:      hex.EncodeToString(hash[:])[:16],
		CreatedAt:      created,
		UpdatedAt:      now,
	}

	if existing, err := c.db.GetKnowledgeItem(ctx, contentID); err == nil && existing != nil {
		if existing.VersionID == item.VersionID {
			return nil // unchanged
		}
		_ = c.db.DeleteKnowledgeItem(ctx, contentID)
	}
	if err := c.db.InsertKnowledgeItem(ctx, item); err != nil {
		return err
	}
	// Best-effort embedding — keep the item even if the embedder is down.
	if c.emb != nil {
		if _, _, err := ingest.IndexSections(ctx, c.db, c.emb, contentID, normalized, ingest.DefaultChunkTokenBudget, now); err != nil {
			c.log.Debug().Err(err).Str("content_id", contentID).Msg("event embedding failed; kept item")
		}
	}
	return nil
}

func (c *Connector) setHealth(status plugin.Status, msg string) {
	c.mu.Lock()
	c.health.Status = status
	c.health.Message = msg
	c.mu.Unlock()
}

func (c *Connector) markSynced(indexed int64) {
	now := time.Now().UTC()
	c.mu.Lock()
	c.lastSync = now
	c.health = plugin.HealthStatus{Status: plugin.StatusHealthy, LastSync: now, ItemCount: c.health.ItemCount + indexed}
	c.mu.Unlock()
}

// eventContentID is stable per (feed URL, event UID) so re-syncs overwrite.
func eventContentID(url string, ev Event) string {
	key := ev.UID
	if key == "" {
		key = ev.Summary + "|" + ev.Start.Format(time.RFC3339)
	}
	h := sha1.Sum([]byte(url + "|" + key))
	return "event:" + hex.EncodeToString(h[:])[:16]
}

// normalizeURL maps webcal:// to https:// (Apple/Google "subscribe" URLs).
func normalizeURL(u string) string {
	if strings.HasPrefix(u, "webcal://") {
		return "https://" + strings.TrimPrefix(u, "webcal://")
	}
	return u
}
