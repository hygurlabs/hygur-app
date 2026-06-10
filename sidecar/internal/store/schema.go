// Package store provides SQLite database access for the Hygur knowledge base.
package store

// schemaSQL contains the SQL statements to create the database schema.
const schemaSQL = `
-- knowledge_items stores the main knowledge entries
CREATE TABLE IF NOT EXISTS knowledge_items (
    content_id TEXT PRIMARY KEY,
    source_type TEXT NOT NULL,  -- markdown, pdf, docx, txt, email, thread
    source_path TEXT,
    title TEXT NOT NULL,
    normalized_text TEXT NOT NULL,
    metadata TEXT,  -- JSON
    version_id TEXT NOT NULL DEFAULT 'v1',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_knowledge_items_source_type ON knowledge_items(source_type);
CREATE INDEX IF NOT EXISTS idx_knowledge_items_created_at ON knowledge_items(created_at);

-- chunks stores embeddings-ready text chunks.
-- section_id links a chunk to its parent logical block (the sections table):
-- chunks are the precise recall unit, sections are the big block given to the LLM.
CREATE TABLE IF NOT EXISTS chunks (
    chunk_id TEXT PRIMARY KEY,
    content_id TEXT NOT NULL REFERENCES knowledge_items(content_id) ON DELETE CASCADE,
    section_id TEXT,
    chunk_hash TEXT NOT NULL,
    embedding_model TEXT,
    text TEXT NOT NULL,
    metadata TEXT,  -- JSON
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_chunks_content_id ON chunks(content_id);
CREATE INDEX IF NOT EXISTS idx_chunks_section_id ON chunks(section_id);
CREATE INDEX IF NOT EXISTS idx_chunks_hash ON chunks(chunk_hash);

-- chunk_vectors stores embedding vectors for semantic search
CREATE TABLE IF NOT EXISTS chunk_vectors (
    chunk_id TEXT PRIMARY KEY REFERENCES chunks(chunk_id) ON DELETE CASCADE,
    embedding BLOB NOT NULL,  -- float32 array serialized as little-endian bytes
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_chunk_vectors_chunk_id ON chunk_vectors(chunk_id);

-- sections stores complete logical blocks of a document (a heading and its body
-- down to the next same-or-higher heading). Hierarchical retrieval narrows
-- document -> section, then hands the section's full_text to the model instead
-- of an arbitrary fixed-size chunk ("small-to-big"). parent_section_id encodes
-- the heading hierarchy; level is the heading depth (1=H1…, 0 = preamble/root).
CREATE TABLE IF NOT EXISTS sections (
    section_id TEXT PRIMARY KEY,
    content_id TEXT NOT NULL REFERENCES knowledge_items(content_id) ON DELETE CASCADE,
    parent_section_id TEXT,
    heading TEXT,
    heading_path TEXT,  -- JSON array of ancestor headings incl. self, root-first
    level INTEGER NOT NULL DEFAULT 0,
    ordinal INTEGER NOT NULL DEFAULT 0,
    full_text TEXT NOT NULL,
    token_count INTEGER NOT NULL DEFAULT 0,
    metadata TEXT,  -- JSON
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_sections_content_id ON sections(content_id);
CREATE INDEX IF NOT EXISTS idx_sections_parent ON sections(parent_section_id);

-- projects organizes knowledge items into groups
CREATE TABLE IF NOT EXISTS projects (
    project_id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    tags TEXT,  -- JSON array
    archived BOOLEAN DEFAULT FALSE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- project_links provides many-to-many relationship between projects and knowledge items
CREATE TABLE IF NOT EXISTS project_links (
    link_id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(project_id) ON DELETE CASCADE,
    content_id TEXT NOT NULL REFERENCES knowledge_items(content_id) ON DELETE CASCADE,
    local_title TEXT,
    local_notes TEXT,
    pin_state BOOLEAN DEFAULT FALSE,
    local_tags TEXT,  -- JSON array
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(project_id, content_id)
);

CREATE INDEX IF NOT EXISTS idx_project_links_project_id ON project_links(project_id);
CREATE INDEX IF NOT EXISTS idx_project_links_content_id ON project_links(content_id);

-- summaries stores AI-generated summaries
CREATE TABLE IF NOT EXISTS summaries (
    summary_id TEXT PRIMARY KEY,
    source_ref TEXT NOT NULL,  -- content_id ou thread_id
    model_used TEXT NOT NULL,
    decisions TEXT,  -- JSON array
    actions TEXT,  -- JSON array
    open_questions TEXT,  -- JSON array
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- memories stores persistent conversation memories
CREATE TABLE IF NOT EXISTS memories (
    memory_id TEXT PRIMARY KEY,
    type TEXT NOT NULL,  -- fact, action, preference
    content TEXT NOT NULL,
    context_id TEXT,  -- conversation ID
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME,
    score FLOAT,  -- relevance score for search
    -- Phase 3.3: long-term chat memory.
    -- 'manual' = user-pinned (auto-accepted); 'extracted' = LLM-distilled candidate (must be accepted).
    source TEXT NOT NULL DEFAULT 'manual',
    -- NULL = pending user review. Set to RFC3339 timestamp on accept.
    accepted_at DATETIME,
    -- Cosine-search embedding (little-endian float32 BLOB) for top-K retrieval.
    embedding BLOB,
    -- Session that produced this memory; useful to trace back the conversation.
    session_id TEXT
);

CREATE INDEX IF NOT EXISTS idx_memories_type ON memories(type);
CREATE INDEX IF NOT EXISTS idx_memories_context_id ON memories(context_id);
CREATE INDEX IF NOT EXISTS idx_memories_source ON memories(source);
CREATE INDEX IF NOT EXISTS idx_memories_accepted_at ON memories(accepted_at);

-- schema_version tracks applied migrations

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

-- Phase 1 (pair mode): interaction_log captures every notable user action so
-- downstream phases (weekly recap slot detection, learning-progress coverage,
-- adaptive ranking signals) can reason about behaviour over time. Append-only
-- by convention; no UPDATE paths exist in the codebase.
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

-- chat_sessions persists conversations so the user can reopen prior exchanges.
-- Unlike the in-memory session.Store (entities/topic cache, TTL-evicted), this
-- is the durable transcript. project_id optionally scopes a conversation to a
-- project for the global "unification" (chat ↔ project association).
CREATE TABLE IF NOT EXISTS chat_sessions (
    session_id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    project_id TEXT REFERENCES projects(project_id) ON DELETE SET NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_chat_sessions_updated_at ON chat_sessions(updated_at);
CREATE INDEX IF NOT EXISTS idx_chat_sessions_project_id ON chat_sessions(project_id);

-- chat_messages stores each turn (user + assistant). The sources column holds
-- the JSON RAGSource array cited by an assistant turn so the transcript can be
-- rehydrated with its citations. ordinal preserves turn order within a session.
CREATE TABLE IF NOT EXISTS chat_messages (
    message_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES chat_sessions(session_id) ON DELETE CASCADE,
    role TEXT NOT NULL,           -- user | assistant
    content TEXT NOT NULL,
    sources TEXT,                 -- JSON array of RAGSource (assistant turns only)
    ordinal INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_chat_messages_session ON chat_messages(session_id, ordinal);

-- chat_message_attachments stores the media (image / audio) sent with a user
-- turn so a reopened conversation can re-display the image and replay the
-- audio. Bytes are stored raw (BLOB); data is NULL when an audio recording has
-- been purged by the retention cap, in which case the row is kept so the UI can
-- show a "recording no longer available" placeholder instead of a broken player.
CREATE TABLE IF NOT EXISTS chat_message_attachments (
    message_id TEXT NOT NULL REFERENCES chat_messages(message_id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL DEFAULT 0, -- order within the message
    type TEXT NOT NULL,                 -- image | audio
    title TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT '', -- image MIME (e.g. image/png)
    format TEXT NOT NULL DEFAULT '',    -- audio format (e.g. wav, mp3)
    data BLOB,                          -- raw bytes; NULL once purged by the cap
    byte_size INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (message_id, ordinal)
);

CREATE INDEX IF NOT EXISTS idx_chat_attachments_type ON chat_message_attachments(type, created_at);

-- tasks is a lightweight local to-do list, optionally linked to a project and
-- to the knowledge item a task was created from.
CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',     -- open | done
    due_date TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    source_content_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status, created_at);
CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project_id);
`

// CurrentSchemaVersion is the current schema version number.
const CurrentSchemaVersion = 16
