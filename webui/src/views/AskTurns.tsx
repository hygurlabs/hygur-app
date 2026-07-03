import { memo, useState } from "react";
import { useNavigate } from "react-router-dom";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  BadgeCheck,
  Check,
  ChevronRight,
  Copy,
  FileAudio,
  FileText,
  Globe,
  NotebookPen,
  Search,
  ShieldCheck,
  ShieldQuestion,
  Square,
  X,
} from "lucide-react";
import type {
  AttachmentRef,
  DeterminedAnswer,
  MemoryWrite,
  PendingAction,
  RagSource,
} from "../lib/types";
import { api } from "../lib/api";
import { fmtDate, srcLabel } from "../lib/format";
import { useSlow } from "../lib/slow";
import { RecordList, type RecordRow } from "../components/RecordList";
import { ErrorBanner } from "../components/ui";

export interface Turn {
  id: string;
  role: "user" | "assistant";
  content: string;
  sources?: RagSource[];
  activity?: string;
  // True when the current activity reaches the internet (web_search / fetch_url) — the UI
  // marks it distinctly because data is leaving the machine.
  activityWeb?: boolean;
  error?: string;
  // Set when the inference backend was down and only retrieved sources are shown
  // (no AI synthesis). The deterministic layer still answered with the facts.
  degraded?: boolean;
  // Set when the user hit "Stop" mid-stream: the partial answer is kept and
  // marked so it's clear it was interrupted, not completed.
  stopped?: boolean;
  // Attachments carried on a user turn so they persist across the conversation
  // (F1): follow-up questions about an attached image keep its context.
  attachments?: AttachmentRef[];
  // Facts this turn autonomously saved to memory (the `memory_write` SSE events),
  // surfaced inline so the write is visible, not buried in the Mind review queue.
  memoryWrites?: MemoryWrite[];
  // Side-effecting actions the turn requested but withheld pending confirmation
  // (the `pending_action` SSE events) — rendered as Confirm/Cancel cards. Nothing
  // is executed until the user confirms (WP3).
  pendingActions?: PendingAction[];
  // Factual-identifier answers the deterministic engine produced (`determined_answer`
  // SSE events), rendered as authoritative cards ABOVE the prose. The value comes from
  // the engine, so the LLM's framing can't substitute, hedge, or decline it (cut-safe).
  determinedAnswers?: DeterminedAnswer[];
}

// MARK: - Copy helper

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
          {att.title ? `${att.title} — ` : ""}recording not kept
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

// MARK: - Memory write indicator

/** Surfaces the facts a turn autonomously saved to memory, inline under the
 *  answer. Unobtrusive but visible — a distinct card (not alarmist), with a
 *  Review link into the Mind → Memory queue where the write can be confirmed or
 *  removed. `aria-live` polite so it's announced when it appears mid-turn. */
function MemoryWrites({ writes }: { writes: MemoryWrite[] }) {
  const navigate = useNavigate();
  return (
    <div aria-live="polite" className="mt-3 flex flex-col gap-1.5 print:hidden">
      {writes.map((w) => {
        const pending = w.status !== "accepted";
        return (
          <div
            key={w.memory_id}
            className="flex items-start gap-2.5 rounded-lg border border-border bg-surface2/50 px-3 py-2 text-[12.5px]"
          >
            <NotebookPen
              size={14}
              strokeWidth={1.9}
              className="mt-0.5 shrink-0 text-accent"
            />
            <div className="min-w-0 flex-1">
              <span className="font-medium text-text">
                {pending
                  ? "Added to your facts memory — pending your review"
                  : "Saved to your facts memory"}
              </span>
              <p className="mt-0.5 line-clamp-2 text-muted">{w.content}</p>
            </div>
            <button
              type="button"
              onClick={() => navigate("/mind?tab=memory")}
              className="shrink-0 rounded-md px-2 py-1 text-[12px] font-medium text-accent transition-colors hover:bg-accent-weak"
            >
              Review
            </button>
          </div>
        );
      })}
    </div>
  );
}

// MARK: - Pending action confirmation

// Friendly label per side-effecting tool for the confirmation card heading.
const PENDING_TOOL_LABELS: Record<string, string> = {
  create_note: "Save a note",
};

/** A single Confirm/Cancel card for a gated side-effect action (WP3). Nothing has
 *  run yet: Confirm calls POST /actions/{id}/confirm to execute the withheld
 *  tool; Cancel just dismisses it (the server-side pending action expires on its
 *  own TTL). Terminal states (confirmed/cancelled/error) replace the buttons. */
function PendingActionCard({ action }: { action: PendingAction }) {
  const [status, setStatus] = useState<NonNullable<PendingAction["status"]>>(
    action.status ?? "pending",
  );
  const [busy, setBusy] = useState(false);
  const heading = PENDING_TOOL_LABELS[action.tool] ?? "Confirm this action";

  async function confirm() {
    setBusy(true);
    try {
      await api.confirmAction(action.action_id);
      setStatus("confirmed");
    } catch {
      setStatus("error");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="rounded-lg border border-accent/30 bg-accent-weak/40 px-3.5 py-3">
      <div className="flex items-start gap-2.5">
        <ShieldCheck
          size={15}
          strokeWidth={1.9}
          className="mt-0.5 shrink-0 text-accent"
        />
        <div className="min-w-0 flex-1">
          <span className="text-[12.5px] font-medium text-text">{heading}</span>
          <p className="mt-0.5 text-[13px] text-muted">{action.preview}</p>

          {status === "pending" && (
            <div className="mt-2.5 flex items-center gap-2">
              <button
                type="button"
                onClick={confirm}
                disabled={busy}
                className="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-[12.5px] font-medium text-white transition-opacity hover:opacity-90 disabled:opacity-50"
              >
                <Check size={13} strokeWidth={2.2} />
                {busy ? "Confirming…" : "Confirm"}
              </button>
              <button
                type="button"
                onClick={() => setStatus("cancelled")}
                disabled={busy}
                className="inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-[12.5px] font-medium text-muted transition-colors hover:bg-surface2 hover:text-text disabled:opacity-50"
              >
                <X size={13} strokeWidth={2.2} />
                Cancel
              </button>
            </div>
          )}
          {status === "confirmed" && (
            <div className="mt-2 flex items-center gap-1.5 text-[12.5px] font-medium text-accent">
              <Check size={13} strokeWidth={2.2} />
              Done
            </div>
          )}
          {status === "cancelled" && (
            <div className="mt-2 flex items-center gap-1.5 text-[12.5px] text-muted">
              <X size={13} strokeWidth={2.2} />
              Cancelled — nothing was saved
            </div>
          )}
          {status === "error" && (
            <div className="mt-2 text-[12.5px] text-danger">
              Couldn&rsquo;t complete this action. Please try again.
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

/** Renders every pending side-effect action for a turn, inline under the answer. */
function PendingActions({ actions }: { actions: PendingAction[] }) {
  return (
    <div aria-live="polite" className="mt-3 flex flex-col gap-2 print:hidden">
      {actions.map((a) => (
        <PendingActionCard key={a.action_id} action={a} />
      ))}
    </div>
  );
}

// MARK: - Determined answer (engine-rendered, cut-LLM-safe)

const CONFIDENCE_LABEL: Record<DeterminedAnswer["confidence"], string> = {
  high: "Verified",
  medium: "Likely — verify",
  none: "Not verified",
};

/** The authoritative render of a factual-identifier answer. The value is PRODUCED by Hygur's
 *  deterministic engine, not the language model — so it's shown here as a distinct, high-trust
 *  card that stands on its own even if the assistant's prose hedges. On an engine decline it
 *  shows an honest "no verified value" instead of letting the model invent one. */
function DeterminedAnswerCard({ answer }: { answer: DeterminedAnswer }) {
  const declined = answer.confidence === "none" || !answer.value;
  const heading = [answer.subject === "you" ? "Your" : answer.subject, answer.label]
    .filter(Boolean)
    .join(" ")
    .trim();
  return (
    <div
      className={`rounded-xl border px-4 py-3 ${
        declined
          ? "border-border bg-surface2/50"
          : "border-accent/40 bg-accent-weak/40"
      }`}
    >
      <div className="flex items-start gap-2.5">
        {declined ? (
          <ShieldQuestion
            size={16}
            strokeWidth={1.9}
            className="mt-0.5 shrink-0 text-muted"
          />
        ) : (
          <BadgeCheck
            size={16}
            strokeWidth={1.9}
            className="mt-0.5 shrink-0 text-accent"
          />
        )}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            {heading && (
              <span className="text-[11.5px] font-medium uppercase tracking-[0.07em] text-muted">
                {heading || "Identifier"}
              </span>
            )}
            <span
              className={`rounded-full px-1.5 py-0.5 text-[10.5px] font-semibold uppercase tracking-[0.05em] ${
                declined
                  ? "bg-surface2 text-muted"
                  : answer.confidence === "high"
                    ? "bg-accent/15 text-accent"
                    : "bg-warn/15 text-warn"
              }`}
            >
              {CONFIDENCE_LABEL[answer.confidence]}
            </span>
          </div>
          {declined ? (
            <p className="mt-1 text-[13.5px] text-text">
              {answer.message ??
                "No verified value — I don't have a confirmed one on record."}
            </p>
          ) : (
            <p className="mt-0.5 select-all font-mono text-[16px] font-semibold text-text">
              {answer.value}
            </p>
          )}
          {answer.sources && answer.sources.length > 0 && (
            <p className="mt-1 text-[11.5px] text-muted">
              Source:{" "}
              {answer.sources
                .slice(0, 3)
                .map((s) => s.title || s.content_id)
                .join("; ")}
            </p>
          )}
        </div>
        {!declined && answer.value && (
          <CopyButton text={answer.value} title="Copy value" />
        )}
      </div>
    </div>
  );
}

/** Renders every engine-determined answer for a turn, above the assistant's prose. */
function DeterminedAnswers({ answers }: { answers: DeterminedAnswer[] }) {
  return (
    <div aria-live="polite" className="mb-3 flex flex-col gap-2">
      {answers.map((a, i) => (
        <DeterminedAnswerCard key={`${a.label ?? "id"}-${i}`} answer={a} />
      ))}
    </div>
  );
}

// MARK: - Turn components

// A sent (user) turn. Memoized so it doesn't re-render as later turns stream —
// its `turn` object keeps referential identity (patchLast only replaces the last
// element) and `openDocument` is a stable callback.
const UserTurn = memo(function UserTurn({
  turn,
  openDocument,
}: {
  turn: Turn;
  openDocument: (att: Extract<AttachmentRef, { type: "document" }>) => void;
}) {
  return (
    <div className="group flex max-w-[86%] flex-col items-end gap-1.5 self-end print:max-w-none">
      {/* Sent images render with the message (and in the print/PDF
          transcript) so you can see what the turn is about. */}
      {turn.attachments?.some((a) => a.type === "image") && (
        <div className="flex flex-wrap justify-end gap-2">
          {turn.attachments
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
      {turn.attachments?.some((a) => a.type === "audio") && (
        <div className="flex w-full max-w-[420px] flex-col gap-2 print:hidden">
          {turn.attachments
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
      {turn.attachments?.some((a) => a.type === "document") && (
        <div className="flex w-full max-w-[420px] flex-col gap-2">
          {turn.attachments
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
      {turn.content && (
        <div className="rounded-xl border border-border bg-surface2 px-3.5 py-2.5 text-[14.5px]">
          {turn.content}
        </div>
      )}
      {turn.content && (
        <div className="print:hidden">
          <CopyButton text={turn.content} title="Copy message" />
        </div>
      )}
    </div>
  );
});

const AssistantTurn = memo(function AssistantTurn({
  turn,
  live = false,
  openDetail,
}: {
  turn: Turn;
  /** This is the turn currently being streamed (last turn + an active request). */
  live?: boolean;
  openDetail: (d: { title: string; meta: string[]; body: string }) => void;
}) {
  // Mid-stream once any answer text has arrived; the activity line covers the
  // pre-token phase (Thinking… / Reading sources… / Running tool…).
  const streaming = live && turn.content !== "";
  // Stall awareness: resetKey changes on every progress event (a streamed token,
  // a new source, a status change), so the "taking longer than usual" hint fires
  // only after a genuine gap of silence — not on a slow-but-steady answer.
  const stallKey = `${turn.content.length}:${turn.activity ?? ""}:${turn.sources?.length ?? 0}`;
  const stalled = useSlow(live && !turn.error, 9000, stallKey);
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
        <div
          className={`mb-2 flex items-center gap-2 text-[13px] ${
            turn.activityWeb ? "text-warn" : "text-muted"
          }`}
        >
          {turn.activityWeb ? (
            <Globe size={13} strokeWidth={2.2} className="shrink-0 text-warn" />
          ) : (
            <span className="size-1.5 animate-pulse rounded-full bg-accent" />
          )}
          {turn.activity}
        </div>
      )}

      {turn.degraded && (
        <div className="mb-2 flex items-center gap-2 text-[12.5px] text-muted">
          <span className="size-1.5 rounded-full bg-warn" />
          Offline mode — AI synthesis paused; showing what Hygur found.
        </div>
      )}

      {turn.determinedAnswers && turn.determinedAnswers.length > 0 && (
        <DeterminedAnswers answers={turn.determinedAnswers} />
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

      {turn.stopped && (
        <div className="mt-2 flex items-center gap-1.5 text-[12.5px] text-muted">
          <Square size={11} strokeWidth={2} className="fill-current text-faint" />
          Stopped
        </div>
      )}

      {/* Stall hint: the request is still open but has gone quiet — tells the
          user it's working, not stuck, without an alarming error. */}
      {stalled && !turn.error && (
        <div className="mt-2 flex items-center gap-2 text-[12.5px] text-muted">
          <span className="size-1.5 rounded-full bg-warn" />
          Still working — taking longer than usual…
        </div>
      )}

      {turn.content && !streaming && (
        <div className="mt-1 flex items-center gap-3 print:hidden">
          {/* Glanceable trust marker: a loupe means this answer was grounded in
              the user's own data. Its absence means general-knowledge — no claim
              of being sourced. */}
          {sourceRows.length > 0 && (
            <span
              title={`Grounded in ${sourceRows.length} of your own ${
                sourceRows.length === 1 ? "source" : "sources"
              } — not general knowledge`}
              className="inline-flex items-center gap-1 text-[11.5px] font-medium text-accent"
            >
              <Search size={12.5} strokeWidth={2.2} />
              From your data
            </span>
          )}
          <CopyButton text={turn.content} title="Copy reply" />
        </div>
      )}

      {turn.error && (
        <div className="mt-2">
          <ErrorBanner message={turn.error} />
        </div>
      )}

      {turn.pendingActions && turn.pendingActions.length > 0 && (
        <PendingActions actions={turn.pendingActions} />
      )}

      {turn.memoryWrites && turn.memoryWrites.length > 0 && (
        <MemoryWrites writes={turn.memoryWrites} />
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
});

/** The scrolling list of conversation turns. Each turn is a memoized component
 *  keyed by index, so only the in-progress (last) turn re-renders per streamed
 *  token — earlier turns keep referential identity and are skipped. */
export function AskTurns({
  turns,
  streaming,
  openDetail,
  openDocument,
}: {
  turns: Turn[];
  streaming: boolean;
  openDetail: (d: { title: string; meta: string[]; body: string }) => void;
  openDocument: (att: Extract<AttachmentRef, { type: "document" }>) => void;
}) {
  return (
    <div className="flex flex-col gap-7">
      {turns.map((t, i) =>
        t.role === "user" ? (
          <UserTurn key={i} turn={t} openDocument={openDocument} />
        ) : (
          <AssistantTurn
            key={i}
            turn={t}
            live={streaming && i === turns.length - 1}
            openDetail={openDetail}
          />
        ),
      )}
    </div>
  );
}
