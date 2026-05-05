// Package main is the entry point for the Hygur sidecar application.
// The sidecar provides an HTTP API for communication between the Hygur
// macOS application and LM Studio for local LLM inference.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/hygur/sidecar/internal/agenda"
	"github.com/hygur/sidecar/internal/api"
	"github.com/hygur/sidecar/internal/api/handlers"
	"github.com/hygur/sidecar/internal/auth"
	"github.com/hygur/sidecar/internal/config"
	filesconnector "github.com/hygur/sidecar/internal/connectors/files"
	imapconnector "github.com/hygur/sidecar/internal/connectors/imap"
	mailconnector "github.com/hygur/sidecar/internal/connectors/mail"
	notesconnector "github.com/hygur/sidecar/internal/connectors/notes"
	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/health"
	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/ingest/parsers"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/mail"
	"github.com/hygur/sidecar/internal/mail/gmail"
	"github.com/hygur/sidecar/internal/mail/proton"
	"github.com/hygur/sidecar/internal/plugin"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/scheduler"
	"github.com/hygur/sidecar/internal/session"
	"github.com/hygur/sidecar/internal/store"
	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"
)

func main() {
	// Set up signal handling for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize structured logger
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()

	// Determine data directory early — needed to locate the canonical config file.
	// On macOS the standard location is ~/Library/Application Support/Hygur/.
	// A one-time silent migration copies existing data from ~/.hygur/ when needed.
	dataDir, err := resolveDataDir(logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to resolve data directory")
	}
	configPath := filepath.Join(dataDir, "config.yaml")

	// Ensure the data-dir config.yaml contains all sections (server, lm_studio, etc.)
	// by merging from the local ./config.yaml when they are missing.
	// This is a one-time bootstrap that keeps read and write paths consistent.
	bootstrapDataDirConfig(configPath, logger)

	// Load configuration from the canonical data-dir path so that SaveConnectorsConfig
	// writes to the same file that this process reads from.
	cfg, err := config.LoadWithOptions(&config.LoadOptions{ConfigPath: configPath})
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to load config")
	}

	// Always derive the SQLite path from the resolved data dir so it follows
	// the migration even when config.yaml still points to the old location.
	cfg.Store.Path = filepath.Join(dataDir, "hygur.db")

	// Configure log level
	level, err := zerolog.ParseLevel(cfg.Logging.Level)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	embURL := cfg.LMStudio.EmbeddingURL
	if embURL == "" {
		embURL = cfg.LMStudio.URL + " (shared)"
	}
	logger.Info().
		Str("host", cfg.Server.Host).
		Int("port", cfg.Server.Port).
		Str("inference_url", cfg.LMStudio.URL).
		Str("embedding_url", embURL).
		Msg("hygur sidecar starting")

	// Initialize authentication token
	token, err := auth.EnsureToken(dataDir)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize authentication token")
	}

	tokenPath := filepath.Join(dataDir, auth.TokenFileName)
	logger.Info().
		Str("token_file", tokenPath).
		Msg("authentication token initialized")

	// Create LLM client
	llmClient := llm.NewClient(&cfg.LMStudio)

	// Check LM Studio connectivity
	available, err := llmClient.Ping(ctx)
	if err != nil {
		logger.Warn().Err(err).Msg("failed to ping LM Studio")
	} else if !available {
		logger.Warn().
			Str("url", cfg.LMStudio.URL).
			Msg("LM Studio is not available - some features may not work")
	} else {
		logger.Info().
			Str("url", cfg.LMStudio.URL).
			Msg("LM Studio connection verified")
	}

	// Initialize SQLite store
	db, err := store.NewDB(cfg.Store.Path)
	if err != nil {
		logger.Fatal().Err(err).Str("path", cfg.Store.Path).Msg("failed to initialize database")
	}
	defer db.Close()
	logger.Info().Str("path", cfg.Store.Path).Msg("database initialized")

	// Create ingestor with store, embeddings, and register parsers
	ingestor := ingest.NewIngestorWithEmbeddings(db, llmClient)
	ingestor.RegisterParser(parsers.NewMarkdownParser())
	ingestor.RegisterParser(parsers.NewTXTParser())
	ingestor.RegisterParser(parsers.NewPDFParser())
	ingestor.RegisterParser(parsers.NewDOCXParser())

	// Create the semantic (vector-only) searcher used by the legacy /knowledge/search endpoint.
	searcher := retrieval.NewHybridSearcher(db, llmClient)

	// Create handlers
	knowledgeHandler := handlers.NewKnowledgeHandler(db, ingestor, searcher, logger)
	projectHandler := handlers.NewProjectHandler(db, logger)

	// Create mail components
	embeddingService := llm.NewEmbeddingService(llmClient, db)
	threadNormalizer := mail.NewThreadNormalizer()
	emailIndexer := mail.NewEmailIndexer(db, threadNormalizer, embeddingService, logger)
	summarizeTool := tools.NewSummarizeThreadTool(llmClient, db)

	// Create mail connectors wrapped with MailSession for auto-reconnection.
	// Each Session manages a single persistent connection with automatic reconnection.
	protonConnector := proton.NewDefaultIMAPConnector()
	gmailConnector := gmail.NewGmailConnector("", "", "") // Credentials set via API

	// Wrap each connector with a MailSession for auto-reconnection.
	// The same connector instance is shared between the session and the connector
	// struct, so state is consistent.
	protonSession := mail.NewSession(protonConnector, logger)
	gmailSession := mail.NewSession(gmailConnector, logger)

	// Create mail handler and wire dependencies
	mailHandler := handlers.NewMailHandler(logger)
	mailHandler.SetConnector("proton", protonConnector)
	mailHandler.SetConnector("gmail", gmailConnector)
	mailHandler.SetIndexer(emailIndexer)
	mailHandler.SetSummarizeTool(summarizeTool)

	// Initialize credential store if HYGUR_CRED_KEY is set
	credStore, err := auth.NewCredentialStore(dataDir)
	if err != nil {
		logger.Warn().Err(err).Msg("credential storage not available - set HYGUR_CRED_KEY to enable")
	} else {
		mailHandler.SetCredentialStore(credStore)
		logger.Info().Msg("credential storage initialized")
	}

	// Create list attachments tool with all connectors
	listAttachmentsTool := tools.NewListAttachmentsTool(map[string]mail.MailConnector{
		"proton": protonConnector,
		"gmail":  gmailConnector,
	})
	mailHandler.SetListAttachmentsTool(listAttachmentsTool)

	// Create notes tool early — needed by both NotesHandler and plugin manager.
	createNoteTool := tools.NewCreateNoteToolWithEmbeddings(db, embeddingService)

	// Plugin manager — registers connector adapters and loads saved configs.
	pluginManager := plugin.NewManager(credStore, logger)

	mailConn := mailconnector.New(protonConnector, gmailConnector, emailIndexer, summarizeTool, listAttachmentsTool, credStore, logger)
	// Wire up connection pooling with auto-reconnection.
	mailConn.SetProtonSession(protonSession)
	mailConn.SetGmailSession(gmailSession)

	// Migrate legacy credentials to the multi-account schema (idempotent).
	if credStore != nil {
		if migRes, mErr := mailconnector.MigrateLegacyCredentials(ctx, credStore, db.SQLDB(), logger); mErr != nil {
			logger.Warn().Err(mErr).Msg("legacy mail credentials migration failed")
		} else if migRes.ProtonMigrated || migRes.GmailMigrated {
			logger.Info().
				Bool("proton", migRes.ProtonMigrated).
				Bool("gmail", migRes.GmailMigrated).
				Int64("knowledge_items_moved", migRes.KnowledgeItemsMoved).
				Msg("legacy mail credentials migrated to multi-account schema")
		}
		if n, lErr := mailConn.LoadAccountsFromCredStore(); lErr != nil {
			logger.Warn().Err(lErr).Msg("loading mail accounts from credential store failed")
		} else if n > 0 {
			logger.Info().Int("accounts", n).Msg("mail accounts registered")
		}
	}

	// Wire the multi-account runner + per-account counts onto the mail handler.
	mailHandler.SetAccountRunner(mailconnector.AsAccountRunner(mailConn))
	mailHandler.SetAccountCounts(db)

	notesConn := notesconnector.New(createNoteTool, db, embeddingService)
	filesConn := filesconnector.New(ingestor, db)
	imapConn := imapconnector.New(db, nil, logger) // broker wired below after events setup
	_ = pluginManager.Register(mailConn)
	_ = pluginManager.Register(notesConn)
	_ = pluginManager.Register(filesConn)
	_ = pluginManager.Register(imapConn)

	// Register IMAP factory for multi-instance support.
	// Broker is nil here; dynamic instances get it wired below via SetBroker iteration.
	pluginManager.RegisterFactory("imap", func() plugin.Connector {
		return imapconnector.New(db, nil, logger)
	})

	// Apply connector configs persisted in config.yaml.
	for id, settings := range cfg.Connectors {
		_ = pluginManager.Configure(id, plugin.ConnectorConfig{
			Enabled:  settings.Enabled,
			Settings: settings.Settings,
			Schedule: settings.Schedule,
		})
	}

	// Reload dynamic connector instances from config.yaml (multi-compte).
	for _, inst := range cfg.ConnectorInstances {
		if err := pluginManager.CreateInstance(inst.TypeName, inst.ID, inst.DisplayName, plugin.ConnectorConfig{
			Enabled:  inst.Enabled,
			Settings: inst.Settings,
			Schedule: inst.Schedule,
		}); err != nil {
			logger.Warn().Err(err).Str("instance", inst.ID).Str("type", inst.TypeName).Msg("failed to load connector instance")
		}
	}

	if err := pluginManager.Start(ctx); err != nil {
		logger.Warn().Err(err).Msg("plugin manager start error")
	}
	defer pluginManager.Stop(context.Background())

	connectorHandler := handlers.NewConnectorHandler(pluginManager, credStore, configPath, logger)
	marketplaceHandler := handlers.NewMarketplaceHandler(pluginManager, logger)

	// Create notes handler with embedding support for RAG search
	notesHandler := handlers.NewNotesHandler(createNoteTool, logger)
	notesHandler.SetStore(db)
	notesHandler.SetEmbeddingService(embeddingService)

	// Create unified searcher for RAG
	unifiedSearcher := retrieval.NewUnifiedSearcher(db, llmClient)
	unifiedSearcher.SetRetrievalOptions(retrieval.RetrievalOptions{
		UseLLMIntent:         cfg.Retrieval.UseLLMIntent,
		UseJudge:             cfg.Retrieval.UseJudge,
		EntitySearchFallback: cfg.Retrieval.EntitySearchFallback,
		EntitySearchMinScore: cfg.Retrieval.EntitySearchMinScore,
	})

	// Create search handler with unified search
	searchTool := tools.NewSearchTool(searcher)
	searchHandler := handlers.NewSearchHandler(searchTool, db, logger)
	searchHandler.SetUnifiedSearcher(unifiedSearcher)

	// Create RAG chat handler. Override the default scoring config from
	// the loaded YAML so users can A/B switch additive ↔ multiplicative.
	ragConfig := handlers.DefaultRAGConfig
	if cfg.Retrieval.TemporalScoringMode != "" {
		ragConfig.TemporalScoringMode = cfg.Retrieval.TemporalScoringMode
	}
	if cfg.Retrieval.CurrentStateFilterDays > 0 {
		ragConfig.CurrentStateFilterDays = cfg.Retrieval.CurrentStateFilterDays
	}

	// In-memory session-context accumulator. TTL of 2h is generous enough
	// for an active conversation while still freeing memory automatically.
	// State is intentionally not persisted across restarts.
	sessionStore := session.NewStore(2 * time.Hour)
	sessionStore.StartGC(ctx)

	ragChatHandler := handlers.NewRAGChatHandler(
		llmClient,
		unifiedSearcher,
		sessionStore,
		ragConfig,
		logger,
	)

	// Create tag handler
	tagHandler := handlers.NewTagHandler(db, logger)
	graphHandler := handlers.NewGraphHandler(db, logger)

	// Create memory handler
	memoryStoreTool := tools.NewMemoryStoreTool(db, llmClient)
	memorySearchTool := tools.NewMemorySearchTool(db)
	memoryHandler := handlers.NewMemoryHandler(db, logger)
	memoryHandler.SetTools(memoryStoreTool, memorySearchTool)

	// Persistent memory feeds into the chat handler so durable user facts get
	// injected into the system prompt AND new facts get auto-extracted at the
	// end of every turn.
	ragChatHandler.SetMemoryTools(memoryStoreTool, memorySearchTool)


	// Create API server
	server := api.NewServer(cfg, logger, token)
	server.SetLLMClient(llmClient)
	server.SetKnowledgeHandler(knowledgeHandler)
	server.SetProjectHandler(projectHandler)
	server.SetMailHandler(mailHandler)
	server.SetNotesHandler(notesHandler)
	server.SetSearchHandler(searchHandler)
	server.SetRAGChatHandler(ragChatHandler)
	server.SetTagHandler(tagHandler)
	server.SetGraphHandler(graphHandler)
	server.SetMemoryHandler(memoryHandler)
	server.SetConnectorHandler(connectorHandler)
	server.SetMarketplaceHandler(marketplaceHandler)

	// Timeline handler — consumes Phase 4 entity metadata to build a
	// chaptered chronological view (POST /timeline/query).
	timelineBuilder := retrieval.NewTimelineBuilder(unifiedSearcher, llmClient)
	timelineHandler := handlers.NewTimelineHandler(timelineBuilder, logger)
	server.SetTimelineHandler(timelineHandler)

	// Agenda handler — extracts upcoming deadlines from recent knowledge items
	// and exposes them via GET /agenda/context.
	agendaExtractor := agenda.NewExtractor(llmClient)
	agendaHandler := handlers.NewAgendaHandler(agendaExtractor, db, logger)
	server.SetAgendaHandler(agendaHandler)

	// Wire agenda extractor into the RAG chat handler for proactive injection.
	ragChatHandler.SetAgendaExtractor(agendaExtractor, db)

	// Create events broker for SSE notifications
	broker := events.NewBroker()
	eventsHandler := handlers.NewEventsHandler(broker, logger)
	server.SetEventsHandler(eventsHandler)

	// Wire up the events broker for connector events
	connectorHandler.SetBroker(broker)

	// Wire the broker into the mail indexer so it can emit priority_mail
	// events when freshly-indexed actionable accounting emails appear.
	emailIndexer.SetBroker(broker)

	// Wire the broker into the ingestor so /knowledge/index calls and
	// connector ingest cycles emit ingest_start / ingest_complete events
	// that the macOS Activity view consumes for live progress.
	ingestor.SetBroker(broker)

	// Wire the broker into the IMAP connector so sync completions emit events.
	imapConn.SetBroker(broker)

	// Wire broker into any dynamic IMAP instances loaded from config.
	for _, inst := range pluginManager.ListInstances() {
		if conn, ok := pluginManager.Get(inst.InstanceID); ok {
			if setter, ok := conn.(interface{ SetBroker(*events.Broker) }); ok {
				setter.SetBroker(broker)
			}
		}
	}

	// Wire the digest pipeline so each mail sync cycle ends with a
	// mail_digest event aggregating the priority_mail items it produced.
	mailSummarizer := retrieval.NewMailSummarizer(llmClient)
	mailConn.SetDigestPipeline(broker, db, mailSummarizer)

	// Background watcher for LM Studio reachability — emits a single event
	// each time up/down flips. The macOS app uses this to drive the menubar
	// status dot.
	lmWatcher := health.New(llmClient, broker, health.Options{
		URL:      cfg.LMStudio.URL,
		Interval: 10 * time.Second,
		Timeout:  3 * time.Second,
	}, logger)
	lmWatcher.Start(ctx)

	// Daily brief — always instantiated so the on-demand POST /brief/run
	// endpoint works even when the scheduled cron is opt-out. Only the
	// background scheduler honours `daily_brief.enabled`; the manual
	// trigger is always available (the macOS app drives "Run brief now"
	// and "Brief this project" through it).
	dailyBrief := scheduler.NewDailyBrief(db, llmClient, broker, cfg.DailyBrief, logger)
	if cfg.DailyBrief.Enabled {
		dailyBrief.Start(ctx)
	}
	briefHandler := handlers.NewBriefHandler(dailyBrief, logger)
	server.SetBriefHandler(briefHandler)

	// Agenda scheduler — runs daily at 08:00, emits agenda_alert events for
	// high-priority items due within the next 48 h.
	agendaScheduler := scheduler.NewAgendaScheduler(db, agendaExtractor, broker, "08:00", logger)
	agendaScheduler.Start(ctx)

	// Files periodic sync scheduler — triggers filesConn.Sync on a cron
	// schedule so locally-modified files are picked up without a manual sync.
	// Uses cfg.Connectors["files"].Schedule when set; falls back to "*/15 * * * *".
	// Fail-soft: if the connector is unconfigured or the broker is nil, a warning
	// is logged and the sidecar continues without periodic file sync.
	filesSchedule := ""
	if filesCfg, ok := cfg.Connectors["files"]; ok {
		filesSchedule = filesCfg.Schedule
	}
	filesScheduler := scheduler.NewFilesScheduler(filesConn, broker, filesSchedule, logger)
	filesScheduler.Start(ctx)

	// Start server (blocks until shutdown)
	if err := server.Start(ctx); err != nil {
		logger.Fatal().Err(err).Msg("server failed")
	}

	logger.Info().Msg("hygur sidecar stopped")
}

// resolveDataDir returns the canonical data directory for the sidecar:
//   - HYGUR_DATA_DIR env var if set
//   - ~/Library/Application Support/Hygur/ on macOS (standard location)
//   - ~/.hygur/ on other platforms or as a fallback
//
// If the new macOS path doesn't exist yet but the legacy ~/.hygur/ does,
// key data files are copied silently so existing users don't lose their data.
func resolveDataDir(logger zerolog.Logger) (string, error) {
	if d := os.Getenv("HYGUR_DATA_DIR"); d != "" {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", fmt.Errorf("create HYGUR_DATA_DIR: %w", err)
		}
		return d, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}

	var newDir string
	if runtime.GOOS == "darwin" {
		newDir = filepath.Join(home, "Library", "Application Support", "Hygur")
	} else {
		newDir = filepath.Join(home, ".hygur")
	}
	oldDir := filepath.Join(home, ".hygur")

	// Migrate legacy ~/.hygur/ → newDir if old data exists and new dir is absent.
	if newDir != oldDir {
		if _, err := os.Stat(newDir); os.IsNotExist(err) {
			if _, err := os.Stat(oldDir); err == nil {
				if mErr := migrateDataDir(oldDir, newDir, logger); mErr != nil {
					// Non-fatal: log and fall back to old dir.
					logger.Warn().Err(mErr).
						Str("old", oldDir).Str("new", newDir).
						Msg("data dir migration failed, using legacy dir")
					return oldDir, nil
				}
				logger.Info().
					Str("from", oldDir).Str("to", newDir).
					Msg("migrated data dir to ~/Library/Application Support/Hygur")
			}
		}
	}

	if err := os.MkdirAll(newDir, 0o700); err != nil {
		return "", fmt.Errorf("create data dir %s: %w", newDir, err)
	}
	return newDir, nil
}

// migrateDataDir copies key sidecar files from oldDir to newDir.
// Skips files that already exist in newDir (idempotent).
func migrateDataDir(oldDir, newDir string, logger zerolog.Logger) error {
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		return fmt.Errorf("mkdir new data dir: %w", err)
	}
	for _, f := range []string{"hygur.db", "config.yaml", "token"} {
		src := filepath.Join(oldDir, f)
		dst := filepath.Join(newDir, f)
		if _, err := os.Stat(src); err != nil {
			continue // src absent — skip
		}
		if _, err := os.Stat(dst); err == nil {
			continue // dst already exists — skip
		}
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copy %s: %w", f, err)
		}
		logger.Debug().Str("file", f).Msg("migrated data file")
	}
	return nil
}

// copyFile copies src to dst, creating dst with mode 0o600.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// bootstrapDataDirConfig ensures configPath contains all base sections (server,
// lm_studio, store, logging) by merging them from ./config.yaml when absent.
// This is a one-time migration that makes the data-dir config the single source
// of truth for both reads and writes, resolving the mismatch where connectors
// were saved to ~/.hygur/config.yaml but loaded from ./config.yaml.
func bootstrapDataDirConfig(configPath string, logger zerolog.Logger) {
	// Read data-dir config (may not exist yet, or may only have connectors).
	dataDirRaw := make(map[string]any)
	if data, err := os.ReadFile(configPath); err == nil {
		_ = yaml.Unmarshal(data, &dataDirRaw)
	}

	_, hasServer := dataDirRaw["server"]
	_, hasLMStudio := dataDirRaw["lm_studio"]
	if hasServer && hasLMStudio {
		return // Already complete — nothing to do.
	}

	// Read local ./config.yaml (development source config with all settings).
	localRaw := make(map[string]any)
	localData, err := os.ReadFile("config.yaml")
	if err != nil {
		return // No local config to merge from.
	}
	if err := yaml.Unmarshal(localData, &localRaw); err != nil {
		return
	}

	// Copy missing base sections from local config into data-dir config.
	for _, section := range []string{"server", "lm_studio", "store", "logging"} {
		if _, exists := dataDirRaw[section]; !exists {
			if v, ok := localRaw[section]; ok {
				dataDirRaw[section] = v
			}
		}
	}

	data, err := yaml.Marshal(dataDirRaw)
	if err != nil {
		logger.Warn().Err(err).Msg("bootstrapDataDirConfig: marshal failed")
		return
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		logger.Warn().Err(err).Msg("bootstrapDataDirConfig: mkdir failed")
		return
	}
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		logger.Warn().Err(err).Msg("bootstrapDataDirConfig: write failed")
		return
	}
	if err := os.Rename(tmp, configPath); err != nil {
		logger.Warn().Err(err).Msg("bootstrapDataDirConfig: rename failed")
		_ = os.Remove(tmp)
		return
	}
	logger.Info().Str("path", configPath).Msg("bootstrapped data-dir config from local config.yaml")
}
