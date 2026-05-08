package plugin

import "time"

// ConnectorInfo métadonnées statiques (lues avant Init).
type ConnectorInfo struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Version       string   `json:"version"`
	Icon          string   `json:"icon"`
	Color         string   `json:"color"`
	Tags          []string `json:"tags"`
	MultiInstance bool     `json:"multi_instance,omitempty"` // true → plusieurs instances autorisées
}

// Capabilities opérations supportées (bitfield logique via champs bool).
type Capabilities struct {
	CanList      bool     `json:"can_list"`
	CanSearch    bool     `json:"can_search"`
	CanSync      bool     `json:"can_sync"`
	CanIndex     bool     `json:"can_index"`
	CanSummarize bool     `json:"can_summarize"`
	CanAttach    bool     `json:"can_attach"`
	NeedsAuth    bool     `json:"needs_auth"`
	AuthType     AuthType `json:"auth_type"`
}

// AuthType représente le type d'authentification requis par un connecteur.
type AuthType string

const (
	AuthNone     AuthType = "none"
	AuthPassword AuthType = "password"
	AuthOAuth2   AuthType = "oauth2"
	AuthAPIKey   AuthType = "api_key"
	AuthToken    AuthType = "token"
)

// ConnectorConfig configuration persistée par le Manager dans config.yaml.
type ConnectorConfig struct {
	Enabled  bool              `yaml:"enabled"            json:"enabled"`
	Settings map[string]string `yaml:"settings,omitempty" json:"settings,omitempty"`
	Schedule string            `yaml:"schedule,omitempty" json:"schedule"`
}

// ConfigSchema schéma pour génération dynamique du formulaire de config.
type ConfigSchema struct {
	Groups []ConfigGroup `json:"groups"`
}

// ConfigGroup regroupe des champs de configuration sous un titre.
type ConfigGroup struct {
	Title  string        `json:"title"`
	Fields []ConfigField `json:"fields"`
}

// ConfigField décrit un champ individuel du formulaire de configuration.
type ConfigField struct {
	Key         string           `json:"key"`
	Type        FieldType        `json:"type"`
	Label       string           `json:"label"`
	Description string           `json:"description"`
	Required    bool             `json:"required"`
	Default     string           `json:"default"`
	Options     []ConfigOption   `json:"options"`
	Condition   *ConfigCondition `json:"condition"`
}

// ConfigOption représente une option dans un champ de type FieldEnum.
type ConfigOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Icon  string `json:"icon,omitempty"`
}

// ConfigCondition définit la condition d'affichage d'un champ.
type ConfigCondition struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// FieldType représente le type d'un champ de configuration.
type FieldType string

const (
	FieldString    FieldType = "string"
	FieldInt       FieldType = "int"
	FieldBool      FieldType = "bool"
	FieldEnum      FieldType = "enum"
	FieldSecret    FieldType = "secret"     // stocké dans CredentialStore, jamais en clair
	FieldOAuth     FieldType = "oauth"      // déclenche AuthURL() + ExchangeCode()
	FieldPath      FieldType = "path"       // sélecteur de fichier/dossier côté macOS
	FieldCron      FieldType = "cron"       // éditeur de planification
	FieldMultiEnum FieldType = "multi_enum" // cases à cocher
	// FieldPermissionCheck rends côté macOS un bloc info + bouton qui ouvre
	// un panneau de Réglages système. Aucune valeur n'est stockée.
	// La cible (URL `x-apple.systempreferences:...`) est passée via le champ
	// Description ; le label du bouton via la Default value.
	FieldPermissionCheck FieldType = "permission_check"
)

// Item unité de données universelle entre connecteurs et Knowledge Base.
type Item struct {
	ID          string // ID natif dans la source (opaque pour le core)
	ConnectorID string // ID du connecteur source, ex: "mail.proton"
	SourceType  string // type sémantique: "mail", "note", "file", "event"
	Title       string
	Content     string         // texte plat normalisé pour indexation
	RawContent  string         // format original (HTML, markdown, etc.)
	Metadata    map[string]any // champs spécifiques à la source
	Author      string
	URL         string // deep link si applicable (ex: mail://...)
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Tags        []string
	Attachments []AttachmentRef
}

// AttachmentRef référence légère vers une pièce jointe.
type AttachmentRef struct {
	ID       string
	Name     string
	MimeType string
	Size     int64 // bytes
}

// Attachment pièce jointe avec données de téléchargement.
type Attachment struct {
	AttachmentRef
	ConnectorID string
	ItemID      string
}

// HealthStatus état de santé instantané (pas de IO).
type HealthStatus struct {
	Status    Status    `json:"status"`
	Message   string    `json:"message"`
	LastSync  time.Time `json:"last_sync"`
	ItemCount int64     `json:"item_count"`
	ErrCount  int64     `json:"error_count"`
	LastError string    `json:"last_error"`
}

// Status représente l'état de santé d'un connecteur.
type Status string

const (
	StatusHealthy      Status = "healthy"
	StatusDegraded     Status = "degraded"     // connecté mais erreurs partielles
	StatusUnhealthy    Status = "unhealthy"    // déconnecté ou erreur critique
	StatusConnecting   Status = "connecting"   // init en cours
	StatusUnconfigured Status = "unconfigured" // credentials manquants
)

// ListOptions options de pagination et filtrage pour List().
type ListOptions struct {
	Offset int
	Limit  int    // 0 = pas de limite
	Query  string // filtre optionnel
	Since  time.Time
}

// SearchOptions options pour Search().
type SearchOptions struct {
	Limit int
}

// SyncOptions paramètres pour Sync().
type SyncOptions struct {
	Mailbox   string   `json:"mailbox,omitempty"`    // pour mail : "INBOX", "Sent", etc. "" = tout
	Limit     int      `json:"limit,omitempty"`      // nombre max d'items à synchroniser
	Full      bool     `json:"full,omitempty"`       // resync complète (ignore last_sync watermark)
	AccountID string   `json:"account_id,omitempty"` // pour mail multi-compte : cible un compte précis
	Labels    []string `json:"labels,omitempty"`     // mail: explicit label IDs to sync
}

// SyncResult résultat d'une synchronisation.
type SyncResult struct {
	Processed int           `json:"processed"`
	Skipped   int           `json:"skipped"`
	Failed    int           `json:"failed"`
	Duration  time.Duration `json:"duration_ms"`
}

// IndexResult résultat d'un IndexBatch().
type IndexResult struct {
	Indexed int
	Failed  int
	Errors  []IndexError
}

// IndexError erreur d'indexation unitaire.
type IndexError struct {
	ItemID  string
	Message string
}

// Summary résultat de Summarize().
type Summary struct {
	Text      string
	ModelID   string
	CreatedAt time.Time
}
