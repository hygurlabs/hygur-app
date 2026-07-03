import { fetchEventSource } from "@microsoft/fetch-event-source";
import { apiBase, apiKey, CONSOLE_URL, localToken, refreshAccessToken } from "./connection";
import type {
  AgendaAction,
  Briefing,
  ChatMessage,
  Conflict,
  Engram,
  SubjectStat,
  Connector,
  ConnectorConfigValue,
  ConnectorDetail,
  ConnectorInstance,
  DeterminedAnswer,
  EncryptionStatus,
  FocusScope,
  FollowUpDigest,
  KnowledgeItem,
  LearningProgress,
  MarketplaceItem,
  Memory,
  MemoryWrite,
  Mention,
  Note,
  PendingAction,
  Project,
  ProjectItem,
  RagSource,
  ReconciledConflict,
  SearchResponse,
  SessionDetail,
  SessionSummary,
  SidecarConfig,
  ChronicleChapterSummary,
  ChronicleChapterDetail,
  SidecarConfigPatch,
  Decision,
  Digest,
  Tag,
  TagItem,
  Task,
  TimelineItem,
  TokenPricing,
  TokenUsageResponse,
} from "./types";

/** API contract version this client speaks. The server advertises its own via
 *  the X-Hygur-API response header and refuses clients older than its minimum
 *  with HTTP 426 (see internal/version.APIVersion). Bump in lock-step with the
 *  Go constant on a breaking contract change. */
export const API_VERSION = "1";

/** Subscription status for the Settings "Billing" panel (from the control plane). */
export interface BillingStatus {
  status: string; // trialing | active | past_due | canceled
  active: boolean;
  valid_until?: string;
  portal_url?: string;
}

/** Last edge-sync summary (cloud desktop thin client). Drives the Proton card's
 *  green dot + last-synced/error display. */
export interface EdgeStatus {
  running: boolean;
  last_sync_at?: string;
  files_pushed: number;
  mail_pushed: number;
  errors: number;
  last_error?: string;
}

/** Prepends the configured API base ("" = same-origin local sidecar, or a
 *  remote endpoint like https://app.hygur.eu). Resolved per call so a connection
 *  change takes effect without reloading the module. */
const u = (path: string): string => apiBase() + path;

function authHeaders(extra?: Record<string, string>): Record<string, string> {
  return { "X-Hygur-Token": apiKey(), "X-Hygur-API": API_VERSION, ...(extra ?? {}) };
}

/** Maps a non-OK response to an Error, with a friendly message for the
 *  version-skew case so the UI can prompt an update instead of a raw "HTTP 426". */
function httpError(r: Response): Error {
  if (r.status === 426) {
    return new Error("This version of Hygur is too old for the server — please update the app.");
  }
  return new Error(`HTTP ${r.status}`);
}

/** fetch + auth headers, with a single transparent refresh-and-retry on 401 so a
 *  short-lived cloud access token expiring mid-session is renewed via the refresh
 *  token without surfacing to the UI. Headers are rebuilt per attempt to pick up
 *  the rotated key. */
async function fetchAuthed(
  path: string,
  init: RequestInit = {},
  extra?: Record<string, string>,
): Promise<Response> {
  const build = (): RequestInit => ({ ...init, headers: authHeaders(extra) });
  let r = await fetch(u(path), build());
  if (r.status === 401 && (await refreshAccessToken())) {
    r = await fetch(u(path), build());
  }
  return r;
}

async function getJSON<T>(path: string): Promise<T> {
  const r = await fetchAuthed(path);
  if (!r.ok) throw httpError(r);
  return (await r.json()) as T;
}

async function sendJSON<T>(
  method: "POST" | "PUT",
  path: string,
  body: unknown,
): Promise<T> {
  const r = await fetchAuthed(path, { method, body: JSON.stringify(body) }, {
    "Content-Type": "application/json",
  });
  if (!r.ok) throw httpError(r);
  // Some endpoints (PUT) may return an empty body.
  const text = await r.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

const postJSON = <T>(path: string, body: unknown) =>
  sendJSON<T>("POST", path, body);
const putJSON = <T>(path: string, body: unknown) =>
  sendJSON<T>("PUT", path, body);

/** Mint a one-time code to connect another device (e.g. a phone). Calls the
 *  console with the current access token; returns the code + instance slug so
 *  the client can build a QR deep link. Cloud/managed only. */
export async function linkDeviceCode(): Promise<{ code: string; slug: string }> {
  const call = () =>
    fetch(`${CONSOLE_URL}/device/link-code`, {
      method: "POST",
      headers: { Authorization: `Bearer ${apiKey()}` },
    });
  let r = await call();
  if (r.status === 401 && (await refreshAccessToken())) r = await call();
  if (!r.ok) throw httpError(r);
  return (await r.json()) as { code: string; slug: string };
}

async function del(path: string): Promise<void> {
  const r = await fetchAuthed(path, { method: "DELETE" });
  if (!r.ok) throw httpError(r);
}

async function patchJSON(path: string, body: unknown): Promise<void> {
  const r = await fetchAuthed(path, { method: "PATCH", body: JSON.stringify(body) }, {
    "Content-Type": "application/json",
  });
  if (!r.ok) throw httpError(r);
}

// Edge routes (/edge/*) are served by the LOCAL sidecar that served this page —
// never the remote tenant. They MUST bypass apiBase() (which may point at the
// cloud tenant) and the remote key: use a same-origin relative URL + the LOCAL
// loopback token. (Regression: with a remote endpoint configured, /edge went to
// the tenant pod → 404 → the desktop Proton card hid.)
function edgeHeaders(): Record<string, string> {
  return { "X-Hygur-Token": localToken(), "X-Hygur-API": API_VERSION };
}
async function edgeGet<T>(path: string): Promise<T> {
  const r = await fetch(path, { headers: edgeHeaders() });
  if (!r.ok) throw httpError(r);
  return (await r.json()) as T;
}
async function edgePost(path: string): Promise<void> {
  const r = await fetch(path, { method: "POST", headers: edgeHeaders() });
  if (!r.ok) throw httpError(r);
}

/** Percent-encodes a content_id for use in a path WITHOUT escaping ':' — the
 *  sidecar's chi router reads the raw param and content_ids like `mail:acct:123`
 *  or `brief:2026-06-01` are stored with literal colons, so escaping them 404s. */
function cidPath(id: string): string {
  return encodeURIComponent(id).replace(/%3A/g, ":");
}

export const api = {
  // Bounded so a stale socket (laptop sleep + network change) fails fast instead
  // of hanging on the TCP timeout for minutes — the health poll then recovers on
  // its next tick rather than showing "offline" long after connectivity is back.
  health: () => {
    const ctrl = new AbortController();
    const t = setTimeout(() => ctrl.abort(), 5000);
    return fetch(u("/health"), { signal: ctrl.signal, cache: "no-store" })
      .then((r) => r.json())
      .finally(() => clearTimeout(t));
  },
  search: (query: string, topK = 15) =>
    postJSON<SearchResponse>("/search", { query, top_k: topK }),
  /** Confirms a gated side-effect action (WP3): executes the withheld tool
   *  server-side. Throws on an expired/unknown action (410) or execution error. */
  confirmAction: async (actionId: string): Promise<void> => {
    const r = await fetchAuthed(
      `/actions/${encodeURIComponent(actionId)}/confirm`,
      { method: "POST" },
    );
    if (!r.ok) throw httpError(r);
  },
  knowledgeItems: (limit = 200, sourceType?: string, exclude?: string[]) =>
    getJSON<{ items: KnowledgeItem[] }>(
      `/knowledge/items?limit=${limit}&offset=0${
        sourceType ? `&source_type=${encodeURIComponent(sourceType)}` : ""
      }${
        exclude && exclude.length
          ? `&exclude_source_type=${encodeURIComponent(exclude.join(","))}`
          : ""
      }`,
    ),
  /** Just the count for a source type (the list response carries `total`). */
  knowledgeCount: (sourceType: string) =>
    getJSON<{ total: number }>(
      `/knowledge/items?limit=1&offset=0&source_type=${encodeURIComponent(sourceType)}`,
    ),
  /** Total indexed items across all sources — drives the first-run state. */
  knowledgeTotal: () =>
    getJSON<{ total: number }>(`/knowledge/items?limit=1&offset=0`),
  knowledgeItem: (contentId: string) =>
    getJSON<KnowledgeItem>(`/knowledge/${cidPath(contentId)}`),
  /** Deterministic contradictions across mail threads (W5, low-level). */
  contradictions: () =>
    getJSON<{ conflicts: Conflict[]; scanned: number }>(
      "/knowledge/contradictions",
    ),
  /** Grounded LLM follow-up digest: topics + real contradictions, cited.
   *  Scoped to a project when projectId is given (W7). */
  followup: (projectId?: string) =>
    getJSON<FollowUpDigest>(
      `/knowledge/followup${projectId ? `?project_id=${encodeURIComponent(projectId)}` : ""}`,
    ),
  /** W6 reconciled semantic contradictions (conflict/supersedes), cited. Scoped to
   *  a project when given; cached ~1h server-side. include_dismissed returns the
   *  ones the user hid, flagged, for the manage view (default hides them). */
  claimContradictions: (projectId?: string, includeDismissed?: boolean) => {
    const qs = new URLSearchParams();
    if (projectId) qs.set("project_id", projectId);
    if (includeDismissed) qs.set("include_dismissed", "1");
    const q = qs.toString();
    return getJSON<{ contradictions: ReconciledConflict[]; scanned: number }>(
      `/knowledge/claim-contradictions${q ? `?${q}` : ""}`,
    );
  },
  /** Dismiss a contradiction by its stable key (undo=true restores it). */
  dismissContradiction: (key: string, undo = false) =>
    postJSON<void>("/knowledge/contradictions/dismiss", { key, undo }),
  /** A project's items as a date-sorted exchange timeline (W7). */
  projectTimeline: (projectId: string) =>
    getJSON<{ items: TimelineItem[] }>(
      `/knowledge/project-timeline?project_id=${encodeURIComponent(projectId)}`,
    ),
  /** On-demand grounded reply draft for a mail item (W7). Not cached. */
  draftReply: (contentId: string) =>
    postJSON<{ draft: string }>(
      `/knowledge/${cidPath(contentId)}/draft-reply`,
      {},
    ),

  // Tasks — local to-do list (W7).
  tasks: (projectId?: string, status?: string) => {
    const qs = new URLSearchParams();
    if (projectId) qs.set("project_id", projectId);
    if (status) qs.set("status", status);
    const q = qs.toString();
    return getJSON<{ tasks: Task[] }>(`/tasks${q ? `?${q}` : ""}`);
  },
  // Long-term memory — what Hygur remembers about you (facts/actions/preferences).
  memories: () => getJSON<{ memories: Memory[]; total: number }>("/memory/list"),
  pendingMemories: () => getJSON<{ memories: Memory[]; total: number }>("/memory/pending"),
  storeMemory: (body: { type: string; content: string }) =>
    postJSON<Memory>("/memory/store", body),
  acceptMemory: (id: string) => postJSON<void>(`/memory/${encodeURIComponent(id)}/accept`, {}),
  discardMemory: (id: string) => postJSON<void>(`/memory/${encodeURIComponent(id)}/discard`, {}),
  deleteMemory: (id: string) => del(`/memory/${encodeURIComponent(id)}`),

  // Learning gauge — "how well Hygur knows you" (Mind hub header).
  learningProgress: () => getJSON<LearningProgress>("/insights/learning-progress"),

  // Chronicle — Hygur's grounded narrative (read as a book).
  chronicle: () => getJSON<{ chapters: ChronicleChapterSummary[] }>("/chronicle"),
  chronicleChapter: (id: string) =>
    getJSON<ChronicleChapterDetail>(`/chronicle/${encodeURIComponent(id)}`),
  chronicleRun: () => postJSON<{ started: boolean }>("/chronicle/run", {}),
  closeChronicleChapter: (id: string, note: string) =>
    postJSON<{ started: boolean }>(`/chronicle/${encodeURIComponent(id)}/close`, { note }),
  reopenChronicleChapter: (id: string, note: string) =>
    postJSON<{ reopened: boolean }>(`/chronicle/${encodeURIComponent(id)}/reopen`, { note }),

  task: (id: string) => getJSON<Task>(`/tasks/${id}`),
  createTask: (body: {
    title: string;
    body?: string;
    status?: string;
    due_date?: string;
    project_id?: string;
    tag_ids?: string[];
  }) => postJSON<Task>("/tasks", body),
  updateTask: (
    id: string,
    patch: {
      title?: string;
      body?: string;
      status?: string;
      due_date?: string;
      project_id?: string;
      tag_ids?: string[];
    },
  ) => patchJSON(`/tasks/${id}`, patch),
  deleteTask: (id: string) => del(`/tasks/${id}`),

  // Decisions — the user's decisions/commitments, logged or proposed by the scan.
  decisions: (projectId?: string, status?: string) => {
    const qs = new URLSearchParams();
    if (projectId) qs.set("project_id", projectId);
    if (status) qs.set("status", status);
    const q = qs.toString();
    return getJSON<{ decisions: Decision[] }>(`/decisions${q ? `?${q}` : ""}`);
  },
  createDecision: (body: {
    statement: string;
    rationale?: string;
    decided_on?: string;
    source_ref?: string;
    project_id?: string;
    tag_ids?: string[];
  }) => postJSON<Decision>("/decisions", body),
  updateDecision: (
    id: string,
    patch: {
      statement?: string;
      rationale?: string;
      status?: string;
      decided_on?: string;
      project_id?: string;
      tag_ids?: string[];
    },
  ) => patchJSON(`/decisions/${cidPath(id)}`, patch),
  deleteDecision: (id: string) => del(`/decisions/${cidPath(id)}`),
  scanDecisions: () => postJSON<{ started: boolean }>("/decisions/scan", {}),

  // Daily composed digest — the "state of your world" surface.
  digest: () => getJSON<Digest>("/digest"),

  // Notes — full CRUD.
  notes: () => getJSON<{ notes: Note[] }>("/notes"),
  createNote: (title: string, content: string) =>
    postJSON<Note>("/notes", { title, content }),
  updateNote: (
    id: string,
    patch: {
      title?: string;
      content?: string;
      project_id?: string;
      tag_ids?: string[];
    },
  ) => putJSON<Note>(`/notes/${id}`, patch),
  deleteNote: (id: string) => del(`/notes/${id}`),

  tags: () => getJSON<{ tags: Tag[] }>("/tags"),
  /** Items carrying a tag — drives the tag → items drill-down. */
  tagItems: (id: string) =>
    getJSON<{ tag_id: string; items: TagItem[] }>(`/tags/${encodeURIComponent(id)}/items`),
  createTag: (name: string) => postJSON<Tag>("/tags", { name }),
  deleteTag: (id: string) => del(`/tags/${id}`),
  /** Maps tag NAMES to ids, creating any that don't exist yet. */
  resolveTagIds: async (names: string[], existing: Tag[]): Promise<string[]> => {
    const byName = new Map(existing.map((t) => [t.name.toLowerCase(), t.id]));
    const ids: string[] = [];
    for (const name of names) {
      const found = byName.get(name.toLowerCase());
      if (found) {
        ids.push(found);
        continue;
      }
      try {
        const created = await postJSON<Tag>("/tags", { name });
        byName.set(name.toLowerCase(), created.id);
        ids.push(created.id);
      } catch {
        /* skip a tag we couldn't create rather than failing the whole save */
      }
    }
    return ids;
  },
  addItemTag: (contentId: string, tagId: string) =>
    postJSON<unknown>(`/knowledge/${cidPath(contentId)}/tags`, { tag_id: tagId }),
  removeItemTag: (contentId: string, tagId: string) =>
    del(`/knowledge/${cidPath(contentId)}/tags/${tagId}`),
  agenda: (rangeHours = 336) =>
    getJSON<{ actions: AgendaAction[]; generated_at: string }>(
      `/agenda/context?range_hours=${rangeHours}`,
    ),
  calendarSummary: () =>
    getJSON<{ summary: string; window: string; count: number }>(
      `/agenda/calendar-summary`,
    ),
  /** Calendar events by date window, ordered by date (not ingestion time). */
  agendaEvents: (fromISO: string, toISO: string) =>
    getJSON<{ events: KnowledgeItem[] }>(
      `/agenda/events?from=${encodeURIComponent(fromISO)}&to=${encodeURIComponent(toISO)}`,
    ),

  // Persistent chat sessions.
  sessions: () => getJSON<{ sessions: SessionSummary[] }>("/sessions"),
  session: (id: string) => getJSON<SessionDetail>(`/sessions/${id}`),
  updateSession: (id: string, patch: { title?: string; project_id?: string }) =>
    putJSON<SessionDetail>(`/sessions/${id}`, patch),
  deleteSession: (id: string) => del(`/sessions/${id}`),

  // Projects.
  projects: () => getJSON<Project[]>("/projects"),
  createProject: (name: string, description?: string) =>
    postJSON<Project>("/projects", { name, description }),
  updateProject: (
    id: string,
    patch: { name?: string; description?: string; archived?: boolean; tags?: string[] },
  ) => putJSON<Project>(`/projects/${id}`, patch),
  deleteProject: (id: string) => del(`/projects/${id}`),
  projectItems: (id: string) =>
    getJSON<{ project_id: string; items: ProjectItem[] }>(
      `/projects/${id}/items`,
    ),
  linkItemToProject: (contentId: string, projectId: string) =>
    postJSON<unknown>(`/knowledge/${cidPath(contentId)}/project`, {
      project_id: projectId,
    }),
  unlinkItemFromProject: (contentId: string) =>
    del(`/knowledge/${cidPath(contentId)}/project`),
  dismissProjectSuggestion: (contentId: string) =>
    del(`/knowledge/${cidPath(contentId)}/project-suggestion`),

  // @-mention autocomplete (projects + notes/mails/docs).
  mentions: (q: string) =>
    getJSON<{ mentions: Mention[] }>(`/mentions?q=${encodeURIComponent(q)}`),

  // Briefings (daily + meeting).
  briefings: () => getJSON<{ briefings: Briefing[] }>("/briefings"),
  /** Discovered subjects (named entities) ranked by centrality (Engram index). */
  engrams: (limit = 200) =>
    getJSON<{ subjects: SubjectStat[] }>(`/engrams?limit=${limit}`),
  /** A subject's consolidated Engram dossier: network + timeline + live/dead. */
  engram: (norm: string) => getJSON<Engram>(`/engrams/${encodeURIComponent(norm)}`),
  /** Triggers an on-demand brief (async, lands via SSE + the list refetch). */
  runBrief: (body: {
    project_ids?: string[];
    content_ids?: string[];
    instructions?: string;
  }) => postJSON<{ status: string }>("/brief/run", body),

  // Connectors + marketplace.
  connectors: () => getJSON<Connector[]>("/connectors"),
  enableConnector: (id: string) => postJSON<unknown>(`/connectors/${id}/enable`, {}),
  disableConnector: (id: string) =>
    postJSON<unknown>(`/connectors/${id}/disable`, {}),
  syncConnector: (id: string) =>
    postJSON<unknown>(`/connectors/${id}/sync?async=true`, {}),
  marketplace: () => getJSON<MarketplaceItem[]>("/marketplace/connectors"),
  installConnector: (typeId: string) =>
    postJSON<unknown>(`/marketplace/install/${typeId}`, {}),

  // Connector configuration (schema-driven form).
  connectorDetail: (id: string) => getJSON<ConnectorDetail>(`/connectors/${id}`),
  configureConnector: (id: string, cfg: ConnectorConfigValue) =>
    putJSON<unknown>(`/connectors/${id}/config`, cfg),
  saveConnectorCredentials: (id: string, fields: Record<string, string>) =>
    putJSON<unknown>(`/connectors/${id}/credentials`, fields),
  connectorAuthUrl: (id: string) =>
    getJSON<{ url: string }>(`/connectors/${id}/auth/url`),
  connectorAuthCallback: (id: string, code: string) =>
    postJSON<unknown>(`/connectors/${id}/auth/callback`, { code }),
  connectorMailboxes: (id: string) =>
    getJSON<string[]>(`/connectors/${id}/mailboxes`),
  connectorLabels: (id: string) =>
    getJSON<{ id: string; name: string }[]>(`/connectors/${id}/labels`),

  // Edge thin-client (cloud desktop): on-device Proton sync. Served locally by
  // the sidecar (never proxied to the tenant). 503 when not a cloud thin client.
  edgeStatus: () => edgeGet<EdgeStatus>("/edge/status"),
  edgeMailboxes: () => edgeGet<{ mailboxes: string[] }>("/edge/proton/mailboxes"),
  edgeSync: () => edgePost("/edge/sync"),

  // Billing status from the control plane (cross-origin to console, device token).
  // Refresh-on-401 like the other authed console calls, so an expired access
  // token doesn't spuriously hide the panel (mirrors linkDeviceCode / fetchAuthed).
  billingStatus: async (): Promise<BillingStatus> => {
    const call = () =>
      fetch(`${CONSOLE_URL}/billing/status`, {
        headers: { "X-Hygur-Token": apiKey(), "X-Hygur-API": API_VERSION },
      });
    let r = await call();
    if (r.status === 401 && (await refreshAccessToken())) r = await call();
    if (!r.ok) throw httpError(r);
    return (await r.json()) as BillingStatus;
  },

  // Multi-instance connectors ("+").
  connectorInstances: () =>
    getJSON<ConnectorInstance[]>("/connectors/instances"),
  createConnectorInstance: (
    typeId: string,
    body: { id: string; display_name: string; settings?: Record<string, string>; schedule?: string; enabled?: boolean },
  ) => postJSON<unknown>(`/connectors/${typeId}/instances`, body),
  deleteConnectorInstance: (instanceId: string) =>
    del(`/connectors/instances/${instanceId}`),

  // Sidecar config.
  config: () => getJSON<SidecarConfig>("/config"),
  patchConfig: (patch: SidecarConfigPatch) => patchJSON("/config", patch),

  // Token usage + cost pricing.
  getTokenUsage: () => getJSON<TokenUsageResponse>("/usage/tokens"),

  // Web Push (browser notifications). subscribePush takes a PushSubscription JSON.
  subscribePush: (sub: unknown) => postJSON<void>("/push/subscribe", sub),
  unsubscribePush: (endpoint: string) => postJSON<void>("/push/unsubscribe", { endpoint }),
  testPush: () => postJSON<{ sent: number }>("/push/test", {}),
  setTokenPricing: (p: TokenPricing) =>
    putJSON<{ status: string }>("/usage/pricing", p),

  // DB backup / restore. Locally the sidecar writes the snapshot to ~/Downloads
  // (the webview can't trigger a browser download); remotely the browser
  // streams + saves it. Restore uploads a snapshot, applied on the next restart.
  saveBackupLocal: () =>
    postJSON<{ status: string; path: string; encrypted: boolean }>(
      "/admin/db/backup/save",
      {},
    ),
  downloadBackup: async (): Promise<void> => {
    const r = await fetch(u("/admin/db/backup"), { headers: authHeaders() });
    if (!r.ok) throw httpError(r);
    const blob = await r.blob();
    const cd = r.headers.get("Content-Disposition") ?? "";
    const name = /filename="?([^"]+)"?/.exec(cd)?.[1] ?? "hygur-backup.db";
    const href = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = href;
    a.download = name;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(href);
  },
  /** Streams a passphrase-encrypted export (notes + briefs) and saves it via the
   *  browser. The passphrase encrypts the archive server-side and is never stored;
   *  decrypt with `openssl enc -d -aes-256-cbc -pbkdf2`. */
  exportData: async (passphrase: string): Promise<void> => {
    const r = await fetchAuthed(
      "/admin/export",
      { method: "POST", body: JSON.stringify({ passphrase }) },
      { "Content-Type": "application/json" },
    );
    if (!r.ok) throw httpError(r);
    const blob = await r.blob();
    const cd = r.headers.get("Content-Disposition") ?? "";
    const name = /filename="?([^"]+)"?/.exec(cd)?.[1] ?? "hygur-export.zip.enc";
    const href = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = href;
    a.download = name;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(href);
  },
  restoreBackup: async (
    file: File,
  ): Promise<{ status: string; restart_required: boolean }> => {
    const form = new FormData();
    form.append("file", file);
    const r = await fetch(u("/admin/db/restore"), {
      method: "POST",
      headers: authHeaders(), // no Content-Type — the browser sets the boundary
      body: form,
    });
    if (!r.ok) throw httpError(r);
    return r.json();
  },

  // Local at-rest encryption (opt-in; key stored in the OS keychain).
  getEncryptionStatus: () => getJSON<EncryptionStatus>("/admin/db/encryption"),
  enableEncryption: () =>
    postJSON<{ status: string; restart_required: boolean }>("/admin/db/encrypt", {}),

  /** Uploads + ingests a file (📎). Returns the new content_id to attach. */
  uploadFile: async (
    file: File,
    projectId?: string,
  ): Promise<{ content_id: string; status: string; title: string }> => {
    const form = new FormData();
    form.append("file", file);
    if (projectId) form.append("project_id", projectId);
    const r = await fetch(u("/knowledge/upload"), {
      method: "POST",
      headers: authHeaders(), // no Content-Type — the browser sets the boundary
      body: form,
    });
    if (!r.ok) throw httpError(r);
    return r.json();
  },
};

export interface ChatHandlers {
  onSources?: (sources: RagSource[]) => void;
  onDelta?: (delta: string) => void;
  onTool?: (name: string) => void;
  onError?: (message: string) => void;
  /** Fires when the turn autonomously wrote a memory (once per write), so the UI
   *  can surface it inline instead of leaving it buried in the review queue. */
  onMemoryWrite?: (write: MemoryWrite) => void;
  /** Fires when the LLM requested a side-effecting action (e.g. create a note)
   *  that was withheld pending the user's confirmation (WP3). The UI renders a
   *  Confirm/Cancel card; Confirm calls confirmAction(action_id). */
  onPendingAction?: (action: PendingAction) => void;
  /** Fires when the deterministic engine produced a factual-identifier answer (the
   *  `determined_answer` SSE event). The UI renders the value authoritatively so the LLM's prose
   *  can't substitute, hedge, or decline it — cut-LLM-safe. */
  onDeterminedAnswer?: (answer: DeterminedAnswer) => void;
  /** degraded=true when the inference backend was down and only retrieved sources are shown. */
  onDone?: (degraded?: boolean) => void;
}

export interface ChatOptions {
  focusScope?: FocusScope;
}

/** onopen guard for SSE streams: if the server answered with an error (usually a JSON
 *  body — quota reached, auth, 5xx) instead of the event stream, surface its message
 *  cleanly instead of the opaque "Expected content-type text/event-stream" throw. */
async function sseOnOpen(response: Response): Promise<void> {
  const ct = response.headers.get("content-type") || "";
  if (response.ok && ct.includes("text/event-stream")) return;
  let msg = `Request failed (${response.status}).`;
  try {
    const b = await response.json();
    msg = b?.error?.message || b?.error || b?.message || msg;
  } catch {
    /* non-JSON body — keep the status message */
  }
  throw new Error(msg); // onerror surfaces this + disables retry
}

/** Streams the grounded Follow-up report (3-paragraph prose) over SSE so the UI
 *  can render it as it's written. One-shot: throwing in onerror disables retry. */
export async function streamFollowupReport(
  handlers: {
    onDelta?: (delta: string) => void;
    onDone?: () => void;
    onError?: (msg: string) => void;
  },
  signal: AbortSignal,
  projectId?: string,
): Promise<void> {
  const path =
    "/knowledge/followup/report" +
    (projectId ? `?project_id=${encodeURIComponent(projectId)}` : "");
  await fetchEventSource(u(path), {
    method: "GET",
    headers: authHeaders(),
    signal,
    openWhenHidden: true,
    onopen: sseOnOpen,
    onmessage(msg) {
      const data = msg.data;
      if (!data) return;
      let evt: Record<string, unknown>;
      try {
        evt = JSON.parse(data);
      } catch {
        return;
      }
      if (typeof evt.error === "string") {
        handlers.onError?.(evt.error);
        return;
      }
      if (typeof evt.delta === "string" && evt.delta) {
        handlers.onDelta?.(evt.delta);
      }
      if (evt.done === true) {
        handlers.onDone?.();
      }
    },
    onerror(err) {
      handlers.onError?.(err instanceof Error ? err.message : String(err));
      throw err;
    },
  });
}

/** Streams a RAG chat turn over SSE. EventSource can't POST with headers, so
 *  we use fetch-event-source. Throwing in onerror disables its auto-retry —
 *  a chat turn is one-shot, not a long-lived subscription. */
export async function streamChat(
  messages: ChatMessage[],
  sessionId: string,
  handlers: ChatHandlers,
  signal: AbortSignal,
  opts?: ChatOptions,
): Promise<void> {
  await fetchEventSource(u("/chat"), {
    method: "POST",
    headers: authHeaders({ "Content-Type": "application/json" }),
    body: JSON.stringify({
      messages,
      stream: true,
      rag_enabled: true,
      session_id: sessionId,
      ...(opts?.focusScope ? { focus_scope: opts.focusScope } : {}),
    }),
    signal,
    openWhenHidden: true,
    onopen: sseOnOpen,
    onmessage(msg) {
      const data = msg.data;
      if (!data) return;
      if (data === "[DONE]") {
        handlers.onDone?.();
        return;
      }
      let evt: Record<string, unknown>;
      try {
        evt = JSON.parse(data);
      } catch {
        return;
      }
      if (evt.error) {
        const e = evt.error;
        handlers.onError?.(
          typeof e === "string"
            ? e
            : ((e as { message?: string })?.message ?? "LLM error"),
        );
      }
      if (evt.type === "rag_context" && Array.isArray(evt.sources)) {
        handlers.onSources?.(evt.sources as RagSource[]);
      }
      if (evt.type === "tool_call") {
        handlers.onTool?.((evt.name as string) ?? "");
      }
      if (evt.type === "memory_write" && typeof evt.memory_id === "string") {
        handlers.onMemoryWrite?.(evt as unknown as MemoryWrite);
      }
      if (evt.type === "pending_action" && typeof evt.action_id === "string") {
        handlers.onPendingAction?.({
          action_id: evt.action_id as string,
          tool: (evt.tool as string) ?? "",
          preview: (evt.preview as string) ?? "",
        });
      }
      if (evt.type === "determined_answer") {
        handlers.onDeterminedAnswer?.({
          label: evt.label as string | undefined,
          subject: evt.subject as string | undefined,
          value: evt.value as string | undefined,
          confidence: (evt.confidence as DeterminedAnswer["confidence"]) ?? "none",
          message: evt.message as string | undefined,
          sources: evt.sources as DeterminedAnswer["sources"],
        });
      }
      if (typeof evt.delta === "string" && evt.delta) {
        handlers.onDelta?.(evt.delta);
      }
      if (evt.done === true) {
        handlers.onDone?.(evt.degraded === true);
      }
    },
    onerror(err) {
      handlers.onError?.(err instanceof Error ? err.message : String(err));
      throw err;
    },
  });
}
