package plugin

import (
	"context"
	"io"
)

// Connector est l'interface minimale que tout connecteur doit implémenter.
type Connector interface {
	// Info retourne les métadonnées statiques du connecteur (lues sans init).
	Info() ConnectorInfo

	// Capabilities retourne les opérations supportées.
	Capabilities() Capabilities

	// ConfigSchema retourne le schéma de configuration pour génération de l'UI.
	ConfigSchema() ConfigSchema

	// Init initialise le connecteur avec sa configuration.
	// Appelé par le Manager après Configure(), avant Start().
	Init(ctx context.Context, cfg ConnectorConfig) error

	// Start démarre le connecteur (connexions, goroutines de fond).
	// Le ctx passé dure toute la vie du Manager.
	Start(ctx context.Context) error

	// Stop arrête proprement le connecteur. Timeout recommandé : 5s.
	Stop(ctx context.Context) error

	// Health retourne l'état actuel de santé sans IO bloquant.
	Health() HealthStatus
}

// Lister peut énumérer ses éléments.
type Lister interface {
	Connector
	List(ctx context.Context, opts ListOptions) ([]Item, error)
}

// Searcher peut effectuer une recherche plein texte dans sa source.
type Searcher interface {
	Connector
	Search(ctx context.Context, query string, opts SearchOptions) ([]Item, error)
}

// Syncer supporte la synchronisation en arrière-plan.
// Le Scheduler du Manager appelle Sync() selon ConnectorConfig.Schedule.
type Syncer interface {
	Connector
	Sync(ctx context.Context, opts SyncOptions) (*SyncResult, error)
}

// Indexer peut indexer du contenu dans la Knowledge Base (store SQLite).
type Indexer interface {
	Connector
	// Index indexe un item par son ID natif dans la source.
	Index(ctx context.Context, itemID string) error
	// IndexBatch indexe plusieurs items ; continue en cas d'erreur partielle.
	IndexBatch(ctx context.Context, itemIDs []string) (*IndexResult, error)
}

// Summarizer délègue la résumé à l'IA locale.
type Summarizer interface {
	Connector
	Summarize(ctx context.Context, itemID string, modelID string) (*Summary, error)
}

// Attacher expose les pièces jointes d'un item.
type Attacher interface {
	Connector
	ListAttachments(ctx context.Context, itemID string) ([]Attachment, error)
	// DownloadAttachment retourne un ReadCloser ; l'appelant doit le fermer.
	DownloadAttachment(ctx context.Context, attachmentID string) (io.ReadCloser, Attachment, error)
}

// Authenticator supporte un flux d'authentification interactif (OAuth, etc.).
type Authenticator interface {
	Connector
	// AuthURL génère l'URL d'autorisation. Pour OAuth2, retourne l'URL Google/etc.
	// Pour password, retourne "".
	AuthURL(ctx context.Context) (string, error)
	// ExchangeCode échange un code ou termine le flow (OAuth callback).
	ExchangeCode(ctx context.Context, code string) error
	// IsAuthenticated retourne true si le connecteur possède des credentials valides.
	IsAuthenticated() bool
}

// SecretFieldProvider is an optional interface for connectors that have secret
// configuration fields. The returned keys are stripped from settings before
// they are persisted to config.yaml.
type SecretFieldProvider interface {
	SecretFieldKeys() []string
}

// DefaultEnabledProvider is an optional interface for connectors that should be
// enabled out of the box (no setup required). The Manager uses this only when
// no prior configuration exists for the connector — once a user explicitly
// disables a connector, that choice is respected on subsequent launches.
type DefaultEnabledProvider interface {
	EnabledByDefault() bool
}
