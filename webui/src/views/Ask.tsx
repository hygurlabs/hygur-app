import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowDown,
  FileDown,
  History,
  PanelRight,
  Plus,
  Printer,
  Upload,
} from "lucide-react";
import { streamChat, api } from "../lib/api";
import { native } from "../lib/native";
import type {
  AttachmentRef,
  ChatMessage,
  RagSource,
  SessionAttachment,
} from "../lib/types";
import { fmtDate, srcLabel } from "../lib/format";
import { useDetail } from "../components/DetailPanel";
import {
  ContradictionList,
  useDismissContradiction,
  useOpenSource,
} from "../components/ContradictionList";
import { ErrorBanner } from "../components/ui";
import { AskComposer, buildAttachment, type ProjectFocus } from "./AskComposer";
import { AskTurns, type Turn } from "./AskTurns";
import { SessionsPanel } from "./SessionsPanel";

// Friendly, human-readable status for each tool the agent may call (the LLM sees the raw
// snake_case name; the user should not). Unknown tools fall back to a generic label.
const TOOL_LABELS: Record<string, string> = {
  search_knowledge_base: "Searching your knowledge base…",
  lookup_identifier: "Looking up the exact value…",
  web_search: "Searching the web…",
  fetch_url: "Reading a web page…",
  summarize_thread: "Summarizing the thread…",
  list_attachments: "Listing attachments…",
  recall_memory: "Recalling from memory…",
  find_decisions: "Reviewing logged decisions…",
  create_note: "Preparing a note…",
  create_calendar_event: "Creating a calendar event…",
};
// Tools that reach the internet — flagged distinctly so the user always knows when data
// leaves the machine.
const WEB_TOOLS = new Set(["web_search", "fetch_url"]);
function toolLabel(name: string): string {
  return TOOL_LABELS[name] ?? `Running ${name}…`;
}

// MARK: - Session / export helpers

/** Maps a persisted session attachment to the in-memory AttachmentRef the turn
 *  renderer uses. Purged audio arrives with available=false and no data. */
function sessionAttachmentToRef(a: SessionAttachment): AttachmentRef {
  if (a.type === "image") {
    return {
      type: "image",
      data: a.data ?? "",
      mime_type: a.mime_type ?? "image/png",
      title: a.title,
    };
  }
  return {
    type: "audio",
    data: a.data ?? "",
    format: a.format ?? "wav",
    title: a.title,
    available: a.available,
  };
}

/** Renders the conversation as Markdown for export. */
function buildChatMarkdown(turns: Turn[]): string {
  const out: string[] = ["# Hygur conversation", ""];
  for (const t of turns) {
    const images = (t.attachments ?? []).filter(
      (a): a is Extract<AttachmentRef, { type: "image" }> => a.type === "image",
    );
    const audios = (t.attachments ?? []).filter(
      (a): a is Extract<AttachmentRef, { type: "audio" }> => a.type === "audio",
    );
    if (!t.content && images.length === 0 && audios.length === 0) continue;
    out.push(t.role === "user" ? "## 🧑 You" : "## 🤖 Hygur", "");
    for (const a of images) {
      out.push(`_[Attached image: ${a.title || "image"}]_`, "");
    }
    for (const a of audios) {
      out.push(`_[Attached audio: ${a.title || "audio"}]_`, "");
    }
    if (t.content) out.push(t.content, "");
    if (t.role === "assistant" && t.sources?.length) {
      out.push("**Sources:**");
      for (const s of t.sources) {
        out.push(`- ${s.title}${s.mail_date ? ` — ${fmtDate(s.mail_date)}` : ""}`);
      }
      out.push("");
    }
  }
  return out.join("\n");
}

const EXAMPLES = [
  "Where do things stand right now?",
  "What have I decided lately — and why?",
  "What needs my attention this week?",
];

let turnSeq = 0;
const nextId = () => `t${++turnSeq}`;
const newSessionId = () =>
  typeof crypto !== "undefined" && crypto.randomUUID
    ? crypto.randomUUID()
    : String(Date.now());

/** Home-screen contradiction card (the "feature phare" placement): surfaces the
 *  W6 reconciled contradictions on the Ask landing so they're visible on open,
 *  with a link to the full list. Renders nothing when there are none. */
function HomeContradictions() {
  const navigate = useNavigate();
  const openSource = useOpenSource();
  const dismiss = useDismissContradiction();
  const { data } = useQuery({
    queryKey: ["claim-contradictions", ""],
    queryFn: () => api.claimContradictions(),
  });
  const items = data?.contradictions ?? [];
  if (items.length === 0) return null;
  return (
    <div className="mt-9 rounded-xl border border-border bg-surface2/40 p-4">
      <div className="mb-3 flex items-center justify-between gap-3">
        <span className="flex items-center gap-1.5 text-[11.5px] font-medium uppercase tracking-[0.09em] text-danger">
          <AlertTriangle size={13} strokeWidth={2} />
          {items.length} contradiction{items.length === 1 ? "" : "s"} in your sources
        </span>
        {items.length > 2 && (
          <button
            onClick={() => navigate("/contradictions")}
            className="shrink-0 text-[12.5px] text-muted transition-colors hover:text-accent"
          >
            See all →
          </button>
        )}
      </div>
      <ContradictionList
        items={items}
        onOpenSource={openSource}
        onDismiss={dismiss}
        limit={2}
      />
    </div>
  );
}

// useIsDesktop tracks the Tailwind `lg` breakpoint (1024px). Below it the right panel is a
// full-height drawer that overlays the chat (88vw) — so on mobile we must NOT auto-open it on
// submit (it would hide the streaming answer). Desktop (lg+), where the panel is an inline
// column, keeps its existing auto-open-during-retrieval behavior.
function useIsDesktop() {
  const [isDesktop, setIsDesktop] = useState(
    () =>
      typeof window !== "undefined" &&
      window.matchMedia("(min-width: 1024px)").matches,
  );
  useEffect(() => {
    if (typeof window === "undefined") return;
    const mq = window.matchMedia("(min-width: 1024px)");
    const onChange = () => setIsDesktop(mq.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);
  return isDesktop;
}

export function Ask() {
  const openDetail = useDetail();
  const qc = useQueryClient();
  const isDesktop = useIsDesktop();

  const [sessionId, setSessionId] = useState(newSessionId);
  const [turns, setTurns] = useState<Turn[]>([]);
  const [input, setInput] = useState("");
  const [streaming, setStreaming] = useState(false);

  // Pending context for the NEXT message: 📎 file/@-mention attachments
  // (document refs, consumed per message) and @-mentioned projects (focus
  // scope, sticky for the conversation).
  const [attachments, setAttachments] = useState<AttachmentRef[]>([]);
  const [focusProjects, setFocusProjects] = useState<ProjectFocus[]>([]);
  const [focusTags, setFocusTags] = useState<ProjectFocus[]>([]);

  // Right panel: "sessions" (history picker) or "context" (live retrieval that
  // auto-hides once the answer starts streaming).
  const [panelOpen, setPanelOpen] = useState(false);
  const [panelMode, setPanelMode] = useState<"sessions" | "context">("context");
  const [liveSources, setLiveSources] = useState<RagSource[]>([]);

  const scrollRef = useRef<HTMLDivElement>(null);
  const taRef = useRef<HTMLTextAreaElement>(null);
  const abortRef = useRef<AbortController | null>(null);
  const hidOnStreamRef = useRef(false);
  // True when the user hit "Stop" (vs. an abort from starting a new chat), so the
  // finally block can mark the partial turn.
  const stoppedRef = useRef(false);

  // Smart auto-scroll: the view sticks to the bottom on new tokens ONLY while the
  // reader is already there. `atBottomRef` tracks that (updated on scroll). When
  // the reader scrolls up (`detached`) during a live stream, a floating "Jump to
  // latest" chip appears — so a long answer can stream while they read further up.
  const atBottomRef = useRef(true);
  const [detached, setDetached] = useState(false);

  // Drag-and-drop attach: a depth counter rides the dragenter/leave bubbling so
  // the overlay doesn't flicker as the pointer crosses child elements.
  const [dragging, setDragging] = useState(false);
  const [dropError, setDropError] = useState<string | null>(null);
  const dragDepth = useRef(0);

  const [params, setParams] = useSearchParams();
  const lastQ = useRef<string | null>(null);

  // Track whether the reader is pinned to the bottom (mounted once). Whether the
  // chip actually shows is derived at render time (`detached && streaming`), so
  // it clears on its own when the stream ends — no effect needs to reset it.
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const onScroll = () => {
      const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
      atBottomRef.current = atBottom;
      setDetached(!atBottom);
    };
    el.addEventListener("scroll", onScroll, { passive: true });
    return () => el.removeEventListener("scroll", onScroll);
  }, []);

  // Stick to the bottom on new content only when the reader hasn't scrolled up.
  useEffect(() => {
    const el = scrollRef.current;
    if (el && atBottomRef.current) el.scrollTop = el.scrollHeight;
  }, [turns]);

  function jumpToLatest() {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
    atBottomRef.current = true;
    setDetached(false);
  }

  useEffect(() => {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = "auto";
    ta.style.height = `${Math.min(ta.scrollHeight, 168)}px`;
  }, [input]);

  // Deep-linked query (#/?q=…&n=…) from the native shell — run once, then strip.
  useEffect(() => {
    const q = params.get("q");
    if (!q) return;
    const key = `${q} ${params.get("n") ?? ""}`;
    if (key !== lastQ.current && !streaming) {
      lastQ.current = key;
      setParams({}, { replace: true });
      void send(q);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params]);

  // Deep-link: a document opened from Library/Search can be attached to the
  // chat (#/?attach=<content_id>&attachTitle=…) so the user asks about it.
  const lastAttach = useRef<string | null>(null);
  useEffect(() => {
    const attach = params.get("attach");
    if (!attach || attach === lastAttach.current) return;
    lastAttach.current = attach;
    const title = params.get("attachTitle") ?? undefined;
    setAttachments((prev) =>
      prev.some((a) => a.type === "document" && a.content_id === attach)
        ? prev
        : [...prev, { type: "document", content_id: attach, title }],
    );
    setParams({}, { replace: true });
    taRef.current?.focus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params]);

  function patchLast(patch: (t: Turn) => Turn) {
    setTurns((prev) => {
      if (prev.length === 0) return prev;
      const copy = prev.slice();
      copy[copy.length - 1] = patch(copy[copy.length - 1]);
      return copy;
    });
  }

  // Stop the in-flight stream: abort the request and keep the partial answer,
  // flagged "stopped" (vs. startNewChat's abort, which clears the turns).
  function stop() {
    stoppedRef.current = true;
    abortRef.current?.abort();
  }

  function startNewChat() {
    abortRef.current?.abort();
    setSessionId(newSessionId());
    setTurns([]);
    setAttachments([]);
    setFocusProjects([]);
    setFocusTags([]);
    setLiveSources([]);
    setPanelOpen(false);
    setInput("");
    taRef.current?.focus();
  }

  async function loadSession(id: string) {
    try {
      const detail = await api.session(id);
      setSessionId(detail.id);
      setTurns(
        detail.messages.map((m) => ({
          id: nextId(),
          role: m.role,
          content: m.content,
          sources: m.sources,
          ...(m.attachments?.length
            ? { attachments: m.attachments.map(sessionAttachmentToRef) }
            : {}),
        })),
      );
      setAttachments([]);
      setFocusProjects([]);
      setFocusTags([]);
      setPanelOpen(false);
    } catch {
      /* surfaced by the panel's own error state if needed */
    }
  }

  async function send(explicit?: string) {
    const q = (explicit ?? input).trim();
    if (!q || streaming) return;
    if (!explicit) setInput("");
    setStreaming(true);

    const msgAttachments = attachments.slice();
    setAttachments([]);
    setLiveSources([]);
    hidOnStreamRef.current = false;
    setPanelMode("context");
    // Auto-open the live-context panel only on desktop, where it's an inline column. On mobile
    // it's a full-screen drawer that would hide the answer/streaming — the user opens it manually
    // via the Sources toggle when they want it.
    if (isDesktop) setPanelOpen(true);

    const userTurn: Turn = {
      id: nextId(),
      role: "user",
      content: q,
      ...(msgAttachments.length ? { attachments: msgAttachments } : {}),
    };
    const assistantTurn: Turn = {
      id: nextId(),
      role: "assistant",
      content: "",
      activity: "Thinking…",
    };
    setTurns((prev) => [...prev, userTurn, assistantTurn]);

    const history: ChatMessage[] = [
      ...turns.map((t) => {
        // Carry document + image attachments across turns (F1) so follow-ups
        // keep their context. Audio is excluded: it's transcribed once and the
        // transcript lives in the reply, so re-sending the bytes each turn is
        // wasteful.
        const carried = t.attachments?.filter((a) => a.type !== "audio");
        return {
          role: t.role,
          content: t.content,
          ...(carried && carried.length ? { attachments: carried } : {}),
        };
      }),
      {
        role: "user" as const,
        content: q,
        ...(msgAttachments.length ? { attachments: msgAttachments } : {}),
      },
    ];

    const projectIds = focusProjects.map((p) => p.id);
    const tagIds = focusTags.map((t) => t.id);
    const focusScope =
      projectIds.length || tagIds.length
        ? {
            ...(projectIds.length ? { project_ids: projectIds } : {}),
            ...(tagIds.length ? { tag_ids: tagIds } : {}),
          }
        : undefined;

    const ctrl = new AbortController();
    abortRef.current = ctrl;
    try {
      await streamChat(
        history,
        sessionId,
        {
          onSources: (sources) => {
            setLiveSources(sources);
            patchLast((t) => ({
              ...t,
              sources,
              activity: `Reading ${sources.length} source${sources.length === 1 ? "" : "s"}…`,
              activityWeb: false,
            }));
          },
          onTool: (name) =>
            patchLast((t) => ({
              ...t,
              activity: toolLabel(name),
              activityWeb: WEB_TOOLS.has(name),
            })),
          onDelta: (delta) => {
            // The answer is now streaming — retract the ephemeral context panel (desktop only;
            // on mobile it was never auto-opened, and a manually-opened one stays until dismissed).
            if (!hidOnStreamRef.current) {
              hidOnStreamRef.current = true;
              if (isDesktop) setPanelOpen(false);
            }
            patchLast((t) => ({
              ...t,
              content: t.content + delta,
              activity: undefined,
            }));
          },
          onError: (message) => patchLast((t) => ({ ...t, error: message })),
          onMemoryWrite: (write) =>
            patchLast((t) => ({
              ...t,
              memoryWrites: [...(t.memoryWrites ?? []), write],
            })),
          onPendingAction: (action) =>
            patchLast((t) => ({
              ...t,
              pendingActions: [...(t.pendingActions ?? []), action],
            })),
          onDeterminedAnswer: (answer) =>
            patchLast((t) => ({
              ...t,
              determinedAnswers: [...(t.determinedAnswers ?? []), answer],
            })),
          onDone: (degraded) =>
            patchLast((t) => ({ ...t, activity: undefined, degraded: !!degraded })),
        },
        ctrl.signal,
        { focusScope },
      );
    } catch {
      /* onError surfaced it */
    } finally {
      const wasStopped = stoppedRef.current;
      stoppedRef.current = false;
      patchLast((t) => ({
        ...t,
        activity: undefined,
        ...(wasStopped ? { stopped: true } : {}),
      }));
      setStreaming(false);
      abortRef.current = null;
      // The session now exists/updated server-side — refresh the history list.
      qc.invalidateQueries({ queryKey: ["sessions"] });
      taRef.current?.focus();
    }
  }

  function onKeyDown(e: React.KeyboardEvent) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void send();
    }
  }

  function toggleHistory() {
    if (panelOpen && panelMode === "sessions") {
      setPanelOpen(false);
    } else {
      setPanelMode("sessions");
      setPanelOpen(true);
    }
  }

  // Open an attached document in the right-side preview panel: fetch its
  // extracted/normalised text and render it (Markdown for .md, text for PDF/DOCX).
  // Stable identity (openDetail is itself stable) so the memoized UserTurn skips
  // re-rendering as later turns stream.
  const openDocument = useCallback(
    async (att: Extract<AttachmentRef, { type: "document" }>) => {
      try {
        const item = await api.knowledgeItem(att.content_id);
        openDetail({
          title: item.title || att.title || "Document",
          contentId: att.content_id,
          meta: [srcLabel(item.source_type), fmtDate(item.date)].filter(Boolean),
          body: item.normalized_text || "_(empty)_",
        });
      } catch {
        openDetail({
          title: att.title || "Document",
          contentId: att.content_id,
          meta: [],
          body: "_(couldn't load this document)_",
        });
      }
    },
    [openDetail],
  );

  const isFileDrag = (e: React.DragEvent) =>
    Array.from(e.dataTransfer.types).includes("Files");

  function onDragEnter(e: React.DragEvent) {
    if (!isFileDrag(e)) return;
    dragDepth.current += 1;
    setDragging(true);
  }
  function onDragOver(e: React.DragEvent) {
    if (isFileDrag(e)) e.preventDefault(); // required to allow the drop
  }
  function onDragLeave() {
    dragDepth.current -= 1;
    if (dragDepth.current <= 0) {
      dragDepth.current = 0;
      setDragging(false);
    }
  }
  async function onDropFiles(e: React.DragEvent) {
    if (!isFileDrag(e)) return;
    e.preventDefault();
    dragDepth.current = 0;
    setDragging(false);
    const files = e.dataTransfer.files;
    if (!files || files.length === 0) return;
    setDropError(null);
    try {
      for (const file of Array.from(files)) {
        const att = await buildAttachment(file);
        setAttachments((prev) => [...prev, att]);
      }
    } catch (err) {
      setDropError((err as Error).message);
    }
  }

  return (
    <div className="flex h-full">
      <div
        className="relative flex min-w-0 flex-1 flex-col"
        onDragEnter={onDragEnter}
        onDragOver={onDragOver}
        onDragLeave={onDragLeave}
        onDrop={onDropFiles}
      >
        {dragging && (
          <div className="pointer-events-none absolute inset-0 z-40 m-3 flex flex-col items-center justify-center gap-2 rounded-2xl border-2 border-dashed border-accent bg-accent-weak/70 backdrop-blur-sm print:hidden">
            <Upload size={28} strokeWidth={1.75} className="text-accent" />
            <span className="text-[14px] font-medium text-accent">
              Drop your files to attach them
            </span>
          </div>
        )}
        <header className="flex items-center justify-between border-b border-border px-4 py-3 sm:px-7 print:hidden">
          <span className="font-display text-[15px] font-semibold tracking-tight">
            Ask Hygur
          </span>
          <div className="flex items-center gap-1">
            <button
              onClick={startNewChat}
              className="inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-[13px] text-muted transition-colors hover:bg-surface2 hover:text-text"
            >
              <Plus size={15} strokeWidth={1.9} />
              <span className="hidden sm:inline">New</span>
            </button>
            <button
              onClick={toggleHistory}
              aria-pressed={panelOpen && panelMode === "sessions"}
              className={`inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-[13px] transition-colors ${
                panelOpen && panelMode === "sessions"
                  ? "bg-accent-weak text-accent"
                  : "text-muted hover:bg-surface2 hover:text-text"
              }`}
            >
              <History size={15} strokeWidth={1.9} />
              <span className="hidden sm:inline">History</span>
            </button>
            {/* Sources toggle — mobile only (lg:hidden). On mobile the context panel no longer
                auto-opens on submit; this lets the user open the live/last sources when wanted.
                Desktop keeps the panel's auto-open-during-retrieval behavior, so it's hidden there. */}
            <button
              onClick={() => {
                if (panelOpen && panelMode === "context") {
                  setPanelOpen(false);
                } else {
                  setPanelMode("context");
                  setPanelOpen(true);
                }
              }}
              aria-pressed={panelOpen && panelMode === "context"}
              title="Show sources"
              className={`lg:hidden inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-[13px] transition-colors ${
                panelOpen && panelMode === "context"
                  ? "bg-accent-weak text-accent"
                  : "text-muted hover:bg-surface2 hover:text-text"
              }`}
            >
              <PanelRight size={15} strokeWidth={1.9} />
              <span className="hidden sm:inline">Sources</span>
            </button>
            <button
              onClick={() =>
                native.download(
                  "hygur-chat.md",
                  "text/markdown;charset=utf-8",
                  buildChatMarkdown(turns),
                )
              }
              disabled={turns.length === 0}
              title="Export conversation as Markdown"
              className="inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-[13px] text-muted transition-colors hover:bg-surface2 hover:text-text disabled:opacity-40"
            >
              <FileDown size={15} strokeWidth={1.9} />
              <span className="hidden sm:inline">MD</span>
            </button>
            <button
              onClick={() => native.print()}
              disabled={turns.length === 0}
              title="Export as PDF (via system print)"
              className="inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-[13px] text-muted transition-colors hover:bg-surface2 hover:text-text disabled:opacity-40"
            >
              <Printer size={15} strokeWidth={1.9} />
              <span className="hidden sm:inline">PDF</span>
            </button>
          </div>
        </header>

        <div className="relative flex min-h-0 flex-1 flex-col">
          <div ref={scrollRef} className="flex-1 overflow-y-auto print:overflow-visible">
          <div className="view-enter mx-auto max-w-[760px] px-4 pb-8 pt-8 sm:px-7">
            {turns.length === 0 ? (
              <div className="pt-8">
                <h1 className="font-display text-[28px] font-semibold leading-tight tracking-tight">
                  Ask Hygur
                </h1>
                <p className="mt-2 max-w-[56ch] text-[14px] text-muted">
                  Everything stays on your machine. Attach documents with the
                  clip, pull in notes, mails and projects with{" "}
                  <span className="font-mono">@</span>, or dictate with the mic.
                </p>
                <div className="mt-7 flex flex-col items-start gap-2">
                  {EXAMPLES.map((ex) => (
                    <button
                      key={ex}
                      onClick={() => {
                        setInput(ex);
                        taRef.current?.focus();
                      }}
                      className="rounded-lg border border-border bg-surface px-3.5 py-2 text-left text-[13.5px] text-muted transition-colors hover:border-accent/40 hover:text-text"
                    >
                      {ex}
                    </button>
                  ))}
                </div>
                <HomeContradictions />
              </div>
            ) : (
              <AskTurns
                turns={turns}
                streaming={streaming}
                openDetail={openDetail}
                openDocument={openDocument}
              />
            )}
          </div>
          </div>
          {detached && streaming && (
            <button
              type="button"
              onClick={jumpToLatest}
              className="absolute bottom-4 left-1/2 z-20 flex -translate-x-1/2 items-center gap-1.5 rounded-full border border-border bg-surface px-3.5 py-1.5 text-[12.5px] font-medium text-muted shadow-lg transition-colors hover:text-text print:hidden"
            >
              <ArrowDown size={14} strokeWidth={2} />
              Jump to latest
            </button>
          )}
        </div>

        {dropError && (
          <div className="px-4 sm:px-7 print:hidden">
            <div className="mx-auto max-w-[760px] pb-1">
              <ErrorBanner message={`Attachment: ${dropError}`} />
            </div>
          </div>
        )}

        <div className="print:hidden">
          <AskComposer
            input={input}
            setInput={setInput}
            onKeyDown={onKeyDown}
            onSend={() => void send()}
            onStop={stop}
            streaming={streaming}
            taRef={taRef}
            attachments={attachments}
            setAttachments={setAttachments}
            focusProjects={focusProjects}
            setFocusProjects={setFocusProjects}
            focusTags={focusTags}
            setFocusTags={setFocusTags}
          />
        </div>
      </div>

      {panelOpen && (
        <>
          {/* Below lg the panel overlays the chat as a right drawer; tap the
              backdrop to dismiss. From lg it's an inline column. */}
          <div
            aria-hidden
            onClick={() => setPanelOpen(false)}
            className="fixed inset-0 z-20 bg-text/25 lg:hidden print:hidden"
          />
          <aside className="fixed inset-y-0 right-0 z-30 flex w-[min(340px,88vw)] shrink-0 flex-col border-l border-border bg-surface shadow-xl lg:static lg:z-auto lg:w-[300px] lg:bg-surface2/40 lg:shadow-none print:hidden">
            {panelMode === "sessions" ? (
              <SessionsPanel
                activeId={sessionId}
                onPick={loadSession}
                onClose={() => setPanelOpen(false)}
              />
            ) : (
              <ContextPanel sources={liveSources} openDetail={openDetail} />
            )}
          </aside>
        </>
      )}
    </div>
  );
}

// MARK: - Right panel

function ContextPanel({
  sources,
  openDetail,
}: {
  sources: RagSource[];
  openDetail: (d: { title: string; meta: string[]; body: string }) => void;
}) {
  return (
    <div className="flex h-full flex-col">
      <div className="border-b border-border px-4 py-3">
        <span className="text-[11.5px] font-medium uppercase tracking-[0.09em] text-faint">
          Building context
        </span>
      </div>
      <div className="flex-1 overflow-y-auto p-3">
        {sources.length === 0 ? (
          <div className="flex items-center gap-2 px-1 py-3 text-[13px] text-muted">
            <span className="size-1.5 animate-pulse rounded-full bg-accent" />
            Searching your data…
          </div>
        ) : (
          <ul className="flex flex-col gap-2">
            {sources.map((s, i) => (
              <li key={`${s.content_id}-${i}`}>
                <button
                  onClick={() =>
                    openDetail({
                      title: s.title,
                      meta: [
                        srcLabel(s.source_type),
                        fmtDate(s.mail_date),
                        s.mail_from ? `from ${s.mail_from}` : "",
                      ].filter(Boolean),
                      body: s.excerpt,
                    })
                  }
                  className="w-full rounded-lg border border-border bg-surface px-3 py-2.5 text-left transition-colors hover:border-accent/40"
                >
                  <span className="line-clamp-1 text-[13px] font-medium">
                    {s.title || "(untitled)"}
                  </span>
                  <span className="mt-0.5 line-clamp-2 text-[12px] text-muted">
                    {s.excerpt}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
