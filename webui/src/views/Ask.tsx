import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  ArrowUp,
  History,
  Paperclip,
  Mic,
  Plus,
  X,
  FolderKanban,
  StickyNote,
  Mail,
  FileText,
  Tag as TagIcon,
  AtSign,
  Copy,
  Check,
  FileDown,
  Printer,
  ChevronRight,
  FileAudio,
  Upload,
} from "lucide-react";
import { streamChat, api } from "../lib/api";
import { native } from "../lib/native";
import type {
  AttachmentRef,
  ChatMessage,
  Mention,
  RagSource,
  SessionAttachment,
  SessionSummary,
} from "../lib/types";
import { fmtDate, srcLabel } from "../lib/format";
import { useDetail } from "../components/DetailPanel";
import { RecordList, type RecordRow } from "../components/RecordList";
import { ErrorBanner } from "../components/ui";

interface Turn {
  id: string;
  role: "user" | "assistant";
  content: string;
  sources?: RagSource[];
  activity?: string;
  error?: string;
  // Attachments carried on a user turn so they persist across the conversation
  // (F1): follow-up questions about an attached image keep its context.
  attachments?: AttachmentRef[];
}

interface ProjectFocus {
  id: string;
  name: string;
}

// MARK: - Copy / export helpers

/** Copies text to the clipboard, with an execCommand fallback for contexts
 *  where the async Clipboard API is unavailable (older WKWebView). */
async function copyText(text: string): Promise<void> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return;
    }
  } catch {
    /* fall through to legacy path */
  }
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.style.position = "fixed";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  ta.select();
  try {
    document.execCommand("copy");
  } finally {
    ta.remove();
  }
}

/** Small inline copy button that flips to a check for ~1.2s after copying. */
function CopyButton({ text, title = "Copy" }: { text: string; title?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      title={title}
      aria-label={title}
      onClick={async () => {
        await copyText(text);
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1200);
      }}
      className="rounded-md p-1 text-muted opacity-0 transition-all hover:bg-surface2 hover:text-text group-hover:opacity-100"
    >
      {copied ? (
        <Check size={13} strokeWidth={2} className="text-accent" />
      ) : (
        <Copy size={13} strokeWidth={1.9} />
      )}
    </button>
  );
}

// Audio format → MIME type for the inline <audio> data URI.
const AUDIO_MIME: Record<string, string> = {
  wav: "audio/wav",
  mp3: "audio/mpeg",
  mpeg: "audio/mpeg",
  ogg: "audio/ogg",
  opus: "audio/ogg",
  flac: "audio/flac",
  m4a: "audio/mp4",
  aac: "audio/aac",
};
const audioMime = (format: string): string =>
  AUDIO_MIME[format.toLowerCase()] || `audio/${format || "wav"}`;

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

/** An audio attachment in a turn: an inline player when the bytes are present,
 *  or a clean (non-alarming) placeholder when the recording was purged by the
 *  retention cap. */
function AudioAttachment({
  att,
}: {
  att: Extract<AttachmentRef, { type: "audio" }>;
}) {
  const playable = att.available !== false && Boolean(att.data);
  if (!playable) {
    return (
      <div className="flex items-center gap-2 rounded-xl border border-dashed border-border bg-surface2/50 px-3 py-2 text-[12.5px] text-muted">
        <FileAudio size={14} strokeWidth={1.75} className="shrink-0 text-faint" />
        <span className="truncate">
          {att.title ? `${att.title} — ` : ""}enregistrement non conservé
        </span>
      </div>
    );
  }
  return (
    <div className="rounded-xl border border-border bg-surface2 p-2.5">
      {att.title && (
        <div className="mb-1.5 flex items-center gap-1.5 px-0.5 text-[12px] text-muted">
          <FileAudio size={13} strokeWidth={1.9} className="shrink-0 text-faint" />
          <span className="truncate">{att.title}</span>
        </div>
      )}
      <audio
        controls
        preload="metadata"
        src={`data:${audioMime(att.format)};base64,${att.data}`}
        className="h-9 w-full"
      />
    </div>
  );
}

/** Renders the conversation as Markdown for export. */
function buildChatMarkdown(turns: Turn[]): string {
  const out: string[] = ["# Conversation Hygur", ""];
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
      out.push(`_[Image jointe : ${a.title || "image"}]_`, "");
    }
    for (const a of audios) {
      out.push(`_[Audio joint : ${a.title || "audio"}]_`, "");
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

// MARK: - File → attachment (shared by 📎 and drag-and-drop)

// Reads a file as raw (un-prefixed) base64 for inline image/audio attachments.
function fileToBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const res = reader.result as string;
      const comma = res.indexOf(",");
      resolve(comma >= 0 ? res.slice(comma + 1) : res);
    };
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}

// Encodes a mono AudioBuffer as a 16-bit PCM WAV file, returning un-prefixed base64.
function wavBase64FromBuffer(buf: AudioBuffer): string {
  const ch = buf.getChannelData(0);
  const n = ch.length;
  const view = new DataView(new ArrayBuffer(44 + n * 2));
  const str = (o: number, s: string) => {
    for (let i = 0; i < s.length; i++) view.setUint8(o + i, s.charCodeAt(i));
  };
  str(0, "RIFF");
  view.setUint32(4, 36 + n * 2, true);
  str(8, "WAVE");
  str(12, "fmt ");
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true); // PCM
  view.setUint16(22, 1, true); // mono
  view.setUint32(24, buf.sampleRate, true);
  view.setUint32(28, buf.sampleRate * 2, true); // byte rate
  view.setUint16(32, 2, true); // block align
  view.setUint16(34, 16, true); // bits/sample
  str(36, "data");
  view.setUint32(40, n * 2, true);
  let o = 44;
  for (let i = 0; i < n; i++) {
    const s = Math.max(-1, Math.min(1, ch[i]));
    view.setInt16(o, s < 0 ? s * 0x8000 : s * 0x7fff, true);
    o += 2;
  }
  const u8 = new Uint8Array(view.buffer);
  let bin = "";
  for (let i = 0; i < u8.length; i++) bin += String.fromCharCode(u8[i]);
  return btoa(bin);
}

// Decodes any browser-playable audio file and re-encodes it as 16 kHz mono
// 16-bit PCM WAV (un-prefixed base64). Used for m4a/AAC, which Gemma's vLLM
// can't decode but WebKit can. Throws a clear error if decoding fails.
async function transcodeToWav16kMono(file: File): Promise<string> {
  const Ctx: typeof AudioContext =
    window.AudioContext ||
    (window as unknown as { webkitAudioContext: typeof AudioContext })
      .webkitAudioContext;
  const Offline: typeof OfflineAudioContext =
    window.OfflineAudioContext ||
    (window as unknown as { webkitOfflineAudioContext: typeof OfflineAudioContext })
      .webkitOfflineAudioContext;
  if (!Ctx || !Offline) {
    throw new Error(`Audio "${file.name}" : transcodage non supporté par ce navigateur`);
  }
  const buf = await file.arrayBuffer();
  const decodeCtx = new Ctx();
  let decoded: AudioBuffer;
  try {
    decoded = await decodeCtx.decodeAudioData(buf.slice(0));
  } catch {
    throw new Error(`Audio "${file.name}" : format non décodable`);
  } finally {
    void decodeCtx.close();
  }
  const rate = 16000;
  const off = new Offline(1, Math.max(1, Math.ceil(decoded.duration * rate)), rate);
  const src = off.createBufferSource();
  src.buffer = decoded;
  src.connect(off.destination);
  src.start();
  return wavBase64FromBuffer(await off.startRendering());
}

// Turns a dropped/picked file into an AttachmentRef: images & audio go inline to
// the multimodal model (F3); everything else is uploaded + indexed as a doc.
async function buildAttachment(file: File): Promise<AttachmentRef> {
  const mime = file.type || "";
  const ext = (file.name.split(".").pop() || "").toLowerCase();
  if (mime.startsWith("image/")) {
    const data = await fileToBase64(file);
    return { type: "image", data, mime_type: mime, title: file.name };
  }
  if (
    mime.startsWith("audio/") ||
    ["m4a", "mp3", "wav", "ogg", "mp4", "aac", "m4b", "flac", "opus"].includes(ext)
  ) {
    // Gemma's vLLM audio loader can't decode the m4a/AAC container — it silently
    // drops the audio. WebKit *can*, so we transcode AAC (and any unknown
    // container) to 16 kHz mono WAV. ogg/mp3/wav/flac pass through untouched.
    const knownGood = ["ogg", "mp3", "mpeg", "wav", "flac", "opus"];
    const isAac =
      ["m4a", "mp4", "m4b", "aac"].includes(ext) || /mp4|aac|x-m4a/.test(mime);
    if (!isAac && knownGood.includes(ext)) {
      const data = await fileToBase64(file);
      return { type: "audio", data, format: ext === "mpeg" ? "mp3" : ext, title: file.name };
    }
    const data = await transcodeToWav16kMono(file);
    return { type: "audio", data, format: "wav", title: file.name };
  }
  // Documents (PDF/DOCX/text) → index in the KB, attach by reference.
  const res = await api.uploadFile(file);
  return { type: "document", content_id: res.content_id, title: res.title || file.name };
}

const EXAMPLES = [
  "What's my VAT due for Q1 2026?",
  "Summarise my last invoices from EDF",
  "What deadlines do I have this month?",
];

let turnSeq = 0;
const nextId = () => `t${++turnSeq}`;
const newSessionId = () =>
  typeof crypto !== "undefined" && crypto.randomUUID
    ? crypto.randomUUID()
    : String(Date.now());

export function Ask() {
  const openDetail = useDetail();
  const qc = useQueryClient();

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

  // Drag-and-drop attach: a depth counter rides the dragenter/leave bubbling so
  // the overlay doesn't flicker as the pointer crosses child elements.
  const [dragging, setDragging] = useState(false);
  const [dropError, setDropError] = useState<string | null>(null);
  const dragDepth = useRef(0);

  const [params, setParams] = useSearchParams();
  const lastQ = useRef<string | null>(null);

  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [turns]);

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
    setPanelOpen(true);

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
            }));
          },
          onTool: (name) =>
            patchLast((t) => ({
              ...t,
              activity:
                name === "search_knowledge_base"
                  ? "Searching your knowledge base…"
                  : `Running ${name}…`,
            })),
          onDelta: (delta) => {
            // The answer is now streaming — retract the ephemeral context panel.
            if (!hidOnStreamRef.current) {
              hidOnStreamRef.current = true;
              setPanelOpen(false);
            }
            patchLast((t) => ({
              ...t,
              content: t.content + delta,
              activity: undefined,
            }));
          },
          onError: (message) => patchLast((t) => ({ ...t, error: message })),
          onDone: () => patchLast((t) => ({ ...t, activity: undefined })),
        },
        ctrl.signal,
        { focusScope },
      );
    } catch {
      /* onError surfaced it */
    } finally {
      patchLast((t) => ({ ...t, activity: undefined }));
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
  async function openDocument(att: Extract<AttachmentRef, { type: "document" }>) {
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
  }

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
              Déposez vos fichiers pour les joindre
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
              </div>
            ) : (
              <div className="flex flex-col gap-7">
                {turns.map((t) =>
                  t.role === "user" ? (
                    <div
                      key={t.id}
                      className="group flex max-w-[86%] flex-col items-end gap-1.5 self-end print:max-w-none"
                    >
                      {/* Sent images render with the message (and in the print/PDF
                          transcript) so you can see what the turn is about. */}
                      {t.attachments?.some((a) => a.type === "image") && (
                        <div className="flex flex-wrap justify-end gap-2">
                          {t.attachments
                            .filter(
                              (a): a is Extract<AttachmentRef, { type: "image" }> =>
                                a.type === "image",
                            )
                            .map((a, i) => (
                              <img
                                key={i}
                                src={`data:${a.mime_type};base64,${a.data}`}
                                alt={a.title || "image"}
                                className="max-h-52 max-w-full rounded-xl border border-border object-contain print:max-h-none"
                              />
                            ))}
                        </div>
                      )}
                      {/* Sent audio: an inline player on replay, or a clean
                          placeholder when the recording was purged by the cap. */}
                      {t.attachments?.some((a) => a.type === "audio") && (
                        <div className="flex w-full max-w-[420px] flex-col gap-2 print:hidden">
                          {t.attachments
                            .filter(
                              (a): a is Extract<AttachmentRef, { type: "audio" }> =>
                                a.type === "audio",
                            )
                            .map((a, i) => (
                              <AudioAttachment key={i} att={a} />
                            ))}
                        </div>
                      )}
                      {/* Attached documents (PDF/DOCX/MD/…) — click to preview in
                          the right panel (rendered MD / extracted text). */}
                      {t.attachments?.some((a) => a.type === "document") && (
                        <div className="flex w-full max-w-[420px] flex-col gap-2">
                          {t.attachments
                            .filter(
                              (a): a is Extract<AttachmentRef, { type: "document" }> =>
                                a.type === "document",
                            )
                            .map((a, i) => (
                              <button
                                key={i}
                                onClick={() => void openDocument(a)}
                                className="flex w-full items-center gap-2.5 rounded-xl border border-border bg-surface2 px-3 py-2.5 text-left transition-colors hover:border-accent/50"
                              >
                                <FileText
                                  size={16}
                                  strokeWidth={1.9}
                                  className="shrink-0 text-accent"
                                />
                                <span className="truncate text-[13px] font-medium">
                                  {a.title || a.content_id}
                                </span>
                                <ChevronRight
                                  size={14}
                                  strokeWidth={2}
                                  className="ml-auto shrink-0 text-faint"
                                />
                              </button>
                            ))}
                        </div>
                      )}
                      {t.content && (
                        <div className="rounded-xl border border-border bg-surface2 px-3.5 py-2.5 text-[14.5px]">
                          {t.content}
                        </div>
                      )}
                      {t.content && (
                        <div className="print:hidden">
                          <CopyButton text={t.content} title="Copy message" />
                        </div>
                      )}
                    </div>
                  ) : (
                    <AssistantTurn key={t.id} turn={t} openDetail={openDetail} />
                  ),
                )}
              </div>
            )}
          </div>
        </div>

        {dropError && (
          <div className="px-4 sm:px-7 print:hidden">
            <div className="mx-auto max-w-[760px] pb-1">
              <ErrorBanner message={`Pièce jointe : ${dropError}`} />
            </div>
          </div>
        )}

        <div className="print:hidden">
          <Composer
            input={input}
            setInput={setInput}
            onKeyDown={onKeyDown}
            onSend={() => void send()}
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

// MARK: - Composer (textarea + 📎 + @ + 🎤)

// Matches an in-progress @-mention at the end of the input: an "@" at a word
// boundary (start of line or after whitespace) followed by any run of
// non-whitespace, non-"@" characters. Broader than \w so tag names with ":",
// "-", "/", "." or accents stay searchable; the boundary keeps email addresses
// like "john@gmail.com" from triggering the picker. Group 1 is the query.
const MENTION_RE = /(?:^|\s)@([^\s@]*)$/;

// Strips the trailing "@token" the user typed (keeps any preceding whitespace).
const MENTION_STRIP_RE = /@[^\s@]*$/;

function Composer({
  input,
  setInput,
  onKeyDown,
  onSend,
  streaming,
  taRef,
  attachments,
  setAttachments,
  focusProjects,
  setFocusProjects,
  focusTags,
  setFocusTags,
}: {
  input: string;
  setInput: (v: string) => void;
  onKeyDown: (e: React.KeyboardEvent) => void;
  onSend: () => void;
  streaming: boolean;
  taRef: React.RefObject<HTMLTextAreaElement | null>;
  attachments: AttachmentRef[];
  setAttachments: React.Dispatch<React.SetStateAction<AttachmentRef[]>>;
  focusProjects: ProjectFocus[];
  setFocusProjects: React.Dispatch<React.SetStateAction<ProjectFocus[]>>;
  focusTags: ProjectFocus[];
  setFocusTags: React.Dispatch<React.SetStateAction<ProjectFocus[]>>;
}) {
  const fileRef = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState(false);
  const [uploadError, setUploadError] = useState<string | null>(null);

  // @-mention state.
  const [mentionQuery, setMentionQuery] = useState<string | null>(null);
  const { data: mentionData, isFetching: mentionsLoading } = useQuery({
    queryKey: ["mentions", mentionQuery],
    queryFn: () => api.mentions(mentionQuery ?? ""),
    enabled: mentionQuery !== null,
  });
  const mentions = mentionData?.mentions ?? [];

  // 🎤 dictation.
  const [recording, setRecording] = useState(false);
  const dictationBase = useRef("");
  useEffect(() => {
    if (!native.dictationAvailable) return;
    return native.dictation.listen((text, isFinal) => {
      const base = dictationBase.current;
      setInput(base ? `${base} ${text}` : text);
      if (isFinal) setRecording(false);
    });
  }, [setInput]);

  function onChange(e: React.ChangeEvent<HTMLTextAreaElement>) {
    const v = e.target.value;
    setInput(v);
    // Capture everything after "@" up to the next whitespace or "@" — not just
    // \w — so queries with ":", "-", "/", "." or accents (common in tag names
    // like "mail:facture" or "RH-contact") keep the picker open and searchable.
    const m = MENTION_RE.exec(v);
    setMentionQuery(m ? m[1] : null);
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    if (e.key === "Escape" && mentionQuery !== null) {
      setMentionQuery(null);
      return;
    }
    onKeyDown(e);
  }

  // Opens the context picker without needing to type "@" — the button users
  // reach for. Shows recent notes/mails/docs/projects/tags to pick from.
  function toggleMentionPicker() {
    setMentionQuery((prev) => (prev === null ? "" : null));
    taRef.current?.focus();
  }

  function pickMention(m: Mention) {
    // Drop the trailing "@token" the user typed.
    setInput(input.replace(MENTION_STRIP_RE, ""));
    setMentionQuery(null);
    if (m.type === "project") {
      setFocusProjects((prev) =>
        prev.some((p) => p.id === m.id) ? prev : [...prev, { id: m.id, name: m.title }],
      );
    } else if (m.type === "tag") {
      setFocusTags((prev) =>
        prev.some((t) => t.id === m.id) ? prev : [...prev, { id: m.id, name: m.title }],
      );
    } else {
      setAttachments((prev) =>
        prev.some((a) => a.type === "document" && a.content_id === m.id)
          ? prev
          : [...prev, { type: "document", content_id: m.id, title: m.title }],
      );
    }
    taRef.current?.focus();
  }

  async function onFiles(files: FileList | null) {
    if (!files || files.length === 0) return;
    setUploadError(null);
    setUploading(true);
    try {
      for (const file of Array.from(files)) {
        const att = await buildAttachment(file);
        setAttachments((prev) => [...prev, att]);
      }
    } catch (e) {
      setUploadError((e as Error).message);
    } finally {
      setUploading(false);
      if (fileRef.current) fileRef.current.value = "";
    }
  }

  async function toggleMic() {
    if (recording) {
      await native.dictation.stop();
      setRecording(false);
    } else {
      dictationBase.current = input;
      const ok = await native.dictation.start();
      if (ok) setRecording(true);
    }
  }

  const hasChips =
    attachments.length > 0 || focusProjects.length > 0 || focusTags.length > 0;

  return (
    <div className="border-t border-border bg-bg/85 px-4 py-4 backdrop-blur sm:px-7">
      <div className="relative mx-auto max-w-[760px]">
        {mentionQuery !== null && (
          <ul className="absolute bottom-full z-30 mb-2 max-h-64 w-full overflow-auto rounded-xl border border-border bg-surface py-1 shadow-lg">
            {mentions.length > 0 ? (
              mentions.map((m) => (
                <li key={`${m.type}-${m.id}`}>
                  <button
                    onClick={() => pickMention(m)}
                    className="flex w-full items-center gap-2.5 px-3.5 py-2 text-left text-[13.5px] transition-colors hover:bg-accent-weak/60"
                  >
                    <MentionIcon type={m.type} />
                    <span className="truncate">{m.title}</span>
                    <span className="ml-auto text-[11px] uppercase tracking-wide text-faint">
                      {m.type}
                    </span>
                  </button>
                </li>
              ))
            ) : (
              <li className="px-3.5 py-2 text-[13px] text-muted">
                {mentionsLoading
                  ? "Searching…"
                  : "No matches — type to find notes, mails, docs & projects"}
              </li>
            )}
          </ul>
        )}

        {hasChips && (
          <div className="mb-2 flex flex-wrap gap-1.5">
            {focusProjects.map((p) => (
              <Chip
                key={`p-${p.id}`}
                icon={<FolderKanban size={12} strokeWidth={2} />}
                label={p.name}
                onRemove={() =>
                  setFocusProjects((prev) => prev.filter((x) => x.id !== p.id))
                }
              />
            ))}
            {focusTags.map((t) => (
              <Chip
                key={`t-${t.id}`}
                icon={<TagIcon size={12} strokeWidth={2} />}
                label={t.name}
                onRemove={() =>
                  setFocusTags((prev) => prev.filter((x) => x.id !== t.id))
                }
              />
            ))}
            {attachments.map((a, i) => (
              <Chip
                key={`a-${i}-${a.title ?? (a.type === "document" ? a.content_id : a.type)}`}
                icon={<Paperclip size={12} strokeWidth={2} />}
                label={a.title ?? (a.type === "document" ? a.content_id : a.type)}
                onRemove={() =>
                  setAttachments((prev) => prev.filter((_, j) => j !== i))
                }
              />
            ))}
          </div>
        )}

        {uploadError && (
          <div className="mb-2">
            <ErrorBanner message={`Upload failed: ${uploadError}`} />
          </div>
        )}

        <div className="flex items-end gap-1.5 rounded-2xl border border-border bg-surface py-2 pl-2 pr-2 focus-within:border-accent">
          <input
            ref={fileRef}
            type="file"
            multiple
            className="hidden"
            onChange={(e) => void onFiles(e.target.files)}
          />
          <ComposerIcon
            label="Attach a document"
            onClick={() => fileRef.current?.click()}
            disabled={uploading}
            spinning={uploading}
          >
            <Paperclip size={17} strokeWidth={1.9} />
          </ComposerIcon>
          <ComposerIcon
            label="Add context (notes, mails, projects, tags)"
            onClick={toggleMentionPicker}
            active={mentionQuery !== null}
          >
            <AtSign size={17} strokeWidth={1.9} />
          </ComposerIcon>

          <textarea
            ref={taRef}
            rows={1}
            value={input}
            onChange={onChange}
            onKeyDown={handleKeyDown}
            placeholder="Ask anything — @ to add context, 📎 to attach…"
            className="max-h-[168px] flex-1 resize-none bg-transparent py-1.5 text-[14.5px] text-text outline-none placeholder:text-faint"
          />

          {native.dictationAvailable && (
            <ComposerIcon
              label={recording ? "Stop dictation" : "Dictate"}
              onClick={() => void toggleMic()}
              active={recording}
            >
              <Mic size={17} strokeWidth={1.9} />
            </ComposerIcon>
          )}

          <button
            onClick={onSend}
            disabled={streaming || !input.trim()}
            aria-label="Send"
            className="grid size-9 shrink-0 place-items-center rounded-xl bg-accent text-white transition-opacity hover:opacity-90 disabled:opacity-30"
          >
            <ArrowUp size={18} strokeWidth={2.2} />
          </button>
        </div>
      </div>
    </div>
  );
}

function ComposerIcon({
  children,
  label,
  onClick,
  disabled,
  active,
  spinning,
}: {
  children: React.ReactNode;
  label: string;
  onClick: () => void;
  disabled?: boolean;
  active?: boolean;
  spinning?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={label}
      title={label}
      className={`grid size-9 shrink-0 place-items-center rounded-xl transition-colors disabled:opacity-40 ${
        active
          ? "bg-accent text-white"
          : "text-muted hover:bg-surface2 hover:text-text"
      } ${spinning ? "animate-pulse" : ""}`}
    >
      {children}
    </button>
  );
}

function Chip({
  icon,
  label,
  onRemove,
}: {
  icon: React.ReactNode;
  label: string;
  onRemove: () => void;
}) {
  return (
    <span className="inline-flex max-w-[220px] items-center gap-1.5 rounded-full border border-border bg-surface py-1 pl-2.5 pr-1.5 text-[12.5px]">
      <span className="text-accent">{icon}</span>
      <span className="truncate">{label}</span>
      <button
        onClick={onRemove}
        aria-label="Remove"
        className="rounded-full p-0.5 text-faint transition-colors hover:bg-surface2 hover:text-danger"
      >
        <X size={12} strokeWidth={2} />
      </button>
    </span>
  );
}

function MentionIcon({ type }: { type: Mention["type"] }) {
  const cls = "shrink-0 text-faint";
  if (type === "project")
    return <FolderKanban size={15} strokeWidth={1.75} className={cls} />;
  if (type === "tag") return <TagIcon size={15} strokeWidth={1.75} className={cls} />;
  if (type === "note")
    return <StickyNote size={15} strokeWidth={1.75} className={cls} />;
  if (type === "mail") return <Mail size={15} strokeWidth={1.75} className={cls} />;
  return <FileText size={15} strokeWidth={1.75} className={cls} />;
}

// MARK: - Right panel

function SessionsPanel({
  activeId,
  onPick,
  onClose,
}: {
  activeId: string;
  onPick: (id: string) => void;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ["sessions"],
    queryFn: () => api.sessions(),
  });
  const sessions: SessionSummary[] = data?.sessions ?? [];

  async function remove(id: string, e: React.MouseEvent) {
    e.stopPropagation();
    try {
      await api.deleteSession(id);
      qc.invalidateQueries({ queryKey: ["sessions"] });
    } catch {
      /* ignore */
    }
  }

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between border-b border-border px-4 py-3">
        <span className="text-[11.5px] font-medium uppercase tracking-[0.09em] text-faint">
          Conversations
        </span>
        <button
          onClick={onClose}
          aria-label="Close"
          className="rounded-md p-1 text-muted transition-colors hover:bg-surface2 hover:text-text"
        >
          <X size={15} strokeWidth={1.75} />
        </button>
      </div>
      <div className="flex-1 overflow-y-auto p-2">
        {isLoading ? (
          <p className="px-2 py-3 text-[13px] text-muted">Loading…</p>
        ) : sessions.length === 0 ? (
          <p className="px-2 py-3 text-[13px] text-muted">No past conversations yet.</p>
        ) : (
          <ul className="flex flex-col gap-0.5">
            {sessions.map((s) => (
              <li key={s.id}>
                <button
                  onClick={() => onPick(s.id)}
                  className={`group flex w-full flex-col items-start gap-0.5 rounded-lg px-2.5 py-2 text-left transition-colors ${
                    s.id === activeId
                      ? "bg-accent-weak"
                      : "hover:bg-surface2"
                  }`}
                >
                  <span className="flex w-full items-center gap-2">
                    <span className="truncate text-[13.5px] font-medium">
                      {s.title || "Untitled"}
                    </span>
                    <span
                      onClick={(e) => void remove(s.id, e)}
                      role="button"
                      tabIndex={0}
                      aria-label="Delete conversation"
                      className="ml-auto rounded p-0.5 text-faint opacity-0 transition-opacity hover:text-danger group-hover:opacity-100"
                    >
                      <X size={13} strokeWidth={2} />
                    </span>
                  </span>
                  {s.last_message && (
                    <span className="line-clamp-1 text-[12px] text-muted">
                      {s.last_message}
                    </span>
                  )}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

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

function AssistantTurn({
  turn,
  openDetail,
}: {
  turn: Turn;
  openDetail: (d: { title: string; meta: string[]; body: string }) => void;
}) {
  const streaming = turn.activity !== undefined && turn.content === "";
  const sourceRows: RecordRow[] = (turn.sources ?? []).map((s, i) => ({
    id: `${s.content_id}-${i}`,
    title: s.title,
    badge: srcLabel(s.source_type),
    meta: fmtDate(s.mail_date),
    excerpt: s.excerpt,
    onClick: () =>
      openDetail({
        title: s.title,
        meta: [
          srcLabel(s.source_type),
          fmtDate(s.mail_date),
          s.mail_from ? `from ${s.mail_from}` : "",
        ].filter(Boolean),
        body: s.excerpt,
      }),
  }));

  return (
    <div className="group">
      {turn.activity && (
        <div className="mb-2 flex items-center gap-2 text-[13px] text-muted">
          <span className="size-1.5 animate-pulse rounded-full bg-accent" />
          {turn.activity}
        </div>
      )}

      {turn.content && (
        <div className="prose-answer text-[14.5px] leading-relaxed">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{turn.content}</ReactMarkdown>
          {streaming && (
            <span
              className="ml-0.5 inline-block h-[1em] w-0.5 translate-y-0.5 bg-accent align-middle"
              style={{ animation: "hygur-blink 1s steps(2) infinite" }}
            />
          )}
        </div>
      )}

      {turn.content && !streaming && (
        <div className="mt-1 print:hidden">
          <CopyButton text={turn.content} title="Copy reply" />
        </div>
      )}

      {turn.error && (
        <div className="mt-2">
          <ErrorBanner message={turn.error} />
        </div>
      )}

      {sourceRows.length > 0 && (
        <details className="group mt-4">
          <summary className="inline-flex cursor-pointer select-none items-center gap-1 text-[11.5px] font-medium uppercase tracking-[0.09em] text-faint hover:text-muted">
            <ChevronRight
              size={13}
              strokeWidth={2}
              className="transition-transform group-open:rotate-90"
            />
            {sourceRows.length} source{sourceRows.length === 1 ? "" : "s"}
          </summary>
          <div className="mt-1.5">
            <RecordList rows={sourceRows} />
          </div>
        </details>
      )}
    </div>
  );
}
