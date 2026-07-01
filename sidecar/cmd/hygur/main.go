// Package main is the entry point for the Hygur sidecar application.
// The sidecar provides an HTTP API for communication between the Hygur
// macOS application and LM Studio for local LLM inference.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	_ "net/http/pprof" // registered on the pprof mux, only served when HYGUR_PPROF is set
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hygur/sidecar/internal/agenda"
	"github.com/hygur/sidecar/internal/api"
	"github.com/hygur/sidecar/internal/api/handlers"
	"github.com/hygur/sidecar/internal/auth"
	"github.com/hygur/sidecar/internal/config"
	caldavconnector "github.com/hygur/sidecar/internal/connectors/caldav"
	filesconnector "github.com/hygur/sidecar/internal/connectors/files"
	imapconnector "github.com/hygur/sidecar/internal/connectors/imap"
	mailconnector "github.com/hygur/sidecar/internal/connectors/mail"
	notesconnector "github.com/hygur/sidecar/internal/connectors/notes"
	"github.com/hygur/sidecar/internal/edge"
	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/health"
	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/ingest/parsers"
	"github.com/hygur/sidecar/internal/interactions"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/mail"
	"github.com/hygur/sidecar/internal/mail/gmail"
	"github.com/hygur/sidecar/internal/mail/proton"
	"github.com/hygur/sidecar/internal/plugin"
	"github.com/hygur/sidecar/internal/prose"
	"github.com/hygur/sidecar/internal/push"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/scheduler"
	"github.com/hygur/sidecar/internal/secret"
	"github.com/hygur/sidecar/internal/session"
	"github.com/hygur/sidecar/internal/store"
	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"
)

func main() {
	// Isolated PDF text extraction: re-invocations of this binary with the
	// "pdf-extract" subcommand run a short-lived child that parses one PDF from
	// stdin and exits. A malformed PDF / decompression bomb can make the pure-Go
	// parser allocate tens of GB, so we sandbox it in a process that self-caps
	// its heap and is killed on timeout — the sidecar is never touched. This
	// MUST run before any normal startup (no port bind, no DB).
	if len(os.Args) > 1 && os.Args[1] == parsers.PDFExtractSubcommand {
		parsers.RunPDFExtractSubprocess() // never returns
		return
	}

	// Operator/dev CLI for remote auth (self-hosting). These exit before any
	// server startup (no port bind, no DB).
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "gen-auth-key":
			runGenAuthKey()
			return
		case "issue-token":
			runIssueToken(os.Args[2:])
			return
		case "dek":
			runDEK(os.Args[2:])
			return
		case "edge":
			runEdge(os.Args[2:])
			return
		case "backup-db":
			runBackupDB(os.Args[2:])
			return
		case "rekey":
			runRekey(os.Args[2:])
			return
		case "usage":
			runUsage(os.Args[2:])
			return
		case "mail-breakdown":
			runMailBreakdown(os.Args[2:])
			return
		}
	}

	// Set up signal handling for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Soft heap cap so the sidecar stays small on modest machines. The mail
	// pipeline now bounds its per-thread working set, so this is a safety net
	// against accumulation rather than the primary control. Default 1 GiB;
	// override with HYGUR_MEM_LIMIT_MIB (0 disables). Go also honours GOMEMLIMIT
	// natively if set in the environment.
	memLimitMiB := 1024
	if v := os.Getenv("HYGUR_MEM_LIMIT_MIB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			memLimitMiB = n
		}
	}
	if memLimitMiB > 0 {
		debug.SetMemoryLimit(int64(memLimitMiB) << 20)
	}

	// Memory hygiene: Go keeps freed heap pages mapped by default, so a one-off
	// peak stays resident as RSS long after the work is done. Periodically hand
	// idle memory back to the OS so the footprint tracks the working set.
	go func() {
		t := time.NewTicker(90 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				debug.FreeOSMemory()
			}
		}
	}()

	// Optional pprof for memory/CPU profiling: set HYGUR_PPROF to a loopback
	// addr (e.g. "127.0.0.1:6060"), then `go tool pprof http://…/debug/pprof/heap`.
	if addr := os.Getenv("HYGUR_PPROF"); addr != "" {
		go func() {
			srv := &http.Server{Addr: addr, ReadHeaderTimeout: 5 * time.Second}
			_ = srv.ListenAndServe()
		}()
	}

	// Initialize structured logger
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()

	// Parent-process watchdog: when the macOS app force-quits or crashes, the
	// kernel re-parents us to launchd (PID 1) without sending SIGTERM. macOS
	// has no PR_SET_PDEATHSIG equivalent, so we poll. If our PPID flips to 1
	// (or any other unexpected value), trigger graceful shutdown so the HTTP
	// port is released — otherwise the next app launch can't bind.
	watchParent(ctx, stop, logger)

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

	// Couche B kill-switch (HYGUR_PROSE_TIDY): the deterministic prose cleanup is
	// process-wide, set once here from config (default on).
	prose.SetEnabled(cfg.Prose.Tidy)

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

	// Initialize the credential store (encrypted at rest). With HYGUR_CRED_KEY
	// unset it falls back to a machine-local key, so it is always available —
	// connector secrets and the LLM API key both live here, never in config.yaml.
	credStore, err := auth.NewCredentialStore(dataDir)
	if err != nil {
		logger.Warn().Err(err).Msg("credential storage not available")
	}

	// Resolve the LLM API key before building the client. Hosted providers
	// (Mistral, OpenAI…) need a bearer token; local runtimes don't. Precedence:
	// HYGUR_LMSTUDIO_API_KEY env (operator path — already bound into cfg by
	// viper) then the credential store (UI-entered, local desktop). config.yaml
	// is never consulted for it.
	if cfg.LMStudio.APIKey == "" && credStore != nil {
		if fields, gErr := credStore.GetConnectorCredential(auth.LLMCredentialID); gErr == nil {
			cfg.LMStudio.APIKey = fields[auth.LLMCredentialField]
		}
	}
	if cfg.LMStudio.APIKey != "" {
		logger.Info().Msg("LLM API key configured — bearer auth enabled for the inference endpoint")
	}

	// Create LLM client
	llmClient := llm.NewClient(&cfg.LMStudio)

	// Indexing client: a fast small model for ingestion-time Tier 2 NER
	// (lm_studio.indexing_url / model_indexing, e.g. minicpm5). A 1B model at
	// ~1000 tok/s across many connections processes bulk extraction far faster
	// than the big default model (~50 tok/s, few connections). Tier 2 is bounded
	// (≈6000 runes in, 1024 out, strict JSON + repair), so it stays within a
	// small model's context and instruction-following. Falls back to the default
	// client when neither indexing_url nor model_indexing is set.
	indexingClient := llmClient
	if cfg.LMStudio.IndexingURL != "" || cfg.LMStudio.ModelIndexing != "" {
		idxCfg := cfg.LMStudio
		if cfg.LMStudio.IndexingURL != "" {
			idxCfg.URL = cfg.LMStudio.IndexingURL
		}
		if cfg.LMStudio.ModelIndexing != "" {
			idxCfg.ModelDefault = cfg.LMStudio.ModelIndexing
		}
		// The indexing model gets its OWN reasoning_effort (e.g. "none" to disable
		// thinking on a reasoning-capable Tier-2 model like Nemotron-Nano), distinct
		// from the chat model (which may reject the field).
		idxCfg.ReasoningEffort = cfg.LMStudio.IndexingReasoningEffort
		indexingClient = llm.NewClient(&idxCfg)
		logger.Info().
			Str("url", idxCfg.URL).
			Str("model", idxCfg.ModelDefault).
			Msg("indexing model client configured for Tier 2 extraction")
	}

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

	// Initialize SQLite store via the per-identity Manager — the single seam
	// that maps an identity to its database file (file-per-identity isolation,
	// see store.Manager). Today there is one identity, so Default() returns the
	// primary hygur.db and every consumer is wired to it exactly as before;
	// multi-user routing onto per-identity handles is P5.
	//
	// Apply a staged restore (uploaded via POST /admin/db/restore) before the
	// store opens — swaps the snapshot in, keeping the current DB as a backup.
	if applied, rErr := store.ApplyPendingRestore(cfg.Store.Path); rErr != nil {
		logger.Error().Err(rErr).Str("path", cfg.Store.Path).Msg("failed to apply staged restore")
	} else if applied {
		logger.Info().Str("path", cfg.Store.Path).Msg("applied staged database restore")
	}

	// At-rest encryption key resolution: HYGUR_DB_KEY (cloud / tenant DEK) takes
	// precedence; otherwise the local desktop key from the OS keychain (set via
	// the WebUI "Encrypt local database" action). Empty = plaintext; a first keyed
	// run auto-migrates an existing plaintext DB (store.Open).
	keyStore := secret.Keychain{}
	dbKey := os.Getenv("HYGUR_DB_KEY")
	dbKeyEnvManaged := dbKey != ""
	if dbKey == "" {
		if k, ok := keyStore.DBKey(); ok {
			dbKey = k
		}
	}
	storeManager := store.NewManagerWithKey(cfg.Store.Path, dbKey)
	db, err := storeManager.Default()
	if err != nil {
		logger.Fatal().Err(err).Str("path", cfg.Store.Path).Msg("failed to initialize database")
	}
	defer storeManager.Close()
	keySource := "none"
	if dbKeyEnvManaged {
		keySource = "env" // cloud / tenant DEK
	} else if dbKey != "" {
		keySource = "keychain" // local opt-in
	}
	logger.Info().
		Str("path", cfg.Store.Path).
		Bool("encrypted", dbKey != "").
		Str("key_source", keySource).
		Msg("database initialized")

	// Token-usage accounting: every chat/embedding/indexing completion records
	// its token counts into SQLite so the Settings cost view persists across
	// restarts. The main client covers chat + embeddings; a distinct indexing
	// client (when configured) logs indexing completions under their own
	// category. Set before the server starts serving requests.
	usageSink := tokenUsageSink{db: db, logger: logger}
	llmClient.SetUsageRecorder(usageSink, store.TokenCategoryChat)
	if indexingClient != llmClient {
		indexingClient.SetUsageRecorder(usageSink, store.TokenCategoryIndexing)
	}

	// Bridge the vision config to the env vars the PDF/image parsers read, so
	// scanned-PDF and image OCR can use the local multimodal model (portable, no
	// system Tesseract needed). Defaults to the inference endpoint + default
	// model, since a multimodal model served there (e.g. nemotron-omni) handles
	// both chat and vision. Empty endpoint leaves OCR a fail-soft no-op.
	visionURL := cfg.LMStudio.VisionURL
	if visionURL == "" {
		visionURL = cfg.LMStudio.URL
	}
	visionModel := cfg.LMStudio.VisionModel
	if visionModel == "" {
		visionModel = cfg.LMStudio.ModelDefault
	}
	if visionURL != "" {
		_ = os.Setenv("HYGUR_VISION_ENDPOINT", visionURL)
		if visionModel != "" {
			_ = os.Setenv("HYGUR_VISION_MODEL", visionModel)
		}
		logger.Info().Str("vision_url", visionURL).Str("vision_model", visionModel).Msg("vision OCR fallback enabled")
	}

	// Create ingestor with store, embeddings, and register parsers
	ingestor := ingest.NewIngestorWithEmbeddings(db, llmClient)
	ingestor.SetIndexingClient(indexingClient) // Tier 2 NER on the fast small model
	ingestor.RegisterParser(parsers.NewMarkdownParser())
	ingestor.RegisterParser(parsers.NewTXTParser())
	ingestor.RegisterParser(parsers.NewPDFParser())
	ingestor.RegisterParser(parsers.NewDOCXParser())
	// Image OCR (.png/.jpg/.jpeg/.heic/.webp): Tesseract if installed, else the
	// vision model (same endpoint/model the scanned-PDF OCR uses). Without this
	// registration, image uploads fail with "no parser available for file type".
	ingestor.RegisterParser(parsers.NewImageParserWithModel(visionURL, visionModel))
	// Audio transcription (.mp3/.m4a/.wav/.ogg). Primary path: native ASR via the
	// multimodal chat model (gemma-4-12B-it does ASR) using Google's ASR prompt;
	// falls back to a Whisper /v1/audio/transcriptions endpoint if that fails.
	// HYGUR_AUDIO_ENDPOINT overrides the endpoint (defaults to the inference URL);
	// HYGUR_ASR_LANGUAGE sets the transcription language (default "French").
	audioURL := os.Getenv("HYGUR_AUDIO_ENDPOINT")
	if audioURL == "" {
		audioURL = cfg.LMStudio.AudioURL
	}
	if audioURL == "" {
		audioURL = cfg.LMStudio.URL
	}
	// chat-ASR via the multimodal chat model (Sparky/local default); disabled on a
	// managed backend whose chat model has no audio (gemma-4-31B) → Whisper only.
	audioChatModel := ""
	if cfg.LMStudio.AudioChatASR {
		audioChatModel = cfg.LMStudio.ModelDefault
	}
	ingestor.RegisterParser(
		parsers.NewAudioParserWithModel(audioURL, audioChatModel).
			WithAuth(cfg.LMStudio.APIKey).
			WithWhisperModel(cfg.LMStudio.AudioModel))

	// Create the semantic (vector-only) searcher used by the legacy /knowledge/search endpoint.
	searcher := retrieval.NewHybridSearcher(db, llmClient)

	// Create handlers
	knowledgeHandler := handlers.NewKnowledgeHandler(db, ingestor, searcher, logger)
	// Uploaded files (WebUI paperclip) are saved here before ingestion so their
	// source_path stays valid after the request.
	knowledgeHandler.SetUploadDir(filepath.Join(dataDir, "uploads"))
	projectHandler := handlers.NewProjectHandler(db, logger)
	taskHandler := handlers.NewTaskHandler(db, logger)

	// Create mail components
	embeddingService := llm.NewEmbeddingService(llmClient, db)
	// Tasks are note-like knowledge_items — index their bodies like notes.
	taskHandler.SetEmbeddingService(embeddingService)
	threadNormalizer := mail.NewThreadNormalizer()
	emailIndexer := mail.NewEmailIndexer(db, threadNormalizer, embeddingService, logger)
	emailIndexer.SetNotifyRecencyDays(cfg.Mail.NotifyRecencyDays)
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

	// Wire the credential store (created above) into the mail handler.
	if credStore != nil {
		mailHandler.SetCredentialStore(credStore)
		logger.Info().Msg("credential storage initialized")
	}

	// Create list attachments tool with all connectors. The same map backs the
	// read-only summarize_thread tool adapter so both can resolve a thread id.
	mailConnectors := map[string]mail.MailConnector{
		"proton": protonConnector,
		"gmail":  gmailConnector,
	}
	listAttachmentsTool := tools.NewListAttachmentsTool(mailConnectors)
	mailHandler.SetListAttachmentsTool(listAttachmentsTool)
	summarizeTool.SetConnectors(mailConnectors)
	summarizeTool.SetDefaultModel(cfg.LMStudio.ModelDefault)

	// Create notes tool early — needed by both NotesHandler and plugin manager.
	createNoteTool := tools.NewCreateNoteToolWithEmbeddings(db, embeddingService)
	// Auto-classify newly created notes into the tag taxonomy (same as mail).
	createNoteTool.SetIngestor(ingestor)

	// Calendar tool — schema-only on the sidecar side. The actual EventKit
	// write is performed by the macOS app after explicit user confirmation,
	// so this tool merely validates and echoes the structured request that
	// the Swift dispatcher unpacks into a CreateCalendarEventSheet.
	createCalendarEventTool := tools.NewCreateCalendarEventTool()

	// Plugin manager — registers connector adapters and loads saved configs.
	pluginManager := plugin.NewManager(credStore, logger)

	// Distinct per-provider connectors replace the legacy unified "Email"
	// connector. Each pins a single provider (own Info/schema/id) but reuses the
	// same tested sync engine + shared connection objects. Credentials still
	// live under the merged "mail" record, so existing Proton/Gmail auth keeps
	// working with no re-auth. IMAP keeps its own dedicated multi-instance
	// connector (registered below).
	protonConn := mailconnector.New(protonConnector, nil, emailIndexer, summarizeTool, listAttachmentsTool, credStore, logger)
	protonConn.SetPinnedProvider("proton")
	protonConn.SetProtonSession(protonSession)

	gmailConn := mailconnector.New(nil, gmailConnector, emailIndexer, summarizeTool, listAttachmentsTool, credStore, logger)
	gmailConn.SetPinnedProvider("gmail")
	gmailConn.SetGmailSession(gmailSession)

	// Mail.app is intrinsically multi-account: it owns the account registry and
	// the per-account runner used by the WebUI mail views.
	mailappConn := mailconnector.New(nil, nil, emailIndexer, summarizeTool, listAttachmentsTool, credStore, logger)
	mailappConn.SetPinnedProvider("mailapp")

	mailProviders := []*mailconnector.MailConnector{protonConn, gmailConn, mailappConn}
	for _, mc := range mailProviders {
		// Opt-in: prune KB mail deleted/spammed on the server after a full sweep.
		mc.SetReconcileDeletions(cfg.Mail.ReconcileDeletions)
	}

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
		// Only the Mail.app connector holds the multi-account registry; Proton
		// and Gmail sync via their legacy single-source path, so loading the
		// registry here does not double-sync them.
		if n, lErr := mailappConn.LoadAccountsFromCredStore(); lErr != nil {
			logger.Warn().Err(lErr).Msg("loading mail accounts from credential store failed")
		} else if n > 0 {
			logger.Info().Int("accounts", n).Msg("mail accounts registered")
		}
	}

	// Wire the multi-account runner + per-account counts onto the mail handler.
	mailHandler.SetAccountRunner(mailconnector.AsAccountRunner(mailappConn))
	mailHandler.SetAccountCounts(db)

	notesConn := notesconnector.New(createNoteTool, db, embeddingService)
	filesConn := filesconnector.New(ingestor, db)
	imapConn := imapconnector.New(db, embeddingService, nil, logger) // broker wired below after events setup
	caldavConn := caldavconnector.New(db, embeddingService, nil, logger)
	_ = pluginManager.Register(protonConn)
	_ = pluginManager.Register(gmailConn)
	_ = pluginManager.Register(mailappConn)
	_ = pluginManager.Register(notesConn)
	_ = pluginManager.Register(filesConn)
	_ = pluginManager.Register(imapConn)
	_ = pluginManager.Register(caldavConn)

	// Register IMAP factory for multi-instance support.
	// Broker is nil here; dynamic instances get it wired below via SetBroker iteration.
	pluginManager.RegisterFactory("imap", func() plugin.Connector {
		return imapconnector.New(db, embeddingService, nil, logger)
	})

	// Register CalDAV factory for multi-instance support (multi-compte +).
	// Dynamic instances get the broker wired below via the SetBroker iteration.
	pluginManager.RegisterFactory("caldav", func() plugin.Connector {
		return caldavconnector.New(db, embeddingService, nil, logger)
	})

	// Migrate a legacy unified "mail" connector config (written by pre-split
	// builds) to the per-provider keys, so existing installs keep their enabled
	// state + settings instead of starting unconfigured after the upgrade.
	if legacy, ok := cfg.Connectors["mail"]; ok {
		s := legacy.Settings
		if s == nil {
			s = map[string]string{}
		}
		if _, exists := cfg.Connectors["proton"]; !exists && s["username"] != "" {
			cfg.Connectors["proton"] = config.ConnectorSettings{
				Enabled:  legacy.Enabled,
				Schedule: legacy.Schedule,
				Settings: map[string]string{"username": s["username"], "proton_mailbox": s["proton_mailbox"], "limit": s["limit"]},
			}
		}
		if _, exists := cfg.Connectors["gmail"]; !exists && s["gmail_mailbox"] != "" {
			cfg.Connectors["gmail"] = config.ConnectorSettings{
				Enabled:  legacy.Enabled,
				Schedule: legacy.Schedule,
				Settings: map[string]string{"gmail_mailbox": s["gmail_mailbox"], "limit": s["limit"]},
			}
		}
		if _, exists := cfg.Connectors["mailapp"]; !exists {
			cfg.Connectors["mailapp"] = config.ConnectorSettings{
				Enabled:  legacy.Enabled,
				Schedule: legacy.Schedule,
				Settings: map[string]string{"limit": s["limit"], "mailapp_mailbox": s["mailapp_mailbox"]},
			}
		}
		delete(cfg.Connectors, "mail")
		logger.Info().Msg("migrated legacy unified 'mail' connector config to proton/gmail/mailapp")
	}

	// Apply connector configs persisted in config.yaml.
	for id, settings := range cfg.Connectors {
		_ = pluginManager.Configure(id, plugin.ConnectorConfig{
			Enabled:  settings.Enabled,
			Settings: settings.Settings,
			Schedule: settings.Schedule,
		})
	}

	// Provider connectors (proton/gmail/mailapp) are keyed by their own ids, so
	// the Configure loop above + pluginManager.Start below initialise each from
	// its config.Connectors entry — no special-case init needed anymore.

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
	configHandler := handlers.NewConfigHandler(cfg, configPath, logger)
	// Secret config fields (LLM API key) are persisted to the credential store.
	configHandler.SetCredentialStore(credStore)
	// Managed cloud tenant (set on the Hygur Cloud pod): redact + lock the
	// AI-runtime endpoints so the client never sees our upstream Infomaniak ones.
	managedDeployment := strings.EqualFold(os.Getenv("HYGUR_MANAGED"), "true") ||
		os.Getenv("HYGUR_MANAGED") == "1"
	configHandler.SetManaged(managedDeployment)
	// Stripe customer-portal link (managed cloud): the client surfaces it for
	// subscription management, invoices, and cancellation → account deletion.
	configHandler.SetBillingPortalURL(os.Getenv("HYGUR_STRIPE_PORTAL_URL"))
	configHandler.SetInstanceName(os.Getenv("HYGUR_TENANT_ID"))
	// Web Push (browser notifications incl. tab-closed). Disabled when no VAPID
	// keypair is set — the handler 503s and the client skips subscription.
	pushSender := push.NewSender(
		os.Getenv("HYGUR_VAPID_PUBLIC_KEY"),
		os.Getenv("HYGUR_VAPID_PRIVATE_KEY"),
		os.Getenv("HYGUR_VAPID_SUBJECT"),
		logger,
	)
	configHandler.SetVAPIDPublicKey(pushSender.PublicKey())
	pushHandler := handlers.NewPushHandler(db, pushSender, logger)
	// Apply runtime-relevant config changes from PATCH /config without a restart.
	configHandler.SetOnChange(func(c *config.Config) {
		for _, mc := range mailProviders {
			mc.SetReconcileDeletions(c.Mail.ReconcileDeletions)
		}
	})
	marketplaceHandler := handlers.NewMarketplaceHandler(pluginManager, logger)

	// Create notes handler with embedding support for RAG search
	notesHandler := handlers.NewNotesHandler(createNoteTool, logger)
	notesHandler.SetStore(db)
	notesHandler.SetEmbeddingService(embeddingService)

	// Create unified searcher for RAG
	unifiedSearcher := retrieval.NewUnifiedSearcher(db, llmClient)
	unifiedSearcher.SetRetrievalOptions(retrieval.RetrievalOptions{
		UseLLMIntent:            cfg.Retrieval.UseLLMIntent,
		UseJudge:                cfg.Retrieval.UseJudge,
		EntitySearchFallback:    cfg.Retrieval.EntitySearchFallback,
		EntitySearchMinScore:    cfg.Retrieval.EntitySearchMinScore,
		AuthorityRerank:         cfg.Retrieval.AuthorityRerank,
		AttentionRerank:         cfg.Retrieval.AttentionRerank,
		ImminenceRerank:         cfg.Retrieval.ImminenceRerank,
		EntityIndex:             cfg.Retrieval.EntityIndex,
		EntitySynonymy:          cfg.Retrieval.EntitySynonymy,
		EntitySynonymyThreshold: cfg.Retrieval.EntitySynonymyThreshold,
		HebbianExpansion:        cfg.Retrieval.HebbianExpansion,
		SalienceRerank:          cfg.Retrieval.SalienceRerank,
		EntityConsolidation:     cfg.Retrieval.EntityConsolidation,
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

	// Create memory handler. The search tool now needs the LLM client too —
	// Phase 3.3 ranks accepted memories by cosine similarity to the user's
	// last message, so it embeds the query before searching.
	memoryStoreTool := tools.NewMemoryStoreTool(db, llmClient)
	memorySearchTool := tools.NewMemorySearchTool(db, llmClient)
	memoryHandler := handlers.NewMemoryHandler(db, logger)
	memoryHandler.SetTools(memoryStoreTool, memorySearchTool)

	// Persistent memory feeds into the chat handler so durable user facts get
	// injected into the system prompt AND new facts get auto-extracted at the
	// end of every turn.
	ragChatHandler.SetMemoryTools(memoryStoreTool, memorySearchTool)

	// Tool registry: the canonical surface through which the LLM invokes
	// side-effecting actions (create note, …). Add new tools here as they
	// graduate from internal handlers to LLM-callable operations.
	// MustRegister panics on collision because registration is a startup
	// invariant — a duplicate name is a developer bug, not a runtime
	// condition we want to swallow.
	toolRegistry := tools.NewRegistry()
	toolRegistry.MustRegister(createNoteTool)

	// search_knowledge_base lets the LLM trigger RAG on demand instead of
	// running it inconditionally on every turn. This kills the classify +
	// expand + judge LLM round-trips for command-style requests (create
	// note, generate, etc.) where retrieval adds nothing.
	searchKBTool := tools.NewSearchKnowledgeBaseTool(
		unifiedSearcher,
		ragConfig.TopK,
		ragConfig.MinConfidence,
		ragConfig.MaxContextTokens,
	)
	toolRegistry.MustRegister(searchKBTool)
	toolRegistry.MustRegister(createCalendarEventTool)
	toolRegistry.MustRegister(listAttachmentsTool)
	toolRegistry.MustRegister(summarizeTool)
	// Read-only recall tools over the user's own data (no side effects):
	// recall_memory re-queries accepted memories on demand; find_decisions
	// surfaces logged decisions (which are NOT pre-injected into the turn).
	toolRegistry.MustRegister(memorySearchTool)
	toolRegistry.MustRegister(tools.NewFindDecisionsTool(db))
	// lookup_identifier: deterministic (entity, type) → value from the typed-identifier graph
	// + proximity, with a confidence tier the model must voice as-is (never a memorised guess).
	toolRegistry.MustRegister(tools.NewLookupIdentifierTool(db))

	// Web access (opt-in): register web_search + fetch_url only when a search
	// endpoint is configured. Web access means data leaves the machine, so it is
	// off by default. Injection defences: fetch_url is SSRF-guarded; once either
	// tool runs, the chat loop drops side-effecting tools (tainted context).
	if webSearchURL := strings.TrimSpace(os.Getenv("HYGUR_WEB_SEARCH_URL")); webSearchURL != "" {
		toolRegistry.MustRegister(tools.NewWebSearchTool(webSearchURL, os.Getenv("HYGUR_WEB_SEARCH_KEY"), 5))
		toolRegistry.MustRegister(tools.NewFetchURLTool(6000))
		logger.Info().Str("endpoint", webSearchURL).Msg("web tools enabled (web_search + fetch_url)")
	}

	ragChatHandler.SetToolRegistry(toolRegistry)

	// Persistent chat transcripts — the handler saves each turn (user +
	// assistant + cited sources) so conversations can be reopened later.
	ragChatHandler.SetChatStore(db)

	// Per-tenant monthly LLM-token cap (cloud margin guard). Unset/0 = unlimited,
	// the local default; a managed tenant sets it in its StatefulSet env.
	if v := strings.TrimSpace(os.Getenv("HYGUR_CHAT_TOKEN_CAP_MONTHLY")); v != "" {
		if cap, err := strconv.Atoi(v); err == nil && cap > 0 {
			ragChatHandler.SetChatTokenCap(cap)
			logger.Info().Int("cap", cap).Msg("monthly chat-token cap enabled")
		} else {
			logger.Warn().Str("value", v).Msg("ignoring invalid HYGUR_CHAT_TOKEN_CAP_MONTHLY")
		}
	}
	// Per-tenant DAILY cap — the fast fuse (catches a runaway loop within a day).
	if v := strings.TrimSpace(os.Getenv("HYGUR_CHAT_TOKEN_CAP_DAILY")); v != "" {
		if cap, err := strconv.Atoi(v); err == nil && cap > 0 {
			ragChatHandler.SetDailyTokenCap(cap)
			logger.Info().Int("cap", cap).Msg("daily chat-token cap enabled")
		} else {
			logger.Warn().Str("value", v).Msg("ignoring invalid HYGUR_CHAT_TOKEN_CAP_DAILY")
		}
	}

	// Per-tenant fast fuses against a runaway client loop (req/min + concurrent
	// generations), complementing the monthly token cap. Unset/0 = off (local).
	{
		rpm, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("HYGUR_CHAT_RPM_PER_TENANT")))
		conc, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("HYGUR_CHAT_CONCURRENCY_PER_TENANT")))
		if rpm > 0 || conc > 0 {
			ragChatHandler.SetRateLimits(rpm, conc)
			logger.Info().Int("rpm", rpm).Int("concurrency", conc).Msg("chat rate limits enabled")
		}
	}

	// Create API server
	server := api.NewServer(cfg, logger, token)
	server.SetPushHandler(pushHandler)
	// Remote auth mode: verify per-device EdDSA JWTs instead of the loopback
	// static token. Fail fast on a bad key — silently falling back to local
	// would mean serving with weaker auth than the operator asked for.
	if cfg.Auth.Mode == "remote" {
		pub, err := auth.ParseEd25519PublicKeyPEM(cfg.Auth.PublicKey)
		if err != nil {
			logger.Fatal().Err(err).Msg("auth.mode=remote but auth.public_key is invalid")
		}
		revoked := make(map[string]bool, len(cfg.Auth.RevokedJTIs))
		for _, jti := range cfg.Auth.RevokedJTIs {
			revoked[jti] = true
		}
		// Pod-per-tenant: pin to HYGUR_TENANT_ID so a token from another tenant is
		// rejected even with a valid signature (defence in depth vs subdomain routing).
		tenant := strings.TrimSpace(os.Getenv("HYGUR_TENANT_ID"))
		// remote auth implies the multi-tenant cloud, where the pin is mandatory: an
		// empty tenant would silently disable cross-tenant isolation (any validly
		// signed token, for any tenant, would be accepted). Fail closed, loudly.
		if tenant == "" {
			logger.Fatal().Msg("auth.mode=remote requires HYGUR_TENANT_ID: refusing to start without the tenant pin (cross-tenant isolation)")
		}
		server.SetAuthenticator(auth.JWTAuth{PublicKey: pub, Revoked: revoked, Tenant: tenant})
		logger.Info().
			Int("revoked", len(revoked)).
			Str("tenant", tenant).
			Msg("remote auth enabled (per-device JWT)")
	}
	// DNS-rebinding guard: enabled in local mode (loopback-only) or whenever
	// HYGUR_ALLOWED_HOSTS is set (the tenant FQDN in cloud). Off for an
	// unconfigured remote server so existing self-hosted deployments don't break.
	var allowedHosts []string
	for _, h := range strings.Split(os.Getenv("HYGUR_ALLOWED_HOSTS"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			allowedHosts = append(allowedHosts, h)
		}
	}
	server.SetHostGuard(cfg.Auth.Mode == "local" || len(allowedHosts) > 0, allowedHosts)
	server.SetManaged(managedDeployment) // don't ship the loopback token into the cloud SPA
	// Cross-origin web shells allowed to call this API (CORS): the Hygur Cloud web
	// app is served on a separate host (e.g. cloud.hygur.ai) and calls the tenant
	// cross-origin. Loopback + Tauri origins are always allowed regardless.
	var allowedOrigins []string
	for _, o := range strings.Split(os.Getenv("HYGUR_ALLOWED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			allowedOrigins = append(allowedOrigins, o)
		}
	}
	server.SetAllowedOrigins(allowedOrigins)
	// Cloud-backed thin-client mode (desktop "cloud" mode): the local loopback
	// sidecar proxies data/AI routes to the tenant with the device token injected,
	// keeping the webview same-origin so the Tauri commands keep working. Two
	// activation paths — env wins (for tests / headless): HYGUR_CLOUD_UPSTREAM
	// (+ HYGUR_CLOUD_TOKEN), or the desktop single-config file
	// ~/.hygur-edge/config.json with "mode":"cloud", whose Server/Token then back
	// BOTH the proxy and the in-process edge push (one binary + one config does
	// everything). Off on the tenant pod itself (no config file, no env).
	cloudUpstream := strings.TrimSpace(os.Getenv("HYGUR_CLOUD_UPSTREAM"))
	cloudToken := strings.TrimSpace(os.Getenv("HYGUR_CLOUD_TOKEN"))
	edgeCfgPath := edge.DefaultConfigPath()
	edgeCfg, _ := edge.LoadConfig(edgeCfgPath)
	edgeCloud := edgeCfg != nil && edgeCfg.Mode == "cloud"
	if cloudUpstream == "" && edgeCloud {
		cloudUpstream = strings.TrimSpace(edgeCfg.Server)
		cloudToken = strings.TrimSpace(edgeCfg.Token)
	}
	if cloudUpstream != "" {
		if err := server.SetCloudProxy(cloudUpstream, cloudToken); err != nil {
			logger.Fatal().Err(err).Msg("cloud mode requested but upstream invalid")
		}
		logger.Info().Str("upstream", cloudUpstream).Msg("cloud-backed mode: data/AI routes proxied to tenant")
		// One process: push local sources (Files/Proton) to the tenant from the
		// same config instead of a separate `hygur edge` process. The runner also
		// backs the local /edge/* routes (folder listing, status, sync) the WebUI's
		// Proton card uses — wired even before sources are configured so "Load
		// folders" works once credentials are entered.
		runner := edge.NewRunner(edgeCfgPath)
		server.SetEdgeRunner(runner)
		if edgeCloud && (edgeCfg.Folder != "" || edgeCfg.ProtonUser != "") {
			go runner.RunLoop(ctx)
			logger.Info().Msg("edge push loop started in-process")
		}
	}
	// Desktop SPA Content-Security-Policy connect-src sources: the resolved cloud
	// tenant upstream (so cloud mode tightens to the exact tenant host instead of
	// the *.hygur.ai wildcard), the console origin (refresh / logout / passkey /
	// billing — HYGUR_CONSOLE_ORIGIN, default https://console.hygur.ai) and any
	// HYGUR_ALLOWED_ORIGINS (self-host manual-remote from the packaged desktop).
	// 'self' + the Tauri IPC sources are added unconditionally in buildCSP; if none
	// of these resolve it falls back to the wildcard.
	var cspSources []string
	if cloudUpstream != "" {
		cspSources = append(cspSources, cloudUpstream)
	}
	consoleOrigin := strings.TrimSpace(os.Getenv("HYGUR_CONSOLE_ORIGIN"))
	if consoleOrigin == "" {
		consoleOrigin = "https://console.hygur.ai"
	}
	cspSources = append(cspSources, consoleOrigin)
	cspSources = append(cspSources, allowedOrigins...)
	server.SetCSPConnectSources(cspSources)
	server.SetLLMClient(llmClient)
	server.SetKnowledgeHandler(knowledgeHandler)
	server.SetProjectHandler(projectHandler)
	server.SetTaskHandler(taskHandler)
	server.SetMailHandler(mailHandler)
	server.SetNotesHandler(notesHandler)
	server.SetSessionsHandler(handlers.NewSessionsHandler(db, logger))
	server.SetMentionsHandler(handlers.NewMentionsHandler(db, logger))
	server.SetSearchHandler(searchHandler)
	server.SetRAGChatHandler(ragChatHandler)
	server.SetTagHandler(tagHandler)
	server.SetGraphHandler(graphHandler)
	server.SetMemoryHandler(memoryHandler)
	server.SetConnectorHandler(connectorHandler)
	server.SetMarketplaceHandler(marketplaceHandler)
	server.SetConfigHandler(configHandler)
	server.SetUsageHandler(handlers.NewUsageHandler(db, logger))
	server.SetBackupHandler(handlers.NewBackupHandler(db, cfg.Store.Path, dbKey, logger))
	server.SetExportHandler(handlers.NewExportHandler(db, logger))
	server.SetEncryptionHandler(handlers.NewEncryptionHandler(keyStore, dbKeyEnvManaged, logger))

	// Phase 1 (pair mode) — interaction logging + learning gauge.
	interactionLogger := interactions.NewLogger(db)
	learningCalculator := interactions.NewLearningCalculator(db)
	server.SetInteractionsHandler(handlers.NewInteractionsHandler(interactionLogger, logger))
	server.SetInsightsHandler(handlers.NewInsightsHandler(learningCalculator, logger))

	// Timeline handler — consumes Phase 4 entity metadata to build a
	// chaptered chronological view (POST /timeline/query).
	timelineBuilder := retrieval.NewTimelineBuilder(unifiedSearcher, llmClient)
	timelineHandler := handlers.NewTimelineHandler(timelineBuilder, logger)
	server.SetTimelineHandler(timelineHandler)

	// Agenda handler — extracts upcoming deadlines from recent knowledge items
	// and exposes them via GET /agenda/context. Deadline extraction is a factual
	// extraction task (not synthesis), so it runs on the fast indexing model
	// (minicpm5 @ indexing_url) rather than the big inference model.
	agendaExtractor := agenda.NewExtractor(indexingClient)
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

	// Wire the broker into the CalDAV connector so calendar syncs emit events.
	caldavConn.SetBroker(broker)

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
	for _, mc := range mailProviders {
		mc.SetDigestPipeline(broker, db, mailSummarizer)
	}

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
	dailyBrief.SetIndexingClient(indexingClient) // G4 decision-claim extraction on the small model (nil-safe)
	// Web Push: nudge subscribers when the daily brief is ready (works tab-closed).
	// Best-effort; prunes endpoints the push service reports gone.
	if pushSender.Configured() {
		dailyBrief.SetBriefNotifier(func(c context.Context, title, body string) {
			subs, err := db.ListPushSubscriptions(c)
			if err != nil || len(subs) == 0 {
				return
			}
			ps := make([]push.Subscription, len(subs))
			for i, s := range subs {
				ps[i] = push.Subscription{Endpoint: s.Endpoint, P256dh: s.P256dh, Auth: s.Auth}
			}
			for _, ep := range pushSender.Send(c, ps, push.Notification{
				Title: title, Body: body, Icon: "/icon-192.png",
				Data: map[string]any{"url": "/digest"},
			}) {
				_ = db.DeletePushSubscription(c, ep)
			}
		})
	}
	// P-2 imminence boost: feed the prospection scan (items tied to a soon-due
	// recurring obligation) to retrieval, cached with a TTL inside the searcher.
	unifiedSearcher.SetImminentIDsFunc(func(c context.Context) map[string]struct{} {
		return dailyBrief.ImminentContentIDs(c, 14)
	})
	if cfg.DailyBrief.Enabled {
		dailyBrief.Start(ctx)
	}
	briefHandler := handlers.NewBriefHandler(dailyBrief, logger)
	server.SetBriefHandler(briefHandler)

	// W4: project-suggestion backfill — runs once in the background at boot
	// (idempotent: skips items already linked or already classified), so a
	// freshly-provisioned tenant gets project suggestions without manual action.
	// Classification is parallelized inside SuggestProjects.
	go func() {
		bctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		select {
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Second): // let the server come up first
		}
		if n, err := ingestor.SuggestProjects(bctx); err != nil {
			logger.Warn().Err(err).Msg("project-suggestion backfill failed")
		} else if n > 0 {
			logger.Info().Int("processed", n).Msg("project-suggestion backfill complete")
		}
	}()

	// One-shot Tier-1 re-extraction (regex only, no LLM) gated by env: refreshes
	// the extracted_* metadata from the current rules — e.g. to purge
	// reference-number false-positive amounts ("365138779 EUR") after the
	// extractor fix. Authoritative merge deletes values the extractor no longer
	// produces. Idempotent (a stable re-extract just re-confirms and skips), so
	// it's safe to leave enabled; unset HYGUR_REINDEX_TIER1 to skip.
	if os.Getenv("HYGUR_REINDEX_TIER1") == "1" {
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
			rctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			if stats, err := mail.ReindexEntitiesTier1(rctx, db, logger, 200); err != nil {
				logger.Warn().Err(err).Msg("tier1 reindex failed")
			} else {
				logger.Info().
					Int("total", stats.Total).
					Int("updated", stats.Updated).
					Int("skipped", stats.Skipped).
					Msg("tier1 reindex complete")
			}
		}()
	}

	// One-shot token-usage reset gated by env: clears the running daily totals
	// (pricing settings untouched). Set HYGUR_RESET_TOKEN_USAGE=1, restart, then
	// unset. A quick DELETE, so it runs synchronously here.
	if os.Getenv("HYGUR_RESET_TOKEN_USAGE") == "1" {
		if n, err := db.ResetTokenUsage(ctx); err != nil {
			logger.Warn().Err(err).Msg("token usage reset failed")
		} else {
			logger.Info().Int64("rows_deleted", n).Msg("token usage reset")
		}
	}

	// Meeting briefings — short RAG briefings ahead of events/deadlines, emitted
	// server-side by the scheduler: upcoming calendar events (source_type=event)
	// and mail-extracted deadlines due today. Each emits a `meeting_briefing`
	// event (→ notification) + persists a `meeting_brief` item for the Briefings
	// view. (The old per-event POST /brief/meeting native path is retired.)
	meetingBriefer := scheduler.NewMeetingBriefer(db, llmClient, unifiedSearcher, broker, logger)
	briefHandler.SetStore(db)
	scheduler.NewMeetingBriefScheduler(meetingBriefer, db, agendaExtractor, 8, logger).Start(ctx)

	// Chronicle (v1) — Hygur writes one grounded narrative act per night into the
	// always-open "life" chapter (continuity via synopsis + watermark). A manual
	// trigger (POST /chronicle/run) regenerates today's act for testing.
	chronicleWriter := scheduler.NewChronicleWriter(db, llmClient, logger)
	server.SetChronicleHandler(handlers.NewChronicleHandler(db, chronicleWriter, logger))
	scheduler.NewChronicleScheduler(chronicleWriter, 22, logger).Start(ctx)

	// Decisions — Hygur proposes the decisions it detects in the user's own
	// records (nightly scan at 23:00 + a manual "Scan now"), for the user to
	// confirm, making decisions first-class objects. Manual logging works even
	// without the scanner; only the grounded scan needs the LLM.
	decisionScanner := scheduler.NewDecisionScanner(db, llmClient, logger)
	decisionHandler := handlers.NewDecisionHandler(db, decisionScanner, logger)
	decisionHandler.SetEmbeddingService(embeddingService)
	server.SetDecisionHandler(decisionHandler)
	scheduler.NewDecisionScheduler(decisionScanner, 23, logger).Start(ctx)

	// Consolidation ("Quand Hygur rêve", DREAM_PLAN Phase 1). The manual trigger
	// (POST /consolidation/run) works any time — it scores + writes item_signals and
	// evicts nothing (shadow). The nightly auto-pass at 00:00 is opt-in on the home
	// canary via HYGUR_DREAM_SHADOW=1, until the scoring is calibrated on real data.
	consolidator := scheduler.NewConsolidator(db, logger)
	server.SetConsolidationHandler(handlers.NewConsolidationHandler(db, consolidator, logger))
	server.SetEngramHandler(handlers.NewEngramHandler(db, cfg.Identity.OwnerNames, logger))
	if os.Getenv("HYGUR_DREAM_SHADOW") == "1" {
		scheduler.NewConsolidationScheduler(consolidator, 0, logger).Start(ctx)
	}

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

// tokenUsageSink adapts the store to llm.UsageRecorder. RecordUsage runs on
// the request goroutine at completion time, so it must not block; it uses a
// background context (the request ctx is often cancelled the instant the
// stream ends) and swallows write errors after logging.
type tokenUsageSink struct {
	db     *store.DB
	logger zerolog.Logger
}

func (s tokenUsageSink) RecordUsage(category string, tokensIn, tokensOut int) {
	if err := s.db.RecordTokenUsage(context.Background(), category, tokensIn, tokensOut); err != nil {
		s.logger.Warn().Err(err).Str("category", category).Msg("record token usage failed")
	}
}

// watchParent polls the parent process ID every 2 seconds and triggers
// shutdown via `stop` (the cancel from signal.NotifyContext) the first time
// it changes. If we were started detached (PPID already 1, e.g. via
// launchd / nohup), the watchdog is disabled — there's no parent to die.
func watchParent(ctx context.Context, stop func(), logger zerolog.Logger) {
	initialPPID := os.Getppid()
	if initialPPID <= 1 {
		return
	}
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current := os.Getppid()
				if current != initialPPID {
					logger.Info().
						Int("initial_parent_pid", initialPPID).
						Int("current_parent_pid", current).
						Msg("parent process exited — initiating shutdown")
					stop()
					return
				}
			}
		}
	}()
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
	// Top-level files to migrate (src name → dst name).
	fileMappings := [][2]string{
		{"hygur.db", "hygur.db"},
		{"config.yaml", "config.yaml"},
		// Canonicalise the token file name: old installs used ".hygur-token";
		// the new canonical name is "token" (matches macOS App Support convention).
		{".hygur-token", "token"},
		{"token", "token"},
		{".cred_key", ".cred_key"},
	}
	for _, m := range fileMappings {
		src := filepath.Join(oldDir, m[0])
		dst := filepath.Join(newDir, m[1])
		if _, err := os.Stat(src); err != nil {
			continue // src absent — skip
		}
		if _, err := os.Stat(dst); err == nil {
			continue // dst already exists — skip
		}
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copy %s: %w", m[0], err)
		}
		logger.Debug().Str("src", m[0]).Str("dst", m[1]).Msg("migrated data file")
	}

	// Migrate credentials directory (encrypted connector secrets + cred.key).
	oldCreds := filepath.Join(oldDir, "credentials")
	newCreds := filepath.Join(newDir, "credentials")
	if err := os.MkdirAll(newCreds, 0o700); err != nil {
		return fmt.Errorf("mkdir credentials: %w", err)
	}
	entries, err := os.ReadDir(oldCreds)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read old credentials dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		src := filepath.Join(oldCreds, entry.Name())
		dst := filepath.Join(newCreds, entry.Name())
		if _, err := os.Stat(dst); err == nil {
			continue // already migrated
		}
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copy credential %s: %w", entry.Name(), err)
		}
		logger.Debug().Str("file", entry.Name()).Msg("migrated credential file")
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
