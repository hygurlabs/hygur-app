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
  | {
      type: "audio";
      data: string;
      format: string;
      title?: string;
      /** History-loaded audio whose bytes were purged by the retention cap:
       *  false ⇒ show a "recording no longer available" placeholder. Undefined
       *  for freshly-sent audio (data is present). */
      available?: boolean;
    };

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

/** One cited side of a contradiction (W5): the divergent value + its source. */
export interface ConflictMember {
  content_id: string;
  title: string;
  from: string;
  date: string; // RFC3339
  value: string;
}

/** A deterministic contradiction within a thread (GET /knowledge/contradictions). */
export interface Conflict {
  type: "amount" | "due_date" | "iban" | "vat" | "structured_comm" | string;
  cluster: string; // normalized thread subject
  severity: "high" | "medium" | string;
  members: ConflictMember[];
}

/** A validated citation back to a real knowledge item (Follow-up digest). */
export interface DigestSource {
  content_id: string;
  title: string;
  from: string;
  date: string; // RFC3339
}

/** One topic or contradiction in the Follow-up digest; title is topics-only. */
export interface DigestEntry {
  title?: string;
  note: string;
  sources: DigestSource[];
}

/** A local to-do (GET/POST /tasks). */
export interface Task {
  id: string;
  title: string;
  body: string; // Markdown — a task is a note-like knowledge_item
  status: string; // "open" | "done"
  due_date?: string;
  project_id?: string;
  tags: Tag[];
  created_at: string;
  updated_at: string;
}

/** A decision/commitment as a first-class object: logged by the user or proposed
 *  by the nightly grounded scan, then confirmed. A note-like knowledge_item
 *  (statement + Markdown rationale + tags + project) plus decision state. */
export interface Decision {
  id: string;
  statement: string;
  rationale: string; // Markdown
  status: string; // "proposed" | "standing" | "superseded"
  decided_on?: string; // RFC3339
  source_refs: string[]; // knowledge_item ids that ground it
  project_id?: string;
  tags: Tag[];
  created_at: string;
  updated_at: string;
  // Angle A-2a — set when this decision updates an earlier one (same matter decided
  // again with a divergent value). updates_statement is the predecessor's statement.
  updates_decision_id?: string;
  updates_statement?: string;
}

/** The daily composed "state of your world" (GET /digest) — assembles already-
 *  computed signals: where things stand, open contradictions, decisions to
 *  confirm, tasks due soon. */
/** One "Coming up" entry (Conséquence/prospection): a recurring obligation's next
 *  occurrence, or a standing decision's future-dated horizon. */
export interface Upcoming {
  kind: string; // "recurrence" | "decision"
  title: string;
  at: string; // RFC3339 — the upcoming date
  detail: string; // "every 31d" | "decision"
}

export interface Digest {
  synopsis: string;
  positions?: string; // Angle A-2b — grounded summary of the user's standing positions
  contradictions: ReconciledConflict[];
  proposed_decisions: Decision[];
  due_tasks: Task[];
  upcoming?: Upcoming[];
}

/** One cited side of a reconciled claim conflict (W6 REDUCE). */
export interface ClaimRef {
  source_id: string;
  value: string;
  quote: string;
  asserted_at: string;
}

/** A reconciled semantic contradiction (GET /knowledge/claim-contradictions):
 *  cross-source divergent claims the LLM classified as a real `conflict` or an
 *  evolution (`supersedes`), each cited verbatim. */
export interface ReconciledConflict {
  cluster: string;
  entity: string;
  attribute: string;
  members: ClaimRef[];
  verdict: { kind: "conflict" | "supersedes" | string; reason: string };
  /** Stable identity used to dismiss/restore this contradiction. */
  key: string;
  /** True when the user has dismissed it (only surfaced with include_dismissed). */
  dismissed?: boolean;
}

/** One row of a project's exchange timeline (GET /knowledge/project-timeline). */
export interface TimelineItem {
  content_id: string;
  title: string;
  source_type: string;
  from: string;
  date: string; // RFC3339
}

/** A long-term memory Hygur keeps about the user (fact / action / preference). */
export interface Memory {
  memory_id: string;
  type: string; // "fact" | "action" | "preference"
  content: string;
  created_at: string;
  source?: string; // "manual" | "extracted"
  accepted_at?: string; // "" = pending review
}

/** Learning gauge — "how well Hygur knows you" (GET /insights/learning-progress). */
export interface LearningPillar {
  key: string;
  label: string;
  progress: number; // [0,1]
  current: number;
  target: number;
  weight: number;
}
export interface LearningProgress {
  coverage: number; // [0,1]
  next_step: string;
  next_step_hint: string;
  pillars: LearningPillar[];
}

/** Chronicle — Hygur's grounded narrative, read as a book. */
export interface ChronicleChapterSummary {
  id: string;
  title: string;
  status: string;
  act_count: number;
  last_date?: string;
}
export interface ChronicleAct {
  date: string; // YYYY-MM-DD
  title: string; // e.g. "12 June 2026"
  markdown: string;
  sources: string[]; // content_ids; index n-1 ↔ "[n]" anchor in the prose
  closing?: boolean; // the act that closed the chapter
}
export interface ChronicleChapterDetail {
  id: string;
  title: string;
  status: string;
  acts: ChronicleAct[];
}

/** An open task with a deadline, surfaced proactively in Follow-up. */
export interface DueTask {
  id: string;
  title: string;
  due_date: string;
  status: string;
}

/** Grounded Follow-up synthesis (GET /knowledge/followup). */
export interface FollowUpDigest {
  topics: DigestEntry[];
  contradictions: DigestEntry[];
  due_tasks?: DueTask[];
  scanned: number;
  window: string;
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

/** One item carrying a tag (GET /tags/{id}/items). */
export interface TagItem {
  id: string;
  title: string;
  source_type: string;
  source_path?: string;
  created_at: string;
  updated_at: string;
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

/** A media attachment returned with a persisted user turn (GET /sessions/{id}).
 *  `data` is base64; absent when `available` is false (audio purged by the cap). */
export interface SessionAttachment {
  type: "image" | "audio";
  title?: string;
  mime_type?: string;
  format?: string;
  data?: string;
  available: boolean;
  byte_size?: number;
}

export interface SessionMessage {
  id: string;
  role: "user" | "assistant";
  content: string;
  sources?: RagSource[];
  attachments?: SessionAttachment[];
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
  /** Hygur-operated cloud tenant: the AI-runtime endpoints are operator-controlled
   *  (redacted server-side). The UI hides the AI-runtime editor when true. */
  managed?: boolean;
  /** Stripe customer-portal link (managed cloud): manage subscription, view
   *  invoices, cancel → account deletion. Surfaced in Settings when present. */
  billing_portal_url?: string;
  /** The tenant's friendly slug (= URL + namespace); used for the type-to-confirm
   *  deletion gate and to show the user which space they're in. */
  instance_name?: string;
  /** Web Push VAPID public key; present = push enabled (the client subscribes). */
  vapid_public_key?: string;
}

/** Per-1M-token prices for the cost estimate (GET/PUT /usage). Chat is billed
 *  per direction; embeddings + indexing share `ingest_per_1m`. */
export interface TokenPricing {
  chat_in_per_1m: number;
  chat_out_per_1m: number;
  ingest_per_1m: number;
  currency: string;
}

/** Token totals for one period. Chat keeps IN/OUT split; embeddings/indexing
 *  are reported as total tokens each; total_in/total_out sum every category
 *  (the input/output budget the inference box sees — drives the usage gauge). */
export interface TokenPeriodUsage {
  chat_in: number;
  chat_out: number;
  embedding: number;
  indexing: number;
  total_in: number;
  total_out: number;
}

/** Response of GET /usage/tokens. */
export interface TokenUsageResponse {
  currency: string;
  pricing: TokenPricing;
  periods: {
    today: TokenPeriodUsage;
    this_week: TokenPeriodUsage;
    this_month: TokenPeriodUsage;
  };
}

/** Local at-rest encryption status (GET /admin/db/encryption). env_managed =
 *  the key comes from the server env (cloud); not user-toggleable locally. */
export interface EncryptionStatus {
  enabled: boolean;
  env_managed: boolean;
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
