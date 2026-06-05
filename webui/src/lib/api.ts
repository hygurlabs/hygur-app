import { fetchEventSource } from "@microsoft/fetch-event-source";
import { apiBase, apiKey } from "./connection";
import type {
  AgendaAction,
  Briefing,
  ChatMessage,
  Connector,
  ConnectorConfigValue,
  ConnectorDetail,
  ConnectorInstance,
  EncryptionStatus,
  FocusScope,
  KnowledgeItem,
  MarketplaceItem,
  Mention,
  Note,
  Project,
  ProjectItem,
  RagSource,
  SearchResponse,
  SessionDetail,
  SessionSummary,
  SidecarConfig,
  SidecarConfigPatch,
  Tag,
  TokenPricing,
  TokenUsageResponse,
} from "./types";

/** API contract version this client speaks. The server advertises its own via
 *  the X-Hygur-API response header and refuses clients older than its minimum
 *  with HTTP 426 (see internal/version.APIVersion). Bump in lock-step with the
 *  Go constant on a breaking contract change. */
export const API_VERSION = "1";

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

async function getJSON<T>(path: string): Promise<T> {
  const r = await fetch(u(path), { headers: authHeaders() });
  if (!r.ok) throw httpError(r);
  return (await r.json()) as T;
}

async function sendJSON<T>(
  method: "POST" | "PUT",
  path: string,
  body: unknown,
): Promise<T> {
  const r = await fetch(u(path), {
    method,
    headers: authHeaders({ "Content-Type": "application/json" }),
    body: JSON.stringify(body),
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

async function del(path: string): Promise<void> {
  const r = await fetch(u(path), { method: "DELETE", headers: authHeaders() });
  if (!r.ok) throw httpError(r);
}

async function patchJSON(path: string, body: unknown): Promise<void> {
  const r = await fetch(u(path), {
    method: "PATCH",
    headers: authHeaders({ "Content-Type": "application/json" }),
    body: JSON.stringify(body),
  });
  if (!r.ok) throw httpError(r);
}

/** Percent-encodes a content_id for use in a path WITHOUT escaping ':' — the
 *  sidecar's chi router reads the raw param and content_ids like `mail:acct:123`
 *  or `brief:2026-06-01` are stored with literal colons, so escaping them 404s. */
function cidPath(id: string): string {
  return encodeURIComponent(id).replace(/%3A/g, ":");
}

export const api = {
  health: () => fetch(u("/health")).then((r) => r.json()),
  search: (query: string, topK = 15) =>
    postJSON<SearchResponse>("/search", { query, top_k: topK }),
  knowledgeItems: (limit = 200, sourceType?: string) =>
    getJSON<{ items: KnowledgeItem[] }>(
      `/knowledge/items?limit=${limit}&offset=0${
        sourceType ? `&source_type=${encodeURIComponent(sourceType)}` : ""
      }`,
    ),
  knowledgeItem: (contentId: string) =>
    getJSON<KnowledgeItem>(`/knowledge/${cidPath(contentId)}`),

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

  // @-mention autocomplete (projects + notes/mails/docs).
  mentions: (q: string) =>
    getJSON<{ mentions: Mention[] }>(`/mentions?q=${encodeURIComponent(q)}`),

  // Briefings (daily + meeting).
  briefings: () => getJSON<{ briefings: Briefing[] }>("/briefings"),
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
  setTokenPricing: (p: TokenPricing) =>
    putJSON<{ status: string }>("/usage/pricing", p),

  // DB backup / restore. Backup streams a consistent snapshot the browser saves
  // wherever you choose; restore uploads a snapshot, staged and applied on the
  // next app restart.
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
  onDone?: () => void;
}

export interface ChatOptions {
  focusScope?: FocusScope;
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
