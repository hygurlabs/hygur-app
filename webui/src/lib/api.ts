import { fetchEventSource } from "@microsoft/fetch-event-source";
import type {
  AgendaAction,
  Briefing,
  ChatMessage,
  Connector,
  ConnectorConfigValue,
  ConnectorDetail,
  ConnectorInstance,
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

/** Reads the API token injected into the page by the sidecar at serve time.
 *  On the Vite dev server the placeholder survives untouched → empty token. */
function readToken(): string {
  const meta = document.querySelector('meta[name="hygur-token"]');
  const t = meta?.getAttribute("content") ?? "";
  return t === "__HYGUR_TOKEN__" ? "" : t;
}

export const TOKEN = readToken();

function authHeaders(extra?: Record<string, string>): Record<string, string> {
  return { "X-Hygur-Token": TOKEN, ...(extra ?? {}) };
}

async function getJSON<T>(path: string): Promise<T> {
  const r = await fetch(path, { headers: authHeaders() });
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
  return (await r.json()) as T;
}

async function sendJSON<T>(
  method: "POST" | "PUT",
  path: string,
  body: unknown,
): Promise<T> {
  const r = await fetch(path, {
    method,
    headers: authHeaders({ "Content-Type": "application/json" }),
    body: JSON.stringify(body),
  });
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
  // Some endpoints (PUT) may return an empty body.
  const text = await r.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

const postJSON = <T>(path: string, body: unknown) =>
  sendJSON<T>("POST", path, body);
const putJSON = <T>(path: string, body: unknown) =>
  sendJSON<T>("PUT", path, body);

async function del(path: string): Promise<void> {
  const r = await fetch(path, { method: "DELETE", headers: authHeaders() });
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
}

async function patchJSON(path: string, body: unknown): Promise<void> {
  const r = await fetch(path, {
    method: "PATCH",
    headers: authHeaders({ "Content-Type": "application/json" }),
    body: JSON.stringify(body),
  });
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
}

/** Percent-encodes a content_id for use in a path WITHOUT escaping ':' — the
 *  sidecar's chi router reads the raw param and content_ids like `mail:acct:123`
 *  or `brief:2026-06-01` are stored with literal colons, so escaping them 404s. */
function cidPath(id: string): string {
  return encodeURIComponent(id).replace(/%3A/g, ":");
}

export const api = {
  health: () => fetch("/health").then((r) => r.json()),
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

  /** Uploads + ingests a file (📎). Returns the new content_id to attach. */
  uploadFile: async (
    file: File,
    projectId?: string,
  ): Promise<{ content_id: string; status: string; title: string }> => {
    const form = new FormData();
    form.append("file", file);
    if (projectId) form.append("project_id", projectId);
    const r = await fetch("/knowledge/upload", {
      method: "POST",
      headers: authHeaders(), // no Content-Type — the browser sets the boundary
      body: form,
    });
    if (!r.ok) throw new Error(`HTTP ${r.status}`);
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
  await fetchEventSource("/chat", {
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
