// Package mail implements the plugin.Connector interface for email sources.
// It wraps the existing Proton (IMAP) and Gmail (OAuth2) connectors under
// a single unified adapter that satisfies plugin.Lister, plugin.Syncer,
// plugin.Indexer, plugin.Summarizer, plugin.Attacher, and plugin.Authenticator.
package mail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hygur/sidecar/internal/auth"
	"github.com/hygur/sidecar/internal/events"
	mailpkg "github.com/hygur/sidecar/internal/mail"
	"github.com/hygur/sidecar/internal/mail/diag"
	"github.com/hygur/sidecar/internal/mail/gmail"
	"github.com/hygur/sidecar/internal/mail/mailapp"
	"github.com/hygur/sidecar/internal/mail/proton"
	"github.com/hygur/sidecar/internal/plugin"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
)

// Compiletime assertions: MailConnector must satisfy all required plugin interfaces.
var (
	_ plugin.Connector     = (*MailConnector)(nil)
	_ plugin.Lister        = (*MailConnector)(nil)
	_ plugin.Syncer        = (*MailConnector)(nil)
	_ plugin.Indexer       = (*MailConnector)(nil)
	_ plugin.Summarizer    = (*MailConnector)(nil)
	_ plugin.Attacher      = (*MailConnector)(nil)
	_ plugin.Authenticator = (*MailConnector)(nil)
)

// defaultMailbox is used when no mailbox is specified in sync options.
const defaultMailbox = "INBOX"

// MailConnector adapts the existing Proton IMAP and Gmail connectors into
// the plugin.Connector family of interfaces.
type MailConnector struct {
	proton        *proton.IMAPConnector
	gmail         *gmail.GmailConnector
	indexer       *mailpkg.EmailIndexer
	summarizeTool *tools.SummarizeThreadTool
	attachTool    *tools.ListAttachmentsTool
	credStore     *auth.CredentialStore
	config        plugin.ConnectorConfig
	activeSource  string // "proton" | "gmail" — legacy single-account path
	// pinnedProvider, when set ("proton"|"gmail"|"mailapp"), makes this instance
	// present itself as a single-provider connector type (distinct Info/schema/
	// id) instead of the legacy unified "Email" connector. Init forces the
	// provider so the instance only ever serves that one source. Empty = legacy
	// unified behaviour (back-compat).
	pinnedProvider string
	health        plugin.HealthStatus
	// reconcileDeletions opts into pruning KB mail that the server no longer
	// returns after a full sweep. Set from config.Mail.ReconcileDeletions.
	reconcileDeletions bool
	mu                 sync.RWMutex

	// Sessions manage persistent connections with auto-reconnection
	// (legacy single-account path).
	protonSession *mailpkg.Session
	gmailSession  *mailpkg.Session

	// Multi-account registry. Each entry corresponds to one MailAccount
	// credential. When non-empty, account-aware operations (SyncAccount,
	// VerifyAccount, Snapshot) operate against the registry; legacy
	// single-source operations remain functional via the fields above.
	accounts *AccountRegistry

	// Logger for sync progress tracking.
	logger zerolog.Logger

	// broker is wired in via SetBroker. When set together with a non-nil
	// store + summarizer, each Sync()/SyncAccount() cycle ends with a
	// MailDigest event listing the priority_mail items emitted during the
	// cycle. Either being nil disables the aggregation.
	broker     *events.Broker
	store      *store.DB
	summarizer *retrieval.MailSummarizer
}

// Accounts returns the multi-account registry. Always non-nil after New().
func (c *MailConnector) Accounts() *AccountRegistry {
	return c.accounts
}

// New creates a new MailConnector.  All parameters may be nil at construction
// time; they are configured during Init() based on the ConnectorConfig.
// Non-nil parameters override the connector instances that Init() would create,
// which is useful for testing.
func New(
	protonConnector *proton.IMAPConnector,
	gmailConnector *gmail.GmailConnector,
	indexer *mailpkg.EmailIndexer,
	summarize *tools.SummarizeThreadTool,
	attach *tools.ListAttachmentsTool,
	credStore *auth.CredentialStore,
	logger zerolog.Logger,
) *MailConnector {
	return &MailConnector{
		proton:        protonConnector,
		gmail:         gmailConnector,
		indexer:       indexer,
		summarizeTool: summarize,
		attachTool:    attach,
		credStore:     credStore,
		accounts:      NewAccountRegistry(),
		logger:        logger.With().Str("connector", "mail").Logger(),
		health: plugin.HealthStatus{
			Status: plugin.StatusUnconfigured,
		},
	}
}

// ---------------------------------------------------------------------------
// plugin.Connector — core lifecycle methods
// ---------------------------------------------------------------------------

// SetPinnedProvider pins this instance to a single provider so it presents as a
// distinct connector type (Proton / Gmail / Mail.app). Must be called before
// registration. Empty restores the legacy unified "Email" connector.
func (c *MailConnector) SetPinnedProvider(provider string) {
	c.pinnedProvider = provider
}

// Info returns the static metadata for this connector.
func (c *MailConnector) Info() plugin.ConnectorInfo {
	if c.pinnedProvider != "" {
		return pinnedProviderInfo(c.pinnedProvider)
	}
	return plugin.ConnectorInfo{
		ID:          "mail",
		Name:        "Email",
		Description: "Proton Mail (IMAP) and Gmail (OAuth2)",
		Version:     "1.0.0",
		Icon:        "envelope",
		Color:       "#6D4AFF",
		Tags:        []string{"email", "communication"},
	}
}

// Capabilities returns the set of operations this connector supports.
func (c *MailConnector) Capabilities() plugin.Capabilities {
	caps := plugin.Capabilities{
		CanList:      true,
		CanSearch:    false,
		CanSync:      true,
		CanIndex:     true,
		CanSummarize: true,
		CanAttach:    true,
		NeedsAuth:    true,
		AuthType:     plugin.AuthPassword,
	}
	switch c.pinnedProvider {
	case "gmail":
		caps.AuthType = plugin.AuthOAuth2
	case "mailapp":
		// Mail.app uses local Apple Events; no credentials.
		caps.NeedsAuth = false
		caps.AuthType = plugin.AuthNone
	}
	return caps
}

// ConfigSchema returns the dynamic form schema used to generate the UI.
func (c *MailConnector) ConfigSchema() plugin.ConfigSchema {
	if c.pinnedProvider != "" {
		return pinnedProviderSchema(c.pinnedProvider)
	}
	return plugin.ConfigSchema{
		Groups: []plugin.ConfigGroup{
			{
				Title: "Provider",
				Fields: []plugin.ConfigField{
					{
						Key:      "provider",
						Type:     plugin.FieldEnum,
						Label:    "Mail provider",
						Required: true,
						Default:  "proton",
						Options: []plugin.ConfigOption{
							{Value: "proton", Label: "Proton Mail (IMAP)", Icon: "lock.shield"},
							{Value: "gmail", Label: "Gmail (OAuth2)", Icon: "envelope.badge"},
							{Value: "mailapp", Label: "Mail.app (macOS, local)", Icon: "envelope.fill"},
						},
					},
				},
			},
			{
				Title: "Proton authentication",
				Fields: []plugin.ConfigField{
					{
						Key:       "username",
						Type:      plugin.FieldString,
						Label:     "Proton username",
						Required:  true,
						Condition: &plugin.ConfigCondition{Field: "provider", Value: "proton"},
					},
					{
						Key:       "password",
						Type:      plugin.FieldSecret,
						Label:     "Bridge password",
						Required:  true,
						Condition: &plugin.ConfigCondition{Field: "provider", Value: "proton"},
					},
				},
			},
			{
				Title: "Gmail authentication",
				Fields: []plugin.ConfigField{
					{
						Key:       "gmail_oauth",
						Type:      plugin.FieldOAuth,
						Label:     "Gmail connection",
						Required:  true,
						Condition: &plugin.ConfigCondition{Field: "provider", Value: "gmail"},
					},
				},
			},
			{
				Title: "Mail.app authorization",
				Fields: []plugin.ConfigField{
					{
						Key:         "mailapp_automation",
						Type:        plugin.FieldPermissionCheck,
						Label:       "Automation permission",
						Description: "Hygur needs Automation permission for Mail.app to read your local emails. Nothing leaves this Mac.",
						Default:     "Open System Settings",
						Condition:   &plugin.ConfigCondition{Field: "provider", Value: "mailapp"},
					},
				},
			},
			{
				Title: "Synchronization",
				Fields: []plugin.ConfigField{
					{
						Key:         "gmail_mailbox",
						Type:        plugin.FieldMultiEnum,
						Label:       "Gmail Labels to sync",
						Default:     "",
						Condition:   &plugin.ConfigCondition{Field: "provider", Value: "gmail"},
						Description: "Select labels to sync from Gmail",
						Options: []plugin.ConfigOption{
							{Value: "INBOX", Label: "Inbox"},
							{Value: "SENT", Label: "Sent"},
							{Value: "STARRED", Label: "Starred"},
							{Value: "IMPORTANT", Label: "Important"},
							{Value: "DRAFT", Label: "Drafts"},
							{Value: "TRASH", Label: "Trash"},
							{Value: "SPAM", Label: "Spam"},
							{Value: "UNREAD", Label: "Unread"},
							{Value: "CATEGORY_PERSONal", Label: "Category: Personal"},
							{Value: "CATEGORY_SOCIAL", Label: "Category: Social"},
							{Value: "CATEGORY_PROMOTIONS", Label: "Category: Promotions"},
							{Value: "CATEGORY_UP", Label: "Category: Updates"},
						},
					},
					{
						Key:         "proton_mailbox",
						Type:        plugin.FieldMultiEnum,
						Label:       "Proton Mailboxes to sync",
						Default:     "INBOX",
						Condition:   &plugin.ConfigCondition{Field: "provider", Value: "proton"},
						Description: "Select mailboxes to sync from Proton Mail",
						Options: []plugin.ConfigOption{
							{Value: "INBOX", Label: "Inbox"},
							{Value: "Sent", Label: "Sent"},
							{Value: "Drafts", Label: "Drafts"},
							{Value: "Trash", Label: "Trash"},
							{Value: "Spam", Label: "Spam"},
							{Value: "Archive", Label: "Archive"},
							{Value: "All Mail", Label: "All Mail"},
						},
					},
					{
						Key:         "limit",
						Type:        plugin.FieldInt,
						Label:       "Max threads",
						Default:     "0",
						Description: "Maximum threads to index per sync (0 = no limit)",
					},
					{
						Key:   "schedule",
						Type:  plugin.FieldCron,
						Label: "Schedule",
					},
				},
			},
		},
	}
}

// Init stores the connector configuration and initialises the underlying mail
// connector for the chosen provider.  Credentials are read from
// cfg.Settings for non-secret values; secrets are expected to already be
// present in c.credStore (loaded during a previous ExchangeCode or first-run
// wizard).
func (c *MailConnector) Init(ctx context.Context, cfg plugin.ConnectorConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// A pinned instance only ever serves its own provider, whatever the config
	// (or its absence) says — this is what makes it a distinct connector type.
	if c.pinnedProvider != "" {
		if cfg.Settings == nil {
			cfg.Settings = map[string]string{}
		}
		cfg.Settings["provider"] = c.pinnedProvider
	}

	c.config = cfg

	provider := cfg.Settings["provider"]
	if provider == "" {
		provider = "proton"
	}
	c.activeSource = provider

	switch provider {
	case "proton":
		username := cfg.Settings["username"]
		if username == "" {
			c.health.Status = plugin.StatusUnconfigured
			c.health.Message = "username missing from configuration"
			return fmt.Errorf("mail connector (proton): username missing")
		}

		// Retrieve password: credential store is authoritative; fall back to
		// cfg.Settings["password"] so the persisted config.yaml is always
		// sufficient to restore the connector after a restart.
		password := cfg.Settings["password"]
		if c.credStore != nil {
			if creds, err := c.credStore.GetConnectorCredential("mail"); err == nil {
				if v := creds["password"]; v != "" {
					password = v
				}
			}
		}

		if c.proton == nil {
			c.proton = proton.NewDefaultIMAPConnector()
		}
		c.proton.SetCredentials(username, password)

	case "gmail":
		clientID := cfg.Settings["client_id"]
		clientSecret := cfg.Settings["client_secret"]
		refreshToken := ""
		redirectURL := cfg.Settings["redirect_url"]
		if redirectURL == "" {
			redirectURL = "urn:ietf:wg:oauth:2.0:oob"
		}

		if c.credStore != nil {
			// Primary: new connector credential store.
			if creds, err := c.credStore.GetConnectorCredential("mail"); err == nil {
				if v := creds["refresh_token"]; v != "" {
					refreshToken = v
				}
				if v := creds["client_id"]; v != "" && clientID == "" {
					clientID = v
				}
				if v := creds["client_secret"]; v != "" && clientSecret == "" {
					clientSecret = v
				}
			}
			// Fallback: migrate credentials from the legacy per-source store.
			if clientID == "" || refreshToken == "" {
				if rt, id, sec, err := c.credStore.GetGmailCredential(); err == nil {
					if rt != "" && refreshToken == "" {
						refreshToken = rt
					}
					if id != "" && clientID == "" {
						clientID = id
					}
					if sec != "" && clientSecret == "" {
						clientSecret = sec
					}
				}
			}
		} else {
			zerolog.Ctx(ctx).Warn().Str("provider", provider).Msg("credential store unavailable, connector will require manual auth")
		}

		if c.gmail == nil {
			c.gmail = gmail.NewGmailConnector(clientID, clientSecret, redirectURL)
		} else {
			c.gmail.SetOAuthCredentials(clientID, clientSecret, redirectURL)
		}
		if refreshToken != "" {
			c.gmail.SetRefreshToken(refreshToken)
		}

	case "mailapp":
		// Mail.app native: no credentials. The account registry is already
		// loaded from the credential store at startup, so the connector is
		// usable immediately. Apple Events discovery only REFRESHES that list
		// and can hang when Mail.app is busy/wedged — running it inline froze
		// the entire sidecar boot (and the app's "Starting…" screen) until it
		// timed out. Refresh in the background so startup never blocks on it.
		if c.credStore == nil {
			c.health.Status = plugin.StatusUnconfigured
			c.health.Message = "credential store unavailable"
			return fmt.Errorf("mail connector (mailapp): credential store unavailable")
		}
		go c.refreshMailappAccounts()

	default:
		c.health.Status = plugin.StatusUnconfigured
		c.health.Message = fmt.Sprintf("unknown provider: %s", provider)
		return fmt.Errorf("mail connector: unknown provider %q (accepted values: proton, gmail, mailapp)", provider)
	}

	c.health.Status = plugin.StatusConnecting
	c.health.Message = ""
	return nil
}

// refreshMailappAccounts re-discovers Mail.app accounts and updates the
// credential store + registry. It runs OFF the startup path (spawned from Init)
// because Apple Events can hang when Mail.app is busy/wedged and must never
// block boot. Best-effort: on failure the accounts already loaded from the
// credential store remain in use. Bounded by its own context on top of the
// per-call osascript ceiling. Does not touch c.mu-guarded fields.
func (c *MailConnector) refreshMailappAccounts() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	accounts, err := mailapp.DiscoverAccounts(ctx)
	if err != nil {
		c.logger.Warn().Err(err).Msg("mailapp: background account discovery failed — using accounts from the credential store")
		return
	}
	for _, acct := range accounts {
		cred := auth.MailAccountCredential{
			AccountID: acct.ID,
			Provider:  "mailapp",
			Email:     acct.PrimaryEmail(),
		}
		if err := c.credStore.SaveMailAccountCredential(cred); err != nil {
			c.logger.Warn().Err(err).Str("account", acct.ID).Msg("mailapp: save credential failed")
		}
	}
	if _, err := c.LoadAccountsFromCredStore(); err != nil {
		c.logger.Warn().Err(err).Msg("mailapp: account registry reload failed")
	}
	c.logger.Info().Int("accounts", len(accounts)).Msg("mailapp: discovered accounts (background refresh)")
}

// Start attempts a test connection to the active mail provider.
// On failure the connector is marked unhealthy but no error is surfaced to the
// caller's goroutine — the error is returned so the Manager can log it.
func (c *MailConnector) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var connErr error

	switch c.activeSource {
	case "proton":
		if c.protonSession != nil {
			connErr = c.protonSession.EnsureConnected(ctx)
		} else if c.proton != nil {
			connErr = c.proton.Connect(ctx)
		} else {
			connErr = errors.New("proton connector not initialised — call Init() first")
		}
	case "gmail":
		if c.gmailSession != nil {
			connErr = c.gmailSession.EnsureConnected(ctx)
		} else if c.gmail != nil {
			connErr = c.gmail.Connect(ctx)
		} else {
			connErr = errors.New("gmail connector not initialised — call Init() first")
		}
	case "mailapp":
		// No legacy single-source connector. Per-account Connect() calls are
		// issued lazily by the multi-account path (SyncAccount / VerifyAccount).
		connErr = nil
	default:
		connErr = fmt.Errorf("activeSource not set — call Init() first")
	}

	if connErr != nil {
		c.health.Status = plugin.StatusUnhealthy
		c.health.Message = connErr.Error()
		return connErr
	}

	c.health.Status = plugin.StatusHealthy
	c.health.Message = ""

	// Seed ItemCount from the DB so the UI shows the real count immediately
	// on startup, before any sync runs in the current session.
	// We pass activeSource as both accountID and provider so the query matches
	// legacy rows (account_id = "gmail") as well as future multi-account rows.
	if c.indexer != nil {
		// Prefer the aggregate mail count (all accounts/providers) so the UI shows
		// the real number; fall back to the per-source count only if the total is
		// unavailable. The per-source CountItems returns 0 for the unified "mail"
		// connector because indexed rows carry a real account id, not "proton".
		if total := c.indexer.CountAllMail(ctx); total > 0 {
			c.health.ItemCount = total
		} else {
			c.health.ItemCount = c.indexer.CountItems(ctx, c.activeSource, c.activeSource)
		}
	}

	return nil
}

// Stop closes open connections gracefully.  A 5-second timeout is applied on
// top of whatever deadline is already in the context.
func (c *MailConnector) Stop(ctx context.Context) error {
	// Apply a 5-second stop timeout regardless of parent context.
	_ = ctx // context timeout is managed internally

	c.mu.Lock()
	defer c.mu.Unlock()

	var errs []string

	if c.protonSession != nil {
		if err := c.protonSession.Disconnect(); err != nil {
			errs = append(errs, fmt.Sprintf("proton: %v", err))
		}
	} else if c.proton != nil && c.proton.IsConnected() {
		if err := c.proton.Disconnect(); err != nil {
			errs = append(errs, fmt.Sprintf("proton: %v", err))
		}
	}

	if c.gmailSession != nil {
		if err := c.gmailSession.Disconnect(); err != nil {
			errs = append(errs, fmt.Sprintf("gmail: %v", err))
		}
	} else if c.gmail != nil && c.gmail.IsConnected() {
		if err := c.gmail.Disconnect(); err != nil {
			errs = append(errs, fmt.Sprintf("gmail: %v", err))
		}
	}

	c.health.Status = plugin.StatusUnhealthy
	c.health.Message = "stopped"

	if len(errs) > 0 {
		return fmt.Errorf("mail connector Stop: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Health returns the current health status without performing any IO.
func (c *MailConnector) Health() plugin.HealthStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.health
}

// ---------------------------------------------------------------------------
// plugin.Syncer — batch sync using the new Syncer abstraction
// ---------------------------------------------------------------------------

// Sync indexes emails from all available mailboxes (or a specific one if
// opts.Mailbox is set). If opts.AccountID is set, the sync is routed to a
// single account via the multi-account registry; otherwise the legacy
// single-source path runs.
func (c *MailConnector) Sync(ctx context.Context, opts plugin.SyncOptions) (*plugin.SyncResult, error) {
	if opts.AccountID != "" {
		return c.SyncAccount(ctx, opts.AccountID, opts)
	}

	c.mu.RLock()
	activeSource := c.activeSource
	c.mu.RUnlock()
	if activeSource == "mailapp" {
		return c.syncAllProvider(ctx, "mailapp", opts)
	}

	// Generic (account-less) sync: index everything the user has — all
	// registered accounts (mailapp/…) PLUS the configured legacy single-source
	// provider (proton/gmail), if any. The unified "mail" connector can hold
	// both at once, so syncing only one would silently drop mail. When nothing
	// is configured it's a clean no-op rather than the old "activeSource not
	// set" error (the connector's "mail" id never matches the provider-keyed
	// config, so Configure() never Init's it on its own).
	start := time.Now()
	var totalProcessed, totalSkipped, totalFailed int
	syncedSomething := false

	// Sync the reliable single-source provider (Proton/Gmail) FIRST so it always
	// completes within the window — a slow or wedged Mail.app account (handled by
	// syncAllAccounts below) can then no longer starve it of the shared lock.
	if activeSource == "proton" || activeSource == "gmail" {
		var lr *plugin.SyncResult
		_ = c.captureAndPublishDigest(ctx, func() error {
			r, err := c.syncLegacy(ctx, opts)
			lr = r
			return err
		})
		if lr != nil {
			totalProcessed += lr.Processed
			totalSkipped += lr.Skipped
			totalFailed += lr.Failed
		}
		syncedSomething = true
	}
	if c.accounts != nil && len(c.accounts.All()) > 0 {
		if r, _ := c.syncAllAccounts(ctx, opts); r != nil {
			totalProcessed += r.Processed
			totalSkipped += r.Skipped
			totalFailed += r.Failed
			syncedSomething = true
		}
	}
	if !syncedSomething {
		c.logger.Info().Msg("mail sync skipped: no provider configured and no accounts registered")
	}
	return &plugin.SyncResult{
		Processed: totalProcessed,
		Skipped:   totalSkipped,
		Failed:    totalFailed,
		Duration:  time.Since(start),
	}, nil
}

// syncAllAccounts syncs every registered account regardless of provider. Used
// when the unified connector has no configured single-source provider, so a
// generic (account-less) sync still indexes all accounts instead of failing.
func (c *MailConnector) syncAllAccounts(ctx context.Context, opts plugin.SyncOptions) (*plugin.SyncResult, error) {
	sessions := c.accounts.All()
	start := time.Now()
	var totalProcessed, totalSkipped, totalFailed int
	for _, s := range sessions {
		r, err := c.SyncAccount(ctx, s.AccountID, opts)
		if err != nil {
			c.logger.Error().Err(err).Str("provider", s.Provider).Str("account", s.AccountID).Msg("account sync failed")
			totalFailed++
			continue
		}
		if r != nil {
			totalProcessed += r.Processed
			totalSkipped += r.Skipped
			totalFailed += r.Failed
		}
	}
	c.logger.Info().Int("accounts", len(sessions)).Int("processed", totalProcessed).Msg("synced all registered accounts")
	return &plugin.SyncResult{
		Processed: totalProcessed,
		Skipped:   totalSkipped,
		Failed:    totalFailed,
		Duration:  time.Since(start),
	}, nil
}

// syncAllProvider iterates every registered account of the given provider
// and syncs each through SyncAccount. Used by providers (e.g. mailapp) that
// are intrinsically multi-account and have no legacy single-source path.
func (c *MailConnector) syncAllProvider(ctx context.Context, provider string, opts plugin.SyncOptions) (*plugin.SyncResult, error) {
	sessions := c.accounts.All()
	start := time.Now()
	var totalProcessed, totalSkipped, totalFailed int
	for _, s := range sessions {
		if s.Provider != provider {
			continue
		}
		r, err := c.SyncAccount(ctx, s.AccountID, opts)
		if err != nil {
			c.logger.Error().Err(err).Str("provider", provider).Str("account", s.AccountID).Msg("account sync failed")
			totalFailed++
			continue
		}
		if r != nil {
			totalProcessed += r.Processed
			totalSkipped += r.Skipped
			totalFailed += r.Failed
		}
	}
	return &plugin.SyncResult{
		Processed: totalProcessed,
		Skipped:   totalSkipped,
		Failed:    totalFailed,
		Duration:  time.Since(start),
	}, nil
}

// applyIncrementalWindow narrows cfg to mail newer than the account's last
// indexed message, so routine (cron) syncs stop re-fetching the whole mailbox
// every run when nothing changed. Safety:
//   - No-op on a forced full sync (full) or when nothing is indexed yet (first
//     sync stays full), or when the watermark can't be resolved — so we never
//     silently narrow a sync that needs to see everything.
//   - A windowed sync can't observe the full mailbox, so it MUST NOT reconcile
//     deletions; reconcile is forced off (it stays a full-sweep-only feature).
//
// A 48h overlap buffer absorbs clock skew / out-of-order delivery; re-seen
// messages are cheap (deduped at index time).
func (c *MailConnector) applyIncrementalWindow(ctx context.Context, cfg *mailpkg.BatchIndexConfig, accountID, provider string, full bool) {
	if full || c.store == nil || accountID == "" {
		return
	}
	_, lastIndexed, err := c.store.CountMailItemsByAccount(ctx, accountID, provider)
	if err != nil || lastIndexed.IsZero() {
		return
	}
	cfg.Since = lastIndexed.Add(-48 * time.Hour)
	cfg.ReconcileDeletions = false
}

// syncLegacy is the original Sync body, extracted so it can be wrapped by
// the digest-capture helper.
func (c *MailConnector) syncLegacy(ctx context.Context, opts plugin.SyncOptions) (*plugin.SyncResult, error) {
	c.mu.RLock()
	activeSource := c.activeSource
	indexer := c.indexer
	protonConn := c.proton
	gmailConn := c.gmail
	configuredLimit := c.config.Settings["limit"]
	// Capture provider-specific mailbox config inside the lock (C2 fix).
	var rawMailboxes string
	switch activeSource {
	case "gmail":
		rawMailboxes = c.config.Settings["gmail_mailbox"]
	case "proton":
		rawMailboxes = c.config.Settings["proton_mailbox"]
	}
	c.mu.RUnlock()
	cfgMailboxes := strings.Split(rawMailboxes, ",")
	var mailboxes []string
	if len(cfgMailboxes) > 0 && cfgMailboxes[0] != "" {
		// User configured specific mailboxes/labels.
		for _, mb := range cfgMailboxes {
			mb = strings.TrimSpace(mb)
			if mb != "" {
				mailboxes = append(mailboxes, mb)
			}
		}
	}

	c.logger.Info().Str("provider", activeSource).Msg("sync started")

	if indexer == nil {
		c.logger.Error().Msg("sync failed: indexer not configured")
		return nil, fmt.Errorf("mail connector Sync: indexer not configured")
	}
	c.logger.Info().Msg("indexer configured, resolving configuration")

	// Resolve limit: caller > user config > 0 (unlimited).
	limit := opts.Limit
	if limit <= 0 {
		if n, err := strconv.Atoi(configuredLimit); err == nil && n > 0 {
			limit = n
		}
		// 0 is intentional: connectors treat it as "fetch everything".
	}

	// If user hasn't specified mailboxes via config OR via opts, use provider-specific defaults.
	if len(mailboxes) == 0 {
		if opts.Mailbox != "" {
			mailboxes = []string{opts.Mailbox}
		} else {
			switch activeSource {
			case "gmail":
				// Empty MailboxID → Gmail API applies no label filter → all threads.
				mailboxes = []string{""}
			case "proton":
				c.logger.Info().Msg("resolving proton mailboxes")
				mailboxes = resolveProtonMailboxes(ctx, protonConn)
			}
		}
	}

	cfg := mailpkg.BatchIndexConfig{
		BatchSize:          10,
		MaxConcurrent:      3,
		Timeout:            30 * time.Second,
		Limit:              limit,
		ReconcileDeletions: c.reconcileDeletions,
	}
	c.applyIncrementalWindow(ctx, &cfg, activeSource, activeSource, opts.Full)

	c.logger.Info().Msg("selecting mail provider")

	// Determine which providers to sync
	type providerInfo struct {
		name    string
		conn    mailpkg.MailConnector
		session *mailpkg.Session
	}
	var providers []providerInfo

	switch activeSource {
	case "gmail":
		// Only sync Gmail when it actually has credentials; the connector
		// object always exists (created in main.go) but may be credential-free.
		if gmailConn != nil && gmailConn.GetRefreshToken() != "" {
			providers = append(providers, providerInfo{name: "gmail", conn: gmailConn, session: c.gmailSession})
		} else {
			c.logger.Warn().Msg("sync skipped: gmail has no refresh token — re-authenticate via Connectors → Mail")
			return &plugin.SyncResult{}, nil
		}
	case "proton":
		if protonConn != nil {
			providers = append(providers, providerInfo{name: "proton", conn: protonConn, session: c.protonSession})
		}
	default:
		// No legacy single-source provider (activeSource is "mailapp" or unset).
		// Multi-account providers are synced via syncAllAccounts/syncAllProvider,
		// so the legacy path is a clean no-op here rather than a spurious error.
		c.logger.Debug().Str("activeSource", activeSource).Msg("legacy sync: no single-source provider, skipping")
		return &plugin.SyncResult{}, nil
	}

	start := time.Now()

	var totalProcessed, totalSkipped, totalFailed int
	for _, provider := range providers {
		currentSource := provider.name

		c.logger.Info().Str("provider", currentSource).Msg("syncing provider")

		// Ensure connected
		if provider.session != nil {
			if err := provider.session.EnsureConnected(ctx); err != nil {
				c.logger.Error().Err(err).Str("provider", currentSource).Msg("connect failed")
				c.mu.Lock()
				c.health.Status = plugin.StatusDegraded
				c.health.Message = err.Error()
				c.mu.Unlock()
				totalFailed++
				continue
			}
		}

		// Resolve mailboxes for this provider
		var providerMailboxes []string
		if len(cfgMailboxes) > 0 && cfgMailboxes[0] != "" {
			providerMailboxes = mailboxes
		} else if opts.Mailbox != "" {
			providerMailboxes = []string{opts.Mailbox}
		} else {
			switch currentSource {
			case "gmail":
				providerMailboxes = []string{""}
			case "proton":
				providerMailboxes = resolveProtonMailboxes(ctx, protonConn)
			}
		}

		// For Gmail, route mailbox IDs as LabelIds() API parameters (C3 fix).
		syncCfg := cfg
		syncCfg.AccountID = "" // legacy path: no per-account ID
		if currentSource == "gmail" && len(providerMailboxes) > 0 && providerMailboxes[0] != "" {
			syncCfg.LabelIDs = providerMailboxes
			providerMailboxes = []string{""}
		}

		mbIndexer := mailpkg.NewMailboxIndexer(indexer, provider.conn)

		for _, mailbox := range providerMailboxes {
			c.logger.Info().Str("mailbox", mailbox).Str("provider", currentSource).Msg("syncing mailbox")
			stats, err := mbIndexer.IndexMailbox(ctx, currentSource, mailbox, syncCfg)
			if err != nil {
				if isMailboxNotFound(err) {
					c.logger.Debug().Str("mailbox", mailbox).Str("provider", currentSource).
						Msg("mailbox absent; skipping")
					continue
				}
				c.logger.Error().Err(err).Str("mailbox", mailbox).Msg("mailbox sync failed")
				c.mu.Lock()
				c.health.Status = plugin.StatusDegraded
				c.health.Message = err.Error()
				c.mu.Unlock()
				totalFailed++
				continue
			}
			c.logger.Info().
				Str("mailbox", mailbox).
				Int("processed", stats.IndexedMessages).
				Int("skipped", stats.SkippedDuplicates).
				Int("errors", stats.Errors).
				Msg("mailbox sync completed")
			totalProcessed += stats.IndexedMessages
			totalSkipped += stats.SkippedDuplicates
			totalFailed += stats.Errors
		}
	}

	c.mu.Lock()
	if c.health.Status != plugin.StatusDegraded {
		c.health.Status = plugin.StatusHealthy
		c.health.Message = ""
	}
	c.health.LastSync = time.Now()
	c.health.ItemCount += int64(totalProcessed)
	c.health.ErrCount = int64(totalFailed)
	c.mu.Unlock()

	c.logger.Info().
		Int("processed", totalProcessed).
		Int("skipped", totalSkipped).
		Int("failed", totalFailed).
		Dur("duration", time.Since(start)).
		Str("provider", activeSource).
		Msg("sync completed")

	return &plugin.SyncResult{
		Processed: totalProcessed,
		Skipped:   totalSkipped,
		Failed:    totalFailed,
		Duration:  time.Since(start),
	}, nil
}

// LoadAccountsFromCredStore reads persisted MailAccount credentials and
// registers them in the registry. Safe to call multiple times — subsequent
// calls reconcile the in-memory state with on-disk credentials. Returns the
// number of accounts loaded.
func (c *MailConnector) LoadAccountsFromCredStore() (int, error) {
	if c.credStore == nil {
		return 0, nil
	}
	sessions, err := LoadAccountsFromStore(c.credStore)
	if err != nil {
		return 0, err
	}
	for _, s := range sessions {
		c.accounts.Register(s)
	}
	return len(sessions), nil
}

// SyncAccount runs Sync against a single account. Replaces the legacy
// activeSource-based Sync for the multi-account UI. mailbox/limit overrides
// come from opts; if both are empty the connector's default behaviour
// applies.
func (c *MailConnector) SyncAccount(ctx context.Context, accountID string, opts plugin.SyncOptions) (*plugin.SyncResult, error) {
	var (
		result  *plugin.SyncResult
		syncErr error
	)
	_ = c.captureAndPublishDigest(ctx, func() error {
		r, err := c.syncAccountInner(ctx, accountID, opts)
		result = r
		syncErr = err
		return err
	})
	return result, syncErr
}

// syncAccountInner is the original SyncAccount body, extracted so the
// digest-capture wrapper can encircle it without duplicating logic.
func (c *MailConnector) syncAccountInner(ctx context.Context, accountID string, opts plugin.SyncOptions) (*plugin.SyncResult, error) {
	sess, err := c.accounts.Get(accountID)
	if err != nil {
		return nil, err
	}

	c.mu.RLock()
	indexer := c.indexer
	c.mu.RUnlock()
	if indexer == nil {
		return nil, fmt.Errorf("mail connector SyncAccount: indexer not configured")
	}

	if sess.Session != nil {
		if err := sess.Session.EnsureConnected(ctx); err != nil {
			c.markAccountError(sess, err)
			return nil, fmt.Errorf("connect %s: %w", accountID, err)
		}
	} else if !sess.Conn.IsConnected() {
		if err := sess.Conn.Connect(ctx); err != nil {
			c.markAccountError(sess, err)
			return nil, fmt.Errorf("connect %s: %w", accountID, err)
		}
	}

	// Resolve label IDs: prefer opts.Labels, then fall back to connector config.
	var labelIDs []string
	if len(opts.Labels) > 0 {
		labelIDs = opts.Labels
	} else if sess.Provider == "gmail" {
		c.mu.RLock()
		raw := c.config.Settings["gmail_mailbox"]
		c.mu.RUnlock()
		if raw != "" {
			for _, l := range strings.Split(raw, ",") {
				if l = strings.TrimSpace(l); l != "" {
					labelIDs = append(labelIDs, l)
				}
			}
		}
	}

	mailboxes := []string{opts.Mailbox}
	if opts.Mailbox == "" {
		switch sess.Provider {
		case "gmail":
			// For Gmail we use LabelIDs via the API; pass a single empty mailbox
			// so IndexMailbox runs once with the label filter applied.
			mailboxes = []string{""}
		case "proton":
			if pc, ok := sess.Conn.(*proton.IMAPConnector); ok {
				mailboxes = resolveProtonMailboxes(ctx, pc)
			} else {
				mailboxes = []string{defaultMailbox}
			}
		case "mailapp":
			// Honour the user-selected folder list (mailapp_mailbox, comma-
			// separated) applied to every Mail.app account. Empty preserves the
			// prior behaviour (the connector's own default mailbox).
			c.mu.RLock()
			raw := c.config.Settings["mailapp_mailbox"]
			c.mu.RUnlock()
			if folders := splitCSV(raw); len(folders) > 0 {
				mailboxes = folders
			}
		}
	}

	cfg := mailpkg.BatchIndexConfig{
		BatchSize:          10,
		MaxConcurrent:      3,
		Timeout:            30 * time.Second,
		Limit:              opts.Limit,
		AccountID:          sess.AccountID,
		LabelIDs:           labelIDs,
		ReconcileDeletions: c.reconcileDeletions,
	}
	c.applyIncrementalWindow(ctx, &cfg, sess.AccountID, sess.Provider, opts.Full)

	mbIndexer := mailpkg.NewMailboxIndexer(indexer, sess.Conn)
	start := time.Now()
	var totalProcessed, totalSkipped, totalFailed, totalEmbeddingFailed int
	var lastErr error

	for _, mailbox := range mailboxes {
		stats, err := mbIndexer.IndexMailbox(ctx, sess.AccountID, mailbox, cfg)
		if err != nil {
			if isMailboxNotFound(err) {
				// Folder absent on this account (the folder list spans accounts);
				// skip silently rather than spamming errors / looking stuck.
				c.logger.Debug().Str("account", sess.AccountID).Str("mailbox", mailbox).
					Msg("mailbox absent on this account; skipping")
				continue
			}
			lastErr = err
			totalFailed++
			c.logger.Error().
				Err(err).
				Str("account", sess.AccountID).
				Str("mailbox", mailbox).
				Msg("mailbox sync failed")
			continue
		}
		totalProcessed += stats.IndexedMessages
		totalSkipped += stats.SkippedDuplicates
		totalFailed += stats.Errors
		totalEmbeddingFailed += stats.EmbeddingErrors
	}

	c.accounts.mu.Lock()
	if lastErr != nil || (totalEmbeddingFailed > 0 && totalEmbeddingFailed*2 > totalProcessed+totalFailed) {
		sess.Health.Status = plugin.StatusDegraded
		if totalEmbeddingFailed*2 > totalProcessed+totalFailed {
			sess.BriefReason = diag.ReasonEmbeddingDown
			sess.Health.Message = string(diag.ReasonEmbeddingDown)
		} else {
			sess.BriefReason = diag.Classify(lastErr)
			sess.Health.Message = string(sess.BriefReason)
		}
	} else {
		sess.Health.Status = plugin.StatusHealthy
		sess.Health.Message = ""
		sess.BriefReason = diag.ReasonHealthy
	}
	sess.Health.LastSync = time.Now()
	sess.Health.ItemCount += int64(totalProcessed)
	sess.Health.ErrCount = int64(totalFailed)
	c.accounts.mu.Unlock()

	return &plugin.SyncResult{
		Processed: totalProcessed,
		Skipped:   totalSkipped,
		Failed:    totalFailed,
		Duration:  time.Since(start),
	}, nil
}

// markAccountError updates a session's health to reflect a connection error.
// Caller does NOT need to hold the registry lock.
func (c *MailConnector) markAccountError(sess *AccountSession, err error) {
	c.accounts.mu.Lock()
	defer c.accounts.mu.Unlock()
	sess.BriefReason = diag.Classify(err)
	sess.Health.Status = plugin.StatusUnhealthy
	sess.Health.Message = string(sess.BriefReason)
	sess.LastVerify = time.Now()
}

// VerifyAccount runs the registry-level VerifyAccount with this connector's
// classification. Convenience pass-through for handlers.
func (c *MailConnector) VerifyAccount(ctx context.Context, accountID string) (*AccountSession, error) {
	return c.accounts.VerifyAccount(ctx, accountID)
}

// ---------------------------------------------------------------------------
// plugin.Lister — list all threads
// ---------------------------------------------------------------------------

// List returns threads from the active mail provider converted to plugin.Item.
func (c *MailConnector) List(ctx context.Context, opts plugin.ListOptions) ([]plugin.Item, error) {
	c.mu.RLock()
	activeSource := c.activeSource
	c.mu.RUnlock()

	mailOpts := mailpkg.ListOptions{
		Limit:  opts.Limit,
		Offset: opts.Offset,
	}
	if !opts.Since.IsZero() {
		t := opts.Since
		mailOpts.Since = &t
	}

	var threads []mailpkg.Thread
	var err error

	switch activeSource {
	case "proton":
		c.mu.RLock()
		conn := c.proton
		c.mu.RUnlock()
		if conn == nil {
			return nil, fmt.Errorf("mail connector List: proton not initialised")
		}
		if c.protonSession != nil {
			if err := c.protonSession.EnsureConnected(ctx); err != nil {
				return nil, fmt.Errorf("mail connector List: proton connect failed: %w", err)
			}
		}
		threads, err = conn.ListThreads(ctx, mailOpts)
	case "gmail":
		c.mu.RLock()
		conn := c.gmail
		c.mu.RUnlock()
		if conn == nil {
			return nil, fmt.Errorf("mail connector List: gmail not initialised")
		}
		if c.gmailSession != nil {
			if err := c.gmailSession.EnsureConnected(ctx); err != nil {
				return nil, fmt.Errorf("mail connector List: gmail connect failed: %w", err)
			}
		}
		threads, err = conn.ListThreads(ctx, mailOpts)
	case "mailapp":
		return nil, fmt.Errorf("mail connector List: mailapp uses the multi-account API; pass an account id")
	default:
		return nil, fmt.Errorf("mail connector List: activeSource not set")
	}

	if err != nil {
		return nil, fmt.Errorf("mail connector List: %w", err)
	}

	items := make([]plugin.Item, 0, len(threads))
	for _, t := range threads {
		items = append(items, threadToItem(t, c.Info().ID))
	}
	return items, nil
}

// threadToItem converts a mail.Thread into the universal plugin.Item.
func threadToItem(t mailpkg.Thread, connectorID string) plugin.Item {
	// Build attachment refs.
	var refs []plugin.AttachmentRef
	if t.HasAttachments {
		// We only know attachments exist; their details require a full fetch.
		refs = []plugin.AttachmentRef{}
	}

	return plugin.Item{
		ID:          t.ID,
		ConnectorID: connectorID,
		SourceType:  store.SourceTypeMail,
		Title:       t.Subject,
		Author:      strings.Join(t.Participants, ", "),
		CreatedAt:   t.DateRange[0],
		UpdatedAt:   t.DateRange[1],
		Tags:        t.Labels,
		Attachments: refs,
		Metadata: map[string]any{
			"participants":    t.Participants,
			"message_count":   t.MessageCount,
			"has_attachments": t.HasAttachments,
			"mailbox":         t.Mailbox,
		},
	}
}

// ---------------------------------------------------------------------------
// plugin.Indexer
// ---------------------------------------------------------------------------

// Index indexes a single thread (identified by its native thread ID) into the
// knowledge base.
func (c *MailConnector) Index(ctx context.Context, itemID string) error {
	_, err := c.IndexBatch(ctx, []string{itemID})
	return err
}

// IndexBatch indexes multiple threads; it continues on partial errors and
// aggregates them in the returned IndexResult.
func (c *MailConnector) IndexBatch(ctx context.Context, itemIDs []string) (*plugin.IndexResult, error) {
	c.mu.RLock()
	activeSource := c.activeSource
	indexer := c.indexer
	c.mu.RUnlock()

	if indexer == nil {
		return nil, fmt.Errorf("mail connector IndexBatch: indexer not configured")
	}

	var connector mailpkg.MailConnector
	switch activeSource {
	case "proton":
		c.mu.RLock()
		connector = c.proton
		c.mu.RUnlock()
	case "gmail":
		c.mu.RLock()
		connector = c.gmail
		c.mu.RUnlock()
	case "mailapp":
		return nil, fmt.Errorf("mail connector IndexBatch: mailapp uses the multi-account API; index per account via SyncAccount")
	default:
		return nil, fmt.Errorf("mail connector IndexBatch: activeSource not set")
	}

	result := &plugin.IndexResult{}

	for _, id := range itemIDs {
		thread, err := connector.GetThread(ctx, id)
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, plugin.IndexError{
				ItemID:  id,
				Message: fmt.Sprintf("GetThread: %v", err),
			})
			continue
		}

		messages, err := connector.GetMessagesByThread(ctx, thread)
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, plugin.IndexError{
				ItemID:  id,
				Message: fmt.Sprintf("GetMessagesByThread: %v", err),
			})
			continue
		}

		if _, err := indexer.IndexThread(ctx, thread, messages, ""); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, plugin.IndexError{
				ItemID:  id,
				Message: fmt.Sprintf("IndexThread: %v", err),
			})
			continue
		}

		result.Indexed++
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// plugin.Summarizer
// ---------------------------------------------------------------------------

// Summarize generates a structured summary of a thread using the configured
// LLM model.  It fetches the thread from the active provider before calling
// the SummarizeThreadTool.
func (c *MailConnector) Summarize(ctx context.Context, itemID string, modelID string) (*plugin.Summary, error) {
	c.mu.RLock()
	activeSource := c.activeSource
	summarizeTool := c.summarizeTool
	c.mu.RUnlock()

	if summarizeTool == nil {
		return nil, fmt.Errorf("mail connector Summarize: summarizeTool not configured")
	}

	var connector mailpkg.MailConnector
	switch activeSource {
	case "proton":
		c.mu.RLock()
		connector = c.proton
		c.mu.RUnlock()
	case "gmail":
		c.mu.RLock()
		connector = c.gmail
		c.mu.RUnlock()
	case "mailapp":
		return nil, fmt.Errorf("mail connector Summarize: mailapp uses the multi-account API; summarize per account via the registry")
	default:
		return nil, fmt.Errorf("mail connector Summarize: activeSource not set")
	}

	thread, err := connector.GetThread(ctx, itemID)
	if err != nil {
		return nil, fmt.Errorf("mail connector Summarize GetThread %q: %w", itemID, err)
	}

	messages, err := connector.GetMessagesByThread(ctx, thread)
	if err != nil {
		return nil, fmt.Errorf("mail connector Summarize GetMessagesByThread %q: %w", itemID, err)
	}

	storeSummary, err := summarizeTool.Run(ctx, thread, messages, modelID)
	if err != nil {
		return nil, fmt.Errorf("mail connector Summarize: %w", err)
	}

	return &plugin.Summary{
		Text:      formatSummaryText(storeSummary.Decisions, storeSummary.Actions, storeSummary.OpenQuestions),
		ModelID:   modelID,
		CreatedAt: storeSummary.CreatedAt,
	}, nil
}

// formatSummaryText serialises the structured LLM output into a readable string
// for plugin.Summary.Text consumers.
func formatSummaryText(decisions, actions, openQuestions []string) string {
	var sb strings.Builder

	if len(decisions) > 0 {
		sb.WriteString("Decisions:\n")
		for _, d := range decisions {
			sb.WriteString("- ")
			sb.WriteString(d)
			sb.WriteByte('\n')
		}
	}
	if len(actions) > 0 {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString("Actions:\n")
		for _, a := range actions {
			sb.WriteString("- ")
			sb.WriteString(a)
			sb.WriteByte('\n')
		}
	}
	if len(openQuestions) > 0 {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString("Open questions:\n")
		for _, q := range openQuestions {
			sb.WriteString("- ")
			sb.WriteString(q)
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// plugin.Attacher
// ---------------------------------------------------------------------------

// ListAttachments lists all attachments for a given thread ID.
func (c *MailConnector) ListAttachments(ctx context.Context, itemID string) ([]plugin.Attachment, error) {
	c.mu.RLock()
	activeSource := c.activeSource
	attachTool := c.attachTool
	c.mu.RUnlock()

	if attachTool == nil {
		return nil, fmt.Errorf("mail connector ListAttachments: attachTool not configured")
	}

	req := tools.ListAttachmentsRequest{
		ThreadID: itemID,
		Source:   activeSource,
	}

	resp, err := attachTool.Run(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("mail connector ListAttachments: %w", err)
	}

	attachments := make([]plugin.Attachment, 0, len(resp.Attachments))
	for _, a := range resp.Attachments {
		attachments = append(attachments, plugin.Attachment{
			AttachmentRef: plugin.AttachmentRef{
				ID:       a.ID,
				Name:     a.Filename,
				MimeType: a.MIMEType,
				Size:     a.Size,
			},
			ConnectorID: c.Info().ID,
			ItemID:      itemID,
		})
	}
	return attachments, nil
}

// DownloadAttachment returns an io.ReadCloser for the attachment content.
// NOTE: Neither the Proton IMAP nor the Gmail connector currently exposes a
// generic attachment download method.  This stub returns an explicit error so
// that callers can handle the case gracefully.  Implement once the underlying
// connectors expose download functionality.
func (c *MailConnector) DownloadAttachment(_ context.Context, attachmentID string) (io.ReadCloser, plugin.Attachment, error) {
	return nil, plugin.Attachment{}, fmt.Errorf(
		"mail connector DownloadAttachment: not implemented for attachment %q — "+
			"mail connectors (proton/gmail) do not expose a download method yet",
		attachmentID,
	)
}

// ---------------------------------------------------------------------------
// plugin.Authenticator
// ---------------------------------------------------------------------------

// AuthURL returns the OAuth2 authorisation URL for Gmail.
// For Proton (password-based), it returns an empty string.
func (c *MailConnector) AuthURL(ctx context.Context) (string, error) {
	c.mu.RLock()
	activeSource := c.activeSource
	gmailConn := c.gmail
	c.mu.RUnlock()

	if activeSource != "gmail" || gmailConn == nil {
		return "", nil
	}

	// Use a random-looking state derived from context deadline; a real
	// implementation should use crypto/rand for CSRF protection.
	state := fmt.Sprintf("hygur-mail-%d", time.Now().UnixNano())
	return gmailConn.GetAuthURL(state), nil
}

// ExchangeCode exchanges an OAuth2 authorisation code (Gmail) or is a no-op
// for password-based providers.  On success the refresh token is persisted in
// the credential store.
func (c *MailConnector) ExchangeCode(ctx context.Context, code string) error {
	c.mu.RLock()
	activeSource := c.activeSource
	gmailConn := c.gmail
	credStore := c.credStore
	c.mu.RUnlock()

	if activeSource != "gmail" {
		// Password-based providers do not use a code exchange.
		return nil
	}

	if gmailConn == nil {
		return fmt.Errorf("mail connector ExchangeCode: gmail connector not initialised")
	}

	if err := gmailConn.ExchangeCode(ctx, code); err != nil {
		return fmt.Errorf("mail connector ExchangeCode: %w", err)
	}

	// Persist credentials so they survive a restart.
	if credStore != nil {
		clientID, clientSecret := gmailConn.GetOAuthConfig()
		refreshToken := gmailConn.GetRefreshToken()
		fields := map[string]string{
			"refresh_token": refreshToken,
			"client_id":     clientID,
			"client_secret": clientSecret,
		}
		if err := credStore.SaveConnectorCredential("mail", fields); err != nil {
			// Best-effort legacy save: the token is live in memory and we still
			// proceed to register the multi-account entry below.
			c.logger.Warn().Err(err).Msg("ExchangeCode: legacy credential save failed")
		}

		// Register in the multi-account registry so the account is immediately
		// available for verify/sync without requiring a restart (C1 fix).
		// Use a temporary connector so we never mutate the shared c.gmail singleton,
		// which avoids races with concurrent Start/Stop calls.
		profileCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		tmpConn := gmail.NewGmailConnector(clientID, clientSecret, "urn:ietf:wg:oauth:2.0:oob")
		tmpConn.SetRefreshToken(refreshToken)
		if connErr := tmpConn.Connect(profileCtx); connErr == nil {
			if email, profErr := tmpConn.GetProfileEmail(profileCtx); profErr == nil && email != "" {
				acctCred := auth.MailAccountCredential{
					AccountID:    email,
					Provider:     "gmail",
					Email:        email,
					RefreshToken: refreshToken,
					ClientID:     clientID,
					ClientSecret: clientSecret,
				}
				if saveErr := credStore.SaveMailAccountCredential(acctCred); saveErr != nil {
					c.logger.Warn().Err(saveErr).Msg("ExchangeCode: multi-account credential save failed")
				}
				if _, loadErr := c.LoadAccountsFromCredStore(); loadErr != nil {
					c.logger.Warn().Err(loadErr).Msg("ExchangeCode: account registry reload failed")
				}
			} else if profErr != nil {
				c.logger.Warn().Err(profErr).Msg("ExchangeCode: could not resolve profile email")
			}
		} else {
			c.logger.Warn().Err(connErr).Msg("ExchangeCode: connect failed; account registry not updated")
		}
	}

	return nil
}

// SetProtonSession injects a pre-built MailSession for the Proton connector.
// This enables automatic reconnection on every operation.
func (c *MailConnector) SetProtonSession(s *mailpkg.Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.protonSession = s
}

// SetReconcileDeletions toggles pruning of KB mail no longer present on the
// server after a full sweep (opt-in; see config.Mail.ReconcileDeletions).
func (c *MailConnector) SetReconcileDeletions(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reconcileDeletions = enabled
}

// SetGmailSession injects a pre-built MailSession for the Gmail connector.
// This enables automatic reconnection on every operation.
func (c *MailConnector) SetGmailSession(s *mailpkg.Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gmailSession = s
}

// SetDigestPipeline wires the broker, store, and summarizer used to
// aggregate priority_mail events into a single mail_digest at the end of
// each sync cycle. All three are required; passing any nil disables the
// digest path silently (the connector keeps working as before).
func (c *MailConnector) SetDigestPipeline(broker *events.Broker, db *store.DB, summarizer *retrieval.MailSummarizer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.broker = broker
	c.store = db
	c.summarizer = summarizer
}

// captureAndPublishDigest subscribes to priority_mail events for the
// duration of `runCycle`, then summarises each captured item and publishes
// a single mail_digest event. Returns runCycle's error untouched. When the
// digest pipeline isn't fully wired the call degrades to running runCycle
// directly with no aggregation.
//
// Per-mail summarisation is bounded by `summarizeCtx`'s deadline so a
// stuck LLM never blocks the sync from returning to the caller.
func (c *MailConnector) captureAndPublishDigest(ctx context.Context, runCycle func() error) error {
	c.mu.RLock()
	broker := c.broker
	db := c.store
	summarizer := c.summarizer
	c.mu.RUnlock()

	if broker == nil || db == nil || summarizer == nil {
		return runCycle()
	}

	sub := broker.SubscribeFor(events.EventTypePriorityMail)
	captured := make([]string, 0, 8)
	doneCapturing := make(chan struct{})

	// `wg` synchronises the capture goroutine with the main routine.
	// Without it, reading `captured` after close(doneCapturing) races with
	// the goroutine's final drain — the goroutine isn't guaranteed to have
	// returned just because we closed the signal channel.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-doneCapturing:
				// Drain anything still queued post-cycle, non-blocking.
				for {
					select {
					case evt := <-sub:
						if id, _ := evt.Data["content_id"].(string); id != "" {
							captured = append(captured, id)
						}
					default:
						return
					}
				}
			case evt, ok := <-sub:
				if !ok {
					return
				}
				if id, _ := evt.Data["content_id"].(string); id != "" {
					captured = append(captured, id)
				}
			}
		}
	}()

	cycleErr := runCycle()
	close(doneCapturing)
	wg.Wait()

	if len(captured) == 0 {
		return cycleErr
	}

	summarizeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	items := make([]events.MailDigestItem, 0, len(captured))
	seen := make(map[string]bool, len(captured))
	for _, contentID := range captured {
		if seen[contentID] {
			continue
		}
		seen[contentID] = true

		item, err := db.GetKnowledgeItem(summarizeCtx, contentID)
		if err != nil || item == nil {
			c.logger.Warn().Err(err).Str("content_id", contentID).Msg("digest: item lookup failed")
			continue
		}
		// The LLM judges relevance (and writes the line) in one call; drop
		// candidates it deems not notification-worthy — this is what keeps the
		// digest relevant beyond the cheap keyword pre-filter.
		oneLiner, notify := summarizer.SummarizeForNotification(summarizeCtx, item)
		if !notify {
			continue
		}
		if oneLiner == "" {
			oneLiner = "📧 " + item.Title
		}
		items = append(items, events.MailDigestItem{
			ContentID: contentID,
			OneLiner:  oneLiner,
		})
	}

	if len(items) == 0 {
		return cycleErr
	}

	broker.Publish(events.NewMailDigestEvent(events.MailDigestPayload{
		Count: len(items),
		Items: items,
	}))
	c.logger.Info().Int("count", len(items)).Msg("mail digest published")

	return cycleErr
}

// ---------------------------------------------------------------------------
// plugin.SecretFieldProvider
// ---------------------------------------------------------------------------

// SecretFieldKeys returns the configuration field keys that contain secrets.
// These keys are stripped from settings before they are persisted to config.yaml.
func (c *MailConnector) SecretFieldKeys() []string {
	return []string{"password", "client_secret"}
}

// IsAuthenticated returns true when the active provider has valid credentials
// and the last Start() call succeeded.
func (c *MailConnector) IsAuthenticated() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.health.Status != plugin.StatusHealthy && c.health.Status != plugin.StatusDegraded {
		return false
	}

	switch c.activeSource {
	case "proton":
		return c.proton != nil && c.proton.IsConnected()
	case "gmail":
		return c.gmail != nil && c.gmail.IsConnected()
	case "mailapp":
		// Authenticated when at least one mailapp account has been
		// discovered and registered. Per-account verification is handled
		// independently via VerifyAccount.
		for _, s := range c.accounts.All() {
			if s.Provider == "mailapp" {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// ListMailboxes returns the folders/labels available for the active provider —
// the source for the "select folders to sync" picker in the config UI. It
// ensures the connection is live first so it works right after the user enters
// credentials: Proton needs an open IMAP session; Gmail and Mail.app are
// stateless (API token / Apple Events).
func (c *MailConnector) ListMailboxes(ctx context.Context) ([]string, error) {
	c.mu.RLock()
	source := c.activeSource
	protonConn := c.proton
	gmailConn := c.gmail
	psess := c.protonSession
	c.mu.RUnlock()

	switch source {
	case "proton":
		if protonConn == nil {
			return []string{}, nil
		}
		if psess != nil {
			if err := psess.EnsureConnected(ctx); err != nil {
				return nil, err
			}
		}
		return protonConn.ListMailboxes(ctx)
	case "gmail":
		if gmailConn == nil {
			return []string{}, nil
		}
		labels, err := gmailConn.ListLabels(ctx)
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(labels))
		for _, l := range labels {
			names = append(names, l.Name)
		}
		return names, nil
	case "mailapp":
		return listMailappFolders(ctx)
	default:
		return []string{}, nil
	}
}

// listMailappFolders unions the folder names exposed by every Mail.app account,
// deduplicated, for the folder picker. Best-effort and bounded by the per-call
// osascript ceiling.
func listMailappFolders(ctx context.Context) ([]string, error) {
	accounts, err := mailapp.DiscoverAccounts(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	folders := []string{}
	for _, a := range accounts {
		for _, mb := range a.MailboxNames {
			if mb != "" && !seen[mb] {
				seen[mb] = true
				folders = append(folders, mb)
			}
		}
	}
	return folders, nil
}

// ListLabels returns available labels/mailboxes for the active provider.
// For Gmail, it returns all labels from the Gmail API.
// For Proton (IMAP), it returns all mailboxes as labels.
func (c *MailConnector) ListLabels(ctx context.Context) ([]mailpkg.Label, error) {
	c.mu.RLock()
	source := c.activeSource
	c.mu.RUnlock()

	switch source {
	case "gmail":
		conn := c.gmail
		if conn == nil {
			return []mailpkg.Label{}, nil
		}
		return conn.ListLabels(ctx)
	case "proton":
		conn := c.proton
		if conn == nil {
			return []mailpkg.Label{}, nil
		}
		mailboxes, err := conn.ListMailboxes(ctx)
		if err != nil {
			return []mailpkg.Label{}, nil
		}
		labels := make([]mailpkg.Label, 0, len(mailboxes))
		for _, mb := range mailboxes {
			labelType := "user"
			systemMailboxes := map[string]bool{
				"INBOX":    true,
				"Sent":     true,
				"Drafts":   true,
				"Trash":    true,
				"Spam":     true,
				"All Mail": true,
				"Starred":  true,
				"Archive":  true,
			}
			if systemMailboxes[mb] {
				labelType = "system"
			}
			labels = append(labels, mailpkg.Label{
				ID:   mb,
				Name: mb,
				Type: labelType,
			})
		}
		return labels, nil
	default:
		return []mailpkg.Label{}, nil
	}
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// resolveProtonMailboxes returns the IMAP mailboxes to sync for Proton.
// Prefers "All Mail" (covers all messages in one pass without per-folder
// duplication). Falls back to all mailboxes except noise folders (Spam,
// Trash, Drafts) whose content is already covered elsewhere.
func resolveProtonMailboxes(ctx context.Context, conn *proton.IMAPConnector) []string {
	mailboxes, err := conn.ListMailboxes(ctx)
	if err != nil || len(mailboxes) == 0 {
		return []string{defaultMailbox}
	}
	for _, mb := range mailboxes {
		if strings.EqualFold(mb, "All Mail") {
			return []string{mb}
		}
	}
	skip := map[string]bool{"Spam": true, "Trash": true, "Drafts": true}
	var result []string
	for _, mb := range mailboxes {
		if !skip[mb] {
			result = append(result, mb)
		}
	}
	if len(result) == 0 {
		return []string{defaultMailbox}
	}
	return result
}
