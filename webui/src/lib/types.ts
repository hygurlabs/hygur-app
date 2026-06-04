// Wire shapes mirrored from the Go sidecar handlers. Kept deliberately loose
// (optional fields, index signatures on metadata) so a server tweak doesn't
// break the build — the UI degrades gracefully on missing fields.

export type Role = "user" | "assistant" | "system";

/** An attachment on a chat message. Mirrors the sidecar's llm.Attachment.
 *  - "document": a KB reference (📎 upload of a doc / @-mention). The sidecar
 *    resolves it to inline text before the LLM sees it.
 *  - "image" / "audio": live media sent to the multimodal model directly
 *    (base64), so Gemma can see/hear it (e.g. transcribe an audio, read a photo). */
export type AttachmentRef =
  | { type: "document"; content_id: string; title?: string }
  | { type: "image"; data: string; mime_type: string; title?: string }
  | { type: "audio"; data: string; format: string; title?: string };

export interface ChatMessage {
  role: Role;
  content: string;
  attachments?: AttachmentRef[];
}

/** Restricts retrieval to projects/tags. @-mentioning a project sets this. */
export interface FocusScope {
  project_ids?: string[];
  tag_ids?: string[];
}

/** A source surfaced by RAG chat (`rag_context` SSE event). */
export interface RagSource {
  content_id: string;
  source_type: string;
  title: string;
  excerpt: string;
  score?: number;
  mail_from?: string;
  mail_date?: string;
  mail_subject?: string;
}

/** A row of `POST /search`. */
export interface SearchResult {
  chunk_id: string;
  content_id: string;
  source_type: string;
  score: number;
  excerpt: string;
  title: string;
  date?: string;
  metadata?: Record<string, unknown>;
  mail_from?: string;
  mail_date?: string;
  mail_subject?: string;
}

export interface SearchStats {
  total_results: number;
  knowledge_results: number;
  mail_results: number;
  search_duration_ms: number;
}

export interface SearchResponse {
  results: SearchResult[];
  search_stats: SearchStats;
}

export interface KnowledgeItem {
  content_id: string;
  source_type: string;
  source_path?: string | null;
  title: string;
  normalized_text: string;
  metadata?: Record<string, unknown> | null;
  // GET /knowledge/items also returns a normalized `date` (mail/file/event date).
  date?: string;
  // GET /knowledge/{id} additionally returns the item's project + tags.
  project_id?: string | null;
  tags?: { id: string; name: string; color?: string }[];
}

export interface NoteTag {
  id: string;
  name: string;
  color?: string;
}

export interface Note {
  id: string;
  title: string;
  content: string;
  project_id?: string | null;
  tags?: NoteTag[];
  created_at?: string;
  updated_at?: string;
}

export interface Tag {
  id: string;
  name: string;
  color?: string;
  auto_rule?: string;
  is_auto?: boolean;
  usage_count?: number;
}

export interface AgendaAction {
  what: string;
  deadline_iso: string;
  priority: string;
  source_id: string;
  confidence: number;
}

/** A persisted conversation (GET /sessions). */
export interface SessionSummary {
  id: string;
  title: string;
  project_id?: string;
  message_count: number;
  last_message?: string;
  created_at: string;
  updated_at: string;
}

export interface SessionMessage {
  id: string;
  role: "user" | "assistant";
  content: string;
  sources?: RagSource[];
  created_at: string;
}

export interface SessionDetail {
  id: string;
  title: string;
  project_id?: string;
  created_at: string;
  updated_at: string;
  messages: SessionMessage[];
}

/** A project (GET /projects). Note the API key is `id`, not `project_id`. */
export interface Project {
  id: string;
  name: string;
  description?: string;
  tags?: string[];
  item_count: number;
  archived: boolean;
  created_at: string;
  updated_at: string;
}

export interface ProjectItem {
  id: string;
  title: string;
  source_type: string;
  source_path?: string;
  created_at: string;
  updated_at: string;
}

/** An @-mention autocomplete entry (GET /mentions). */
export interface Mention {
  id: string;
  type: "project" | "note" | "mail" | "document" | "tag";
  title: string;
}

/** A stored briefing — daily or meeting (GET /briefings). */
export interface Briefing {
  content_id: string;
  title: string;
  kind: string; // "brief" | "meeting_brief"
  content: string;
  when?: string;
  created_at: string;
}

export interface ConnectorHealth {
  status?: string; // "ok" | "degraded" | "down" | "unknown"
  message?: string;
  last_sync?: string;
  item_count?: number;
  error_count?: number;
  last_error?: string;
}

export interface ConnectorInfo {
  id: string;
  name: string;
  description?: string;
  version?: string;
  icon?: string;
  color?: string;
  tags?: string[];
  multi_instance?: boolean;
}

/** A configured connector (GET /connectors). */
export interface Connector {
  info: ConnectorInfo;
  enabled: boolean;
  health: ConnectorHealth;
}

/** Tunable sidecar configuration (GET /config). */
export interface SidecarConfig {
  lm_studio: {
    url: string;
    embedding_url: string;
    indexing_url: string;
    model_default: string;
    model_indexing: string;
    embedding_model: string;
    embedding_max_tokens: number;
    embedding_batch_size: number;
    timeout_seconds: number;
    max_retries: number;
    /** GET only: whether a provider API key is stored. The value is never returned. */
    api_key_set: boolean;
    /** PATCH only: set the provider API key ("" clears it). Stored encrypted, never in config.yaml. */
    api_key?: string;
  };
  logging: { level: string };
  daily_brief: {
    enabled: boolean;
    hour_local: string;
    max_items: number;
    lookback_hours: number;
  };
  retrieval: {
    use_llm_intent: boolean;
    use_judge: boolean;
    temporal_scoring_mode: string;
    entity_search_fallback: boolean;
    entity_search_min_score: number;
  };
  mail: { reconcile_deletions: boolean };
}

export interface SidecarConfigPatch {
  lm_studio?: Partial<SidecarConfig["lm_studio"]>;
  logging?: Partial<SidecarConfig["logging"]>;
  daily_brief?: Partial<SidecarConfig["daily_brief"]>;
  retrieval?: Partial<SidecarConfig["retrieval"]>;
  mail?: Partial<SidecarConfig["mail"]>;
}

export type FieldType =
  | "string"
  | "int"
  | "bool"
  | "enum"
  | "secret"
  | "oauth"
  | "path"
  | "cron"
  | "multi_enum"
  | "permission_check";

export interface ConfigOption {
  value: string;
  label: string;
  icon?: string;
}
export interface ConfigCondition {
  field: string;
  value: string;
}
export interface ConfigField {
  key: string;
  type: FieldType;
  label: string;
  description: string;
  required: boolean;
  default: string;
  options: ConfigOption[] | null;
  condition: ConfigCondition | null;
}
export interface ConfigGroup {
  title: string;
  fields: ConfigField[];
}
export interface ConfigSchema {
  groups: ConfigGroup[];
}

export interface ConnectorConfigValue {
  enabled: boolean;
  settings?: Record<string, string>;
  schedule?: string;
}

/** Full connector detail (GET /connectors/{id}) — drives the config form. */
export interface ConnectorDetail {
  info: ConnectorInfo;
  capabilities: {
    can_sync?: boolean;
    needs_auth?: boolean;
    auth_type?: string;
  };
  config_schema: ConfigSchema;
  config: ConnectorConfigValue;
  health: ConnectorHealth;
}

/** A dynamic connector instance (GET /connectors/instances). */
export interface ConnectorInstance {
  instance_id: string;
  type_id: string;
  display_name: string;
  info: ConnectorInfo;
  enabled: boolean;
  health: ConnectorHealth;
}

/** A marketplace catalog entry (GET /marketplace/connectors). */
export interface MarketplaceItem {
  id: string;
  type: string;
  display_name: string;
  description?: string;
  author?: string;
  categories?: string[];
  capabilities?: string[];
  is_built_in?: boolean;
  is_installed?: boolean;
  verified?: boolean;
  multi_instance?: boolean;
}
