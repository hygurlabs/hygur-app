// Package store provides SQLite database access for the Hygur knowledge base.
package store

import (
	"database/sql"
	"fmt"
)

// Migration represents a database migration.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// migrations contains all available migrations in order.
var migrations = []Migration{
	{
		Version: 1,
		Name:    "initial_schema",
		SQL:     schemaSQL,
	},
	// Migration 2 originally created FTS5 tables; dropped by migration 5. No-op on fresh DBs.
	{
		Version: 2,
		Name:    "fts5_and_vectors",
		SQL:     `SELECT 1;`,
	},
	// Migration 3 is now a no-op because the 'tags' column was added to the initial schema.
	// We keep the migration entry for version tracking but with an empty SQL statement.
	{
		Version: 3,
		Name:    "add_project_tags",
		SQL:     `SELECT 1;`, // No-op: tags column now exists in initial schema
	},
	// Migration 4 adds the tags and item_tags tables for tag management.
	{
		Version: 4,
		Name:    "add_tags_tables",
		SQL: `
-- tags stores user-defined and auto-generated tags
CREATE TABLE IF NOT EXISTS tags (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE,
    color TEXT NOT NULL DEFAULT '#3B82F6',
    auto_rule TEXT,
    is_auto BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_tags_name ON tags(name);
CREATE INDEX IF NOT EXISTS idx_tags_is_auto ON tags(is_auto);

-- item_tags links knowledge_items to tags (many-to-many)
CREATE TABLE IF NOT EXISTS item_tags (
    content_id TEXT NOT NULL REFERENCES knowledge_items(content_id) ON DELETE CASCADE,
    tag_id TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (content_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_item_tags_content_id ON item_tags(content_id);
CREATE INDEX IF NOT EXISTS idx_item_tags_tag_id ON item_tags(tag_id);
`,
	},
	// Migration 5 removes FTS5 virtual tables and their sync triggers.
	// All search is now vector/semantic only.
	{
		Version: 5,
		Name:    "drop_fts_and_unify",
		SQL: `
DROP TRIGGER IF EXISTS chunks_ai;
DROP TRIGGER IF EXISTS chunks_ad;
DROP TRIGGER IF EXISTS chunks_au;
DROP TRIGGER IF EXISTS knowledge_ai;
DROP TRIGGER IF EXISTS knowledge_ad;
DROP TRIGGER IF EXISTS knowledge_au;
DROP TABLE IF EXISTS chunks_fts;
DROP TABLE IF EXISTS knowledge_fts;
`,
	},
	// Migration 6 ensures the memories table exists on installs that pre-date
	// its addition to schemaSQL. CREATE TABLE IF NOT EXISTS is a no-op on
	// fresh DBs (where v1 already created it).
	{
		Version: 6,
		Name:    "ensure_memories_table",
		SQL: `
CREATE TABLE IF NOT EXISTS memories (
    memory_id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    content TEXT NOT NULL,
    context_id TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME,
    score FLOAT
);
CREATE INDEX IF NOT EXISTS idx_memories_type ON memories(type);
CREATE INDEX IF NOT EXISTS idx_memories_context_id ON memories(context_id);
`,
	},
	// Migration 7 adds Phase 3.3 columns to memories: source, accepted_at,
	// embedding, session_id. Existing rows are back-filled to source='manual'
	// and accepted_at=created_at so prior auto-extracted memories continue to
	// be injected (we can't ask the user to retroactively review them, and
	// silently dropping established memories would surprise long-term users).
	// Fresh extractions made after this migration apply will land with
	// source='extracted' and accepted_at=NULL — i.e. require user review.
	//
	// Note: schemaSQL (v1) already declares these columns on fresh installs.
	// Production DBs that pre-date v7 still need the ALTER TABLEs. We can't
	// use "ADD COLUMN IF NOT EXISTS" (SQLite < 3.35), so the migration is
	// applied through a per-statement runner (applyMemoriesV7Migration) that
	// inspects PRAGMA table_info and only adds missing columns.
	{
		Version: 7,
		Name:    "memories_long_term_columns",
		SQL:     "", // handled by applyMigrations special-case below
	},
	// Migration 8 introduces interaction_log, the append-only signal stream
	// that powers Phase 1 (learning progress bar) and unlocks phases 2-5
	// (recap slot detection, ranking signals, contradiction prioritisation).
	// Idempotent on fresh installs because schemaSQL v1 already declares
	// the table.
	{
		Version: 8,
		Name:    "interaction_log",
		SQL: `
CREATE TABLE IF NOT EXISTS interaction_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL,
    ref_kind TEXT,
    ref_id TEXT,
    payload TEXT,
    occurred_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    session_id TEXT
);
CREATE INDEX IF NOT EXISTS idx_interaction_log_kind_time ON interaction_log(kind, occurred_at);
CREATE INDEX IF NOT EXISTS idx_interaction_log_occurred_at ON interaction_log(occurred_at);
`,
	},
	// Migration 9 introduces the hybrid-retrieval storage layer: the `sections`
	// block table (small-to-big), chunks.section_id, and the chunks_fts FTS5
	// index + sync triggers (BM25 lexical search). Handled by
	// applySectionsAndFTSV9Migration (special-cased below) so it stays
	// idempotent across fresh/upgraded installs and so FTS5 trigger creation
	// runs as discrete statements. Requires the sqlite_fts5 build tag.
	{
		Version: 9,
		Name:    "sections_and_fts5",
		SQL:     "", // handled by applySectionsAndFTSV9Migration
	},
	// Migration 10 adds persistent chat transcripts: chat_sessions +
	// chat_messages. Idempotent on fresh installs (schemaSQL v1 now declares
	// both tables). Upgraded DBs get them created here.
	{
		Version: 10,
		Name:    "chat_sessions",
		SQL: `
CREATE TABLE IF NOT EXISTS chat_sessions (
    session_id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    project_id TEXT REFERENCES projects(project_id) ON DELETE SET NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_chat_sessions_updated_at ON chat_sessions(updated_at);
CREATE INDEX IF NOT EXISTS idx_chat_sessions_project_id ON chat_sessions(project_id);
CREATE TABLE IF NOT EXISTS chat_messages (
    message_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES chat_sessions(session_id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    sources TEXT,
    ordinal INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_chat_messages_session ON chat_messages(session_id, ordinal);
`,
	},
	// Migration 11 adds persistent LLM token accounting (daily per-category
	// counters, UPSERTed) and a generic key/value app_settings store used here
	// for the cost-estimate pricing.
	{
		Version: 11,
		Name:    "token_usage_and_settings",
		SQL: `
CREATE TABLE IF NOT EXISTS token_usage (
    day        TEXT NOT NULL,
    category   TEXT NOT NULL,
    tokens_in  INTEGER NOT NULL DEFAULT 0,
    tokens_out INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, category)
);
CREATE TABLE IF NOT EXISTS app_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`,
	},
	// Migration 12 adds chat_message_attachments: the image/audio media of a
	// user turn, so a reopened conversation re-displays the image and replays
	// the audio. data is NULL once an audio recording is purged by the size cap
	// (the row stays so the UI shows a clean "no longer available" placeholder).
	// Idempotent on fresh installs (schemaSQL v1 declares the table too).
	{
		Version: 12,
		Name:    "chat_message_attachments",
		SQL: `
CREATE TABLE IF NOT EXISTS chat_message_attachments (
    message_id TEXT NOT NULL REFERENCES chat_messages(message_id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL DEFAULT 0,
    type TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT '',
    format TEXT NOT NULL DEFAULT '',
    data BLOB,
    byte_size INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (message_id, ordinal)
);
CREATE INDEX IF NOT EXISTS idx_chat_attachments_type ON chat_message_attachments(type, created_at);
`,
	},
	{
		Version: 13,
		Name:    "tasks",
		SQL: `
CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',
    due_date TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    source_content_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status, created_at);
CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project_id);
`,
	},
	// Migration 14 records contradictions the user has dismissed ("seen it,
	// hide it"). Keyed by a stable hash of the conflict (cluster + entity +
	// attribute + value set) so it survives recomputation. Per-tenant DB → no
	// tenant column.
	{
		Version: 14,
		Name:    "dismissed_contradictions",
		SQL: `
CREATE TABLE IF NOT EXISTS dismissed_contradictions (
    key          TEXT PRIMARY KEY,
    dismissed_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
`,
	},
	// Migration 15 turns tasks into note-like knowledge_items: a task is a
	// knowledge_item (source_type='task') carrying a Markdown body, tags
	// (item_tags) and a project (project_links) like a note, plus task state in
	// task_attrs (status, due_date). Existing rows from the standalone `tasks`
	// table are migrated into the new model, then that table is dropped (the
	// off-box backup is the rollback). project_links are recreated only when the
	// referenced project still exists, to respect the FK.
	{
		Version: 15,
		Name:    "tasks_as_knowledge_items",
		SQL: `
CREATE TABLE IF NOT EXISTS task_attrs (
    content_id   TEXT PRIMARY KEY REFERENCES knowledge_items(content_id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'open',
    due_date     TEXT NOT NULL DEFAULT '',
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_task_attrs_due ON task_attrs(due_date);
CREATE INDEX IF NOT EXISTS idx_task_attrs_status ON task_attrs(status);

INSERT OR IGNORE INTO knowledge_items (content_id, source_type, title, normalized_text, version_id, created_at, updated_at)
    SELECT id, 'task', title, '', 'v1', created_at, updated_at FROM tasks;
INSERT OR IGNORE INTO task_attrs (content_id, status, due_date, created_at, updated_at)
    SELECT id, status, due_date, created_at, updated_at FROM tasks;
INSERT OR IGNORE INTO project_links (link_id, project_id, content_id)
    SELECT lower(hex(randomblob(16))), project_id, id
    FROM tasks
    WHERE project_id != '' AND project_id IN (SELECT project_id FROM projects);
DROP TABLE tasks;
`,
	},
	// Migration 16 — Chronicle: per-chapter narrative state. The acts (the nightly
	// prose blocks) live as knowledge_items (source_type=chronicle_act); this table
	// holds the rolling synopsis (continuity), the watermark (last chronicled
	// ingestion time, so traces are never re-narrated), status and project link.
	{
		Version: 16,
		Name:    "chronicle_chapters",
		SQL: `
CREATE TABLE IF NOT EXISTS chronicle_chapters (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL DEFAULT '',
    title       TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'open',
    synopsis    TEXT NOT NULL DEFAULT '',
    watermark   TEXT NOT NULL DEFAULT '',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);
`,
	},
	// Migration 17 — Chronicle reopen: a staged, free-text note set when the user
	// reopens a closed chapter. The next nightly write folds it into the resumption
	// narration (grounded by the user's own words + corroborating traces), then clears it.
	{
		Version: 17,
		Name:    "chronicle_pending_note",
		SQL:     `ALTER TABLE chronicle_chapters ADD COLUMN pending_note TEXT NOT NULL DEFAULT '';`,
	},
	// Migration 18 — Decisions: the user's decisions/commitments as first-class,
	// note-like knowledge_items (source_type='decision') carrying a Markdown
	// rationale, tags and a project like a note, plus decision state in
	// decision_attrs (status, the date it was decided, the source item ids that
	// ground it). status: 'proposed' (detected by the nightly scan, awaiting the
	// user's confirmation), 'standing' (active), 'superseded' (no longer holds).
	// dedup_key (hash of source ref + statement) makes the nightly scan idempotent
	// — the same decision is never re-proposed.
	{
		Version: 18,
		Name:    "decisions",
		SQL: `
CREATE TABLE IF NOT EXISTS decision_attrs (
    content_id   TEXT PRIMARY KEY REFERENCES knowledge_items(content_id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'standing',
    decided_on   TEXT NOT NULL DEFAULT '',
    source_refs  TEXT NOT NULL DEFAULT '[]',
    dedup_key    TEXT NOT NULL DEFAULT '',
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_decision_attrs_status ON decision_attrs(status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_decision_attrs_dedup ON decision_attrs(dedup_key) WHERE dedup_key != '';
`,
	},
	// Migration 19 — durable cache of reconciled contradictions (W6). The
	// reconciliation is LLM-backed and was only cached in-memory (lost on restart,
	// per-process). Persisting the latest result per scope makes it readable
	// instantly + cheaply by Ask (brain-context injection) and the daily digest,
	// without recomputing. One JSON blob per scope ("" = all mail+notes).
	{
		Version: 19,
		Name:    "contradiction_cache",
		SQL: `
CREATE TABLE IF NOT EXISTS contradiction_cache (
    scope         TEXT PRIMARY KEY,
    conflicts     TEXT NOT NULL DEFAULT '[]',
    scanned       INTEGER NOT NULL DEFAULT 0,
    computed_at   DATETIME DEFAULT CURRENT_TIMESTAMP
);
`,
	},
	// Migration 20 — "Quand Hygur rêve" Phase 0: the per-item ACCESS signal, the
	// pivot of memory consolidation (docs/DREAM_PLAN.md). Stamped when an item is
	// CITED in an answer (the "useful" signal, not a raw vector match). Separate
	// table (not columns on knowledge_items) so a citation never rewrites the hot
	// item row (with its big normalized_text). No FK: content_ids that aren't
	// knowledge_items (e.g. synthetic thread ids) must not abort the batch; orphan
	// rows are tiny and reaped during consolidation. OBSERVE-ONLY for now —
	// nothing reads this yet; we measure before tiering.
	{
		Version: 20,
		Name:    "item_access",
		SQL: `
CREATE TABLE IF NOT EXISTS item_access (
    content_id        TEXT PRIMARY KEY,
    hit_count         INTEGER NOT NULL DEFAULT 0,
    last_accessed_at  DATETIME
);
CREATE INDEX IF NOT EXISTS idx_item_access_last ON item_access(last_accessed_at);
`,
	},
	// Migration 21 — per-cluster reconcile verdict cache. The W6 contradiction
	// Reconcile (LLM, one call per cluster) was the steady-state chat-token hog:
	// every cold recompute re-judged EVERY cluster. The cluster Key encodes the
	// exact claim set (cluster+entity+attribute+values), so a verdict is valid for
	// that Key forever. Cache every verdict INCLUDING 'none', so a recompute only
	// calls the LLM for clusters whose claims actually changed (new Keys).
	{
		Version: 21,
		Name:    "reconcile_verdicts",
		SQL: `
CREATE TABLE IF NOT EXISTS reconcile_verdicts (
    cluster_key  TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    reason       TEXT NOT NULL DEFAULT '',
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP
);
`,
	},
	// Migration 22 adds the recycle bin for deletion reconciliation. When a synced
	// source (e.g. mail) reports an item is gone from the server, we MOVE its
	// knowledge_items row here then delete it — the cascade removes chunks/sections/
	// vectors/FTS/claims/tags, so the item vanishes from EVERY read surface at once
	// (no per-query "absent" filter to forget), while staying restorable. A grace
	// period (miss_count) defers the physical purge so a transient bad enumeration
	// can't destroy data; a reappearing item is re-ingested and its row dropped.
	{
		Version: 22,
		Name:    "kb_recycle",
		SQL: `
CREATE TABLE IF NOT EXISTS kb_recycle (
    content_id      TEXT PRIMARY KEY,
    source_type     TEXT NOT NULL,
    source_path     TEXT,
    title           TEXT NOT NULL DEFAULT '',
    normalized_text TEXT NOT NULL DEFAULT '',
    metadata        TEXT,
    source_ref      TEXT NOT NULL DEFAULT '',
    item_created_at DATETIME,
    removed_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    miss_count      INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_kb_recycle_source_ref ON kb_recycle(source_ref);
`,
	},
	// Migration 23 — entity_mentions: a queryable index of the (entity, attribute)
	// pairs each item asserts a claim about. Claims are already extracted at
	// indexing time (extracted_claims in metadata) but were inert JSON; this lifts
	// the entity into a lookup table so retrieval can fan out from a queried entity
	// to every item that mentions it (the associative lens), and so a later
	// embedding-synonymy pass can expand the lookup set. ON DELETE CASCADE keeps the
	// index consistent with the recycle-bin purge — a deleted item's mentions vanish
	// with its row. Populated from the cached claims (deterministic, no LLM).
	{
		Version: 23,
		Name:    "entity_mentions",
		SQL: `
CREATE TABLE IF NOT EXISTS entity_mentions (
    entity_norm TEXT NOT NULL,
    entity_raw  TEXT NOT NULL DEFAULT '',
    content_id  TEXT NOT NULL REFERENCES knowledge_items(content_id) ON DELETE CASCADE,
    attribute   TEXT NOT NULL DEFAULT '',
    asserted_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (entity_norm, content_id, attribute)
);
CREATE INDEX IF NOT EXISTS idx_entity_mentions_norm ON entity_mentions(entity_norm);
CREATE INDEX IF NOT EXISTS idx_entity_mentions_content ON entity_mentions(content_id);
`,
	},
	// Migration 24 — entity_vectors: one embedding per normalized entity, for the
	// brick-2 synonymy expansion. A queried entity is embedded and matched by cosine
	// against these so surface-different mentions of the same thing (e.g. an
	// anglicism and its French equivalent) resolve to one lookup set. The model is
	// pinned per row so a fleet embedding-model change never compares across vector
	// spaces. No FK: entity_norm is a shared canonical string, not a knowledge_items
	// key; orphan vectors (all mentions deleted) are tiny and harmless.
	{
		Version: 24,
		Name:    "entity_vectors",
		SQL: `
CREATE TABLE IF NOT EXISTS entity_vectors (
    entity_norm TEXT PRIMARY KEY,
    embedding   BLOB NOT NULL,
    model       TEXT NOT NULL DEFAULT ''
);
`,
	},
	// Web-push subscriptions (browser notifications when the tab is closed).
	// Keyed by the push endpoint URL; p256dh/auth are the per-device crypto keys.
	{
		Version: 25,
		Name:    "push_subscriptions",
		SQL: `
CREATE TABLE IF NOT EXISTS push_subscriptions (
    endpoint   TEXT PRIMARY KEY,
    p256dh     TEXT NOT NULL,
    auth       TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT ''
);
`,
	},
	// Migration 26 — index_retry: a durable queue of content units (mail threads)
	// that failed to index for a TRANSIENT reason (embedder down / timeout / ubatch
	// overflow). Drained before each incremental sync so the failure is replayed
	// and never becomes a silent permanent gap in the KB (RELIABILITY_BACKLOG R1).
	// The PK is the unit's identity, so re-failing UPDATES the row instead of
	// duplicating it; re-indexing dedups by content hash, so recovery yields
	// exactly one knowledge item.
	{
		Version: 26,
		Name:    "index_retry",
		SQL: `
CREATE TABLE IF NOT EXISTS index_retry (
    connector_id    TEXT NOT NULL,
    account_id      TEXT NOT NULL,
    source_ref      TEXT NOT NULL,
    reason          TEXT NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 0,
    first_failed_at TEXT NOT NULL DEFAULT '',
    next_attempt_at TEXT NOT NULL DEFAULT '',
    last_error      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (connector_id, account_id, source_ref)
);
CREATE INDEX IF NOT EXISTS idx_index_retry_due ON index_retry(connector_id, account_id, next_attempt_at);
`,
	},
	// Migration 27 — item_signals: the deterministic memory-consolidation scores
	// ("Quand Hygur rêve", DREAM_PLAN Phase 1 / docs/DREAM_PLAN_ADDENDUM.md). A
	// sidecar table (kept off the hot knowledge_items row) holding the nightly
	// salience + forgetting-strength + hot/cold tier decision. SHADOW today: it is
	// written and logged, but drives NO eviction yet (that is Phase E). rehydrated_at
	// is reserved for the future re-hydration path.
	{
		Version: 27,
		Name:    "item_signals",
		SQL: `
CREATE TABLE IF NOT EXISTS item_signals (
    content_id    TEXT PRIMARY KEY REFERENCES knowledge_items(content_id) ON DELETE CASCADE,
    salience      REAL NOT NULL DEFAULT 0,
    strength      REAL NOT NULL DEFAULT 0,
    surprise      REAL NOT NULL DEFAULT 0,
    exempt        INTEGER NOT NULL DEFAULT 0,
    tier          TEXT NOT NULL DEFAULT 'hot',
    rehydrated_at DATETIME,
    scored_at     DATETIME
);
CREATE INDEX IF NOT EXISTS idx_item_signals_tier ON item_signals(tier, salience);
`,
	},
	// Migration 28 — item_surprise: the per-item surprise/novelty score ("Quand
	// Hygur rêve" Phase C / docs/DREAM_PLAN_ADDENDUM.md §2). Written at ingestion from
	// how new an item is vs the entity index; read by the consolidation pass to nudge
	// salience. Its own table so a pass re-score can't overwrite the stamped value.
	{
		Version: 28,
		Name:    "item_surprise",
		SQL: `
CREATE TABLE IF NOT EXISTS item_surprise (
    content_id  TEXT PRIMARY KEY REFERENCES knowledge_items(content_id) ON DELETE CASCADE,
    surprise    REAL NOT NULL DEFAULT 0,
    computed_at DATETIME
);
`,
	},
	// Migration 29 — entity_edges: Hebbian co-occurrence graph ("Quand Hygur rêve"
	// Phase D / docs/DREAM_PLAN_ADDENDUM.md §3). Entities that co-occur in an item
	// strengthen their edge; the weight decays with time. Seeds associative
	// expansion + a future cognitive map. Written best-effort at ingestion; read only
	// behind the (default-off) Hebbian expansion flag.
	{
		Version: 29,
		Name:    "entity_edges",
		SQL: `
CREATE TABLE IF NOT EXISTS entity_edges (
    entity_a   TEXT NOT NULL,
    entity_b   TEXT NOT NULL,
    co_count   INTEGER NOT NULL DEFAULT 0,
    last_co_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (entity_a, entity_b)
);
CREATE INDEX IF NOT EXISTS idx_entity_edges_a ON entity_edges(entity_a);
CREATE INDEX IF NOT EXISTS idx_entity_edges_b ON entity_edges(entity_b);
`,
	},
	{
		Version: 30,
		Name:    "item_norm",
		// Materialized identifier index: one alnum-only, lowercased copy of each item's
		// text (identifier.Normalize), so an exact-identifier query matches a formatted
		// value (e.g. "23.02.23:347-71") deterministically via LIKE. Derived + rebuildable;
		// kept in sync by a Go upsert on ingest (triggers can't run the Go normalizer).
		SQL: `
CREATE TABLE IF NOT EXISTS item_norm (
    content_id TEXT PRIMARY KEY,
    norm       TEXT NOT NULL DEFAULT ''
);
`,
	},
	{
		Version: 31,
		Name:    "entity_identifier_link",
		// Proximity links (entity ↔ typed identifier) emitted only when the pairing is
		// unambiguous in a document (nearest same-type, clear runner-up margin, mutual). A
		// per-doc confidence signal the lookup aggregates on top of NPMI to break the
		// family-member tie that doc-level co-occurrence cannot.
		SQL: `
CREATE TABLE IF NOT EXISTS entity_identifier_link (
    content_id  TEXT NOT NULL,
    person_norm TEXT NOT NULL,
    id_norm     TEXT NOT NULL,
    id_type     TEXT NOT NULL DEFAULT '',
    prox        REAL NOT NULL DEFAULT 0,
    PRIMARY KEY (content_id, person_norm, id_norm)
);
CREATE INDEX IF NOT EXISTS idx_eil_person ON entity_identifier_link(person_norm);
CREATE INDEX IF NOT EXISTS idx_eil_id ON entity_identifier_link(id_norm);
`,
	},
	// Migration 32 adds per-(category, pass) LLM token detail feeding the
	// operator GET /usage/by-pass endpoint. Purely additive: token_usage stays
	// the sole source of truth for the chat caps (ChatTokensToday/ThisMonth) and
	// is left untouched — this table only carries the finer per-pass breakdown.
	// Same idempotent UPSERT shape as token_usage; created on fresh installs too
	// (migrations run on an empty DB).
	{
		Version: 32,
		Name:    "token_usage_pass",
		SQL: `
CREATE TABLE IF NOT EXISTS token_usage_pass (
    day        TEXT NOT NULL,
    category   TEXT NOT NULL,
    pass       TEXT NOT NULL DEFAULT '',
    tokens_in  INTEGER NOT NULL DEFAULT 0,
    tokens_out INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, category, pass)
);
`,
	},
	// Migration 33 adds knowledge_items.raw_text: the ORIGINAL item text with line
	// breaks + case preserved, stored alongside the collapsed/lowercased
	// normalized_text so the Library and the LLM read the real content while
	// FTS/embedding/dedup keep using normalized_text. Nullable — old rows have no
	// raw (readers fall back to normalized_text via KnowledgeItem.DisplayText).
	// Handled by applyRawTextV33Migration (PRAGMA-guarded, like v7): schemaSQL v1
	// already declares the column on fresh installs, so the ALTER runs only when
	// it is actually missing.
	{
		Version: 33,
		Name:    "knowledge_items_raw_text",
		SQL:     "", // handled by applyRawTextV33Migration
	},
	// Migration 34 — figure_nodes: labelled MONETARY figures as engram NODES with typed CONTEXT
	// EDGES (FIGURES_TRUTH_PLAN §3 / F1). The NODE is (value, unit); the EDGES are entity_norm
	// (whose figure — the same canonical key the entity graph uses), period, direction and
	// content_id (source). Written at ingest by the deterministic figure extractor + proximity
	// attribution; read by a deterministic traversal (filter label+direction, order by period,
	// pick latest / decline). The composite PK lets one document carry several figures of the same
	// label that differ by period or direction ("TVA à payer Q1" vs "TVA remboursée Q2").
	{
		Version: 34,
		Name:    "figure_nodes",
		SQL: `
CREATE TABLE IF NOT EXISTS figure_nodes (
    content_id  TEXT NOT NULL,
    entity_norm TEXT NOT NULL,
    label       TEXT NOT NULL DEFAULT '',
    value       TEXT NOT NULL DEFAULT '',
    raw         TEXT NOT NULL DEFAULT '',
    unit        TEXT NOT NULL DEFAULT '',
    period      TEXT NOT NULL DEFAULT '',
    direction   TEXT NOT NULL DEFAULT '',
    prox        REAL NOT NULL DEFAULT 0,
    PRIMARY KEY (content_id, entity_norm, label, value, period, direction)
);
CREATE INDEX IF NOT EXISTS idx_figure_nodes_entity ON figure_nodes(entity_norm);
CREATE INDEX IF NOT EXISTS idx_figure_nodes_label ON figure_nodes(label);
`,
	},
	// Migration 35 — dosage/quantity context edges on figure_nodes (C7, FIGURES_TRUTH_PLAN
	// generalized beyond money). A dosage is the SAME figure NODE (value+unit) with two extra edges:
	// MEDICATION (the qualifier the shared "dose" label denotes — the analogue of a rate's client)
	// and FREQUENCY (its cadence). Additive columns keep every existing monetary figure unchanged
	// (both default ''); the unit is now DATA (figure.unitTable), so mg/mcg/ml/IU live beside EUR.
	{
		Version: 35,
		Name:    "figure_nodes_dosage",
		SQL: `
ALTER TABLE figure_nodes ADD COLUMN medication TEXT NOT NULL DEFAULT '';
ALTER TABLE figure_nodes ADD COLUMN frequency  TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_figure_nodes_medication ON figure_nodes(medication);
`,
	},
	// Migration 36 — meeting_nodes: a meeting TIME as a determined temporal fact, the meeting-time
	// analogue of figure_nodes (contradiction-aware rendez-vous). The NODE is the datetime (when_utc);
	// the EDGES are entity_norm (whom the meeting is with — the same canonical entity key), source
	// (email | calendar) and content_id (the message / calendar event). asserted_at is the assertion
	// timestamp the C7 supersession mechanism (figure.ResolveTemporal) orders "latest wins" by, so the
	// latest email time supersedes a stale calendar time and the disagreement surfaces as a
	// cross-source contradiction. The composite PK lets each source hold ONE time per meeting subject.
	{
		Version: 36,
		Name:    "meeting_nodes",
		SQL: `
CREATE TABLE IF NOT EXISTS meeting_nodes (
    content_id  TEXT NOT NULL,
    entity_norm TEXT NOT NULL,
    when_utc    TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT '',
    asserted_at TEXT NOT NULL DEFAULT '',
    title       TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (content_id, entity_norm, source)
);
CREATE INDEX IF NOT EXISTS idx_meeting_nodes_entity ON meeting_nodes(entity_norm);
`,
	},
}

// applyMigrations applies all pending migrations to the database.
func applyMigrations(db *sql.DB) error {
	// Ensure schema_version table exists
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("failed to create schema_version table: %w", err)
	}

	// Get current version
	var currentVersion int
	err = db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("failed to get current schema version: %w", err)
	}

	// Apply pending migrations
	for _, m := range migrations {
		if m.Version <= currentVersion {
			continue
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction for migration %d: %w", m.Version, err)
		}

		// Migration 7 needs to be idempotent on fresh installs (schemaSQL v1
		// already declares the new memories columns). Older DBs that only
		// hold the v6 schema still need the ALTER TABLEs. The custom runner
		// handles both paths cleanly.
		if m.Version == 7 {
			if err := applyMemoriesV7Migration(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("failed to apply migration %d (%s): %w", m.Version, m.Name, err)
			}
		} else if m.Version == 9 {
			if err := applySectionsAndFTSV9Migration(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("failed to apply migration %d (%s): %w", m.Version, m.Name, err)
			}
		} else if m.Version == 33 {
			if err := applyRawTextV33Migration(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("failed to apply migration %d (%s): %w", m.Version, m.Name, err)
			}
		} else if m.SQL != "" {
			if _, err := tx.Exec(m.SQL); err != nil {
				tx.Rollback()
				return fmt.Errorf("failed to apply migration %d (%s): %w", m.Version, m.Name, err)
			}
		}

		_, err = tx.Exec("INSERT INTO schema_version (version) VALUES (?)", m.Version)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to record migration %d: %w", m.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration %d: %w", m.Version, err)
		}
	}

	return nil
}

// applyMemoriesV7Migration adds the Phase 3.3 columns to memories only when
// they are missing. Fresh installs already see them via schemaSQL, so the
// ALTERs would error with "duplicate column name". We inspect PRAGMA
// table_info(memories) and add only what's missing, then back-fill
// accepted_at = created_at for legacy rows.
func applyMemoriesV7Migration(tx *sql.Tx) error {
	existing, err := existingColumns(tx, "memories")
	if err != nil {
		return err
	}
	type colSpec struct {
		name string
		sql  string
	}
	wanted := []colSpec{
		{name: "source", sql: "ALTER TABLE memories ADD COLUMN source TEXT NOT NULL DEFAULT 'manual'"},
		{name: "accepted_at", sql: "ALTER TABLE memories ADD COLUMN accepted_at DATETIME"},
		{name: "embedding", sql: "ALTER TABLE memories ADD COLUMN embedding BLOB"},
		{name: "session_id", sql: "ALTER TABLE memories ADD COLUMN session_id TEXT"},
	}
	for _, c := range wanted {
		if _, ok := existing[c.name]; ok {
			continue
		}
		if _, err := tx.Exec(c.sql); err != nil {
			return fmt.Errorf("add column %s: %w", c.name, err)
		}
	}
	// Back-fill accepted_at for legacy rows that pre-date the column. New
	// rows inserted after this migration can have NULL (= pending).
	if _, err := tx.Exec(`UPDATE memories SET accepted_at = created_at WHERE accepted_at IS NULL`); err != nil {
		return fmt.Errorf("backfill accepted_at: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_source ON memories(source)`); err != nil {
		return fmt.Errorf("index source: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_accepted_at ON memories(accepted_at)`); err != nil {
		return fmt.Errorf("index accepted_at: %w", err)
	}
	return nil
}

// applyRawTextV33Migration adds knowledge_items.raw_text only when it is missing.
// Fresh installs already see it via schemaSQL (v1), so a blind ALTER would fail
// with "duplicate column name". We inspect PRAGMA table_info(knowledge_items) and
// add the column only when absent — idempotent across fresh and upgraded DBs.
// The column is left NULL for existing rows (no backfill): readers fall back to
// normalized_text via KnowledgeItem.DisplayText until a row is re-ingested.
func applyRawTextV33Migration(tx *sql.Tx) error {
	existing, err := existingColumns(tx, "knowledge_items")
	if err != nil {
		return err
	}
	if _, ok := existing["raw_text"]; ok {
		return nil
	}
	if _, err := tx.Exec(`ALTER TABLE knowledge_items ADD COLUMN raw_text TEXT`); err != nil {
		return fmt.Errorf("add knowledge_items.raw_text: %w", err)
	}
	return nil
}

// applySectionsAndFTSV9Migration introduces the hierarchical + hybrid-search
// storage layer:
//   - the `sections` block table and chunks.section_id (small-to-big),
//   - the `chunks_fts` FTS5 virtual table + sync triggers (BM25 lexical search).
//
// Idempotent across fresh and upgraded DBs: schemaSQL (v1) already declares
// `sections` and chunks.section_id on fresh installs, so those are guarded with
// IF NOT EXISTS / PRAGMA inspection. The FTS5 objects live only here (not in
// schemaSQL) so each CREATE runs as a discrete statement.
//
// NOTE: requires the sqlite_fts5 build tag (see the Makefile). Without it,
// "CREATE VIRTUAL TABLE ... USING fts5" fails with "no such module: fts5".
func applySectionsAndFTSV9Migration(tx *sql.Tx) error {
	// 1. sections table + indexes (no-op when schemaSQL already created them).
	for _, s := range []string{
		`CREATE TABLE IF NOT EXISTS sections (
			section_id TEXT PRIMARY KEY,
			content_id TEXT NOT NULL REFERENCES knowledge_items(content_id) ON DELETE CASCADE,
			parent_section_id TEXT,
			heading TEXT,
			heading_path TEXT,
			level INTEGER NOT NULL DEFAULT 0,
			ordinal INTEGER NOT NULL DEFAULT 0,
			full_text TEXT NOT NULL,
			token_count INTEGER NOT NULL DEFAULT 0,
			metadata TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sections_content_id ON sections(content_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sections_parent ON sections(parent_section_id)`,
	} {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("sections ddl: %w", err)
		}
	}

	// 2. chunks.section_id — added via PRAGMA inspection (SQLite < 3.35 lacks
	//    ADD COLUMN IF NOT EXISTS; fresh installs already have it via schemaSQL).
	cols, err := existingColumns(tx, "chunks")
	if err != nil {
		return err
	}
	if _, ok := cols["section_id"]; !ok {
		if _, err := tx.Exec(`ALTER TABLE chunks ADD COLUMN section_id TEXT`); err != nil {
			return fmt.Errorf("add chunks.section_id: %w", err)
		}
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_chunks_section_id ON chunks(section_id)`); err != nil {
		return fmt.Errorf("index chunks.section_id: %w", err)
	}

	// 3. FTS5 index over chunk text + sync triggers. Standalone (not external
	//    content) so chunk_id/content_id are returned directly from a MATCH.
	//    French tokenizer: unicode61 with diacritics folded.
	for _, s := range []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
			chunk_id UNINDEXED,
			content_id UNINDEXED,
			text,
			tokenize = 'unicode61 remove_diacritics 2'
		)`,
		`CREATE TRIGGER IF NOT EXISTS chunks_fts_ai AFTER INSERT ON chunks BEGIN
			INSERT INTO chunks_fts(chunk_id, content_id, text)
			VALUES (new.chunk_id, new.content_id, new.text);
		END`,
		`CREATE TRIGGER IF NOT EXISTS chunks_fts_ad AFTER DELETE ON chunks BEGIN
			DELETE FROM chunks_fts WHERE chunk_id = old.chunk_id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS chunks_fts_au AFTER UPDATE ON chunks BEGIN
			DELETE FROM chunks_fts WHERE chunk_id = old.chunk_id;
			INSERT INTO chunks_fts(chunk_id, content_id, text)
			VALUES (new.chunk_id, new.content_id, new.text);
		END`,
	} {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("fts5 ddl: %w", err)
		}
	}

	// 4. Backfill the FTS index from any chunks predating the triggers. The
	//    anti-join probes chunks_fts by chunk_id, which is UNINDEXED in FTS5, so
	//    it falls back to a full FTS scan PER chunk → O(n²). ensureRAGSchema re-runs
	//    this on EVERY boot, so guard it with a cheap count: skip when the index is
	//    already in sync (the steady state). Only an out-of-sync DB — the table was
	//    just created, or a version-mismatch skipped v9 — pays the one-time backfill.
	//    (Without the guard, boot time grew quadratically with the chunk count:
	//    ~10s at 2.7k chunks, ~106s at 5.2k.)
	var nChunks, nFTS int
	if err := tx.QueryRow(`SELECT count(*) FROM chunks`).Scan(&nChunks); err != nil {
		return fmt.Errorf("count chunks: %w", err)
	}
	if err := tx.QueryRow(`SELECT count(*) FROM chunks_fts`).Scan(&nFTS); err != nil {
		return fmt.Errorf("count chunks_fts: %w", err)
	}
	if nFTS < nChunks {
		if _, err := tx.Exec(`
			INSERT INTO chunks_fts(chunk_id, content_id, text)
			SELECT c.chunk_id, c.content_id, c.text
			FROM chunks c
			WHERE NOT EXISTS (SELECT 1 FROM chunks_fts f WHERE f.chunk_id = c.chunk_id)
		`); err != nil {
			return fmt.Errorf("backfill fts5: %w", err)
		}
	}
	return nil
}

// existingColumns returns the set of column names on `table`, derived from
// PRAGMA table_info. Used by applyMemoriesV7Migration to make the migration
// idempotent across fresh and upgraded installs.
func existingColumns(tx *sql.Tx, table string) (map[string]struct{}, error) {
	rows, err := tx.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, fmt.Errorf("pragma table_info(%s): %w", table, err)
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("scan column info: %w", err)
		}
		out[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSchemaVersion returns the current schema version from the database.
func (d *DB) GetSchemaVersion() (int, error) {
	var version int
	err := d.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("failed to get schema version: %w", err)
	}
	return version, nil
}
