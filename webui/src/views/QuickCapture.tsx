import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import {
  StickyNote,
  MessageSquareText,
  Loader2,
  Check,
  CornerDownLeft,
} from "lucide-react";
import { streamChat, api } from "../lib/api";

type Mode = "note" | "ask";

/** A compact, frameless palette shown in its own always-on-top Tauri window
 *  (summoned by the global shortcut or the tray). It runs same-origin against
 *  the sidecar, so it captures notes and streams answers on its own — no
 *  hand-off to the main window needed. The window hides itself on blur (Rust),
 *  so dismissal is "click away" or press the shortcut again. */
export function QuickCapture() {
  // The URL (?mode=) is the single source of truth: the tray re-navigates it,
  // and the in-window tabs just update it (a hash change — no reload).
  const [params, setParams] = useSearchParams();
  const mode: Mode = params.get("mode") === "ask" ? "ask" : "note";
  const setMode = (m: Mode) => setParams({ mode: m }, { replace: true });

  return (
    <div className="flex h-dvh flex-col bg-surface text-text">
      <header
        data-tauri-drag-region
        className="flex items-center gap-1 border-b border-border px-3 py-2"
      >
        <ModeTab
          active={mode === "note"}
          onClick={() => setMode("note")}
          icon={StickyNote}
          label="Note"
        />
        <ModeTab
          active={mode === "ask"}
          onClick={() => setMode("ask")}
          icon={MessageSquareText}
          label="Ask"
        />
        <span className="ml-auto select-none text-[11px] text-faint">esc to close</span>
      </header>
      {mode === "note" ? <NotePane /> : <AskPane />}
    </div>
  );
}

function ModeTab({
  active,
  onClick,
  icon: Icon,
  label,
}: {
  active: boolean;
  onClick: () => void;
  icon: typeof StickyNote;
  label: string;
}) {
  return (
    <button
      onClick={onClick}
      className={`flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[13px] transition-colors ${
        active ? "bg-accent-weak font-medium text-accent" : "text-muted hover:text-text"
      }`}
    >
      <Icon size={15} strokeWidth={1.9} />
      {label}
    </button>
  );
}

/** Esc → blur the window so the Rust "hide on focus loss" handler dismisses it. */
function useEscapeToDismiss() {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        try {
          (document.activeElement as HTMLElement | null)?.blur();
          window.blur();
        } catch {
          /* no-op outside a native window */
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
}

function NotePane() {
  useEscapeToDismiss();
  const [text, setText] = useState("");
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const ref = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    ref.current?.focus();
  }, []);

  const save = async () => {
    const content = text.trim();
    if (!content || saving) return;
    const title = (content.split("\n")[0] ?? "").trim().slice(0, 80) || "Quick note";
    setSaving(true);
    setError(null);
    try {
      await api.createNote(title, content);
      setText("");
      setSaved(true);
      window.setTimeout(() => setSaved(false), 2500);
      ref.current?.focus();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save the note");
    } finally {
      setSaving(false);
    }
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      void save();
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col p-3">
      <textarea
        ref={ref}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKeyDown}
        placeholder="Jot something down…"
        className="min-h-0 flex-1 resize-none rounded-lg border border-border bg-bg p-3 text-[14px] leading-relaxed outline-none focus:border-accent"
      />
      <div className="mt-2 flex items-center gap-3">
        <button
          onClick={() => void save()}
          disabled={saving || !text.trim()}
          className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-[13px] font-medium text-surface transition-opacity disabled:opacity-40"
        >
          {saving ? <Loader2 size={14} className="animate-spin" /> : <Check size={14} />}
          Save note
        </button>
        <span className="flex items-center gap-1 text-[11px] text-faint">
          <CornerDownLeft size={12} /> ⌘↵ to save
        </span>
        {saved && <span className="text-[12px] text-accent">Saved ✓</span>}
        {error && <span className="text-[12px] text-danger">{error}</span>}
      </div>
    </div>
  );
}

function AskPane() {
  useEscapeToDismiss();
  const [q, setQ] = useState("");
  const [answer, setAnswer] = useState("");
  const [streaming, setStreaming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const ref = useRef<HTMLTextAreaElement>(null);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    ref.current?.focus();
    return () => abortRef.current?.abort();
  }, []);

  const ask = async () => {
    const question = q.trim();
    if (!question || streaming) return;
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setAnswer("");
    setError(null);
    setStreaming(true);
    try {
      await streamChat(
        [{ role: "user", content: question }],
        crypto.randomUUID(),
        {
          onDelta: (delta) => setAnswer((a) => a + delta),
          onError: (m) => setError(m),
        },
        ctrl.signal,
      );
    } catch {
      /* onError surfaced it */
    } finally {
      setStreaming(false);
    }
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void ask();
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col p-3">
      <div className="relative">
        <textarea
          ref={ref}
          rows={2}
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder="Ask Hygur about your documents, mail, notes…"
          className="w-full resize-none rounded-lg border border-border bg-bg p-3 pr-10 text-[14px] leading-relaxed outline-none focus:border-accent"
        />
        <span className="pointer-events-none absolute right-3 top-3 flex items-center gap-1 text-[11px] text-faint">
          {streaming ? (
            <Loader2 size={13} className="animate-spin" />
          ) : (
            <CornerDownLeft size={13} />
          )}
        </span>
      </div>
      <div className="mt-3 min-h-0 flex-1 overflow-y-auto rounded-lg bg-surface2 px-3 py-2 text-[14px] leading-relaxed">
        {error ? (
          <span className="text-danger">{error}</span>
        ) : answer ? (
          <div className="whitespace-pre-wrap">{answer}</div>
        ) : streaming ? (
          <span className="text-muted">Thinking…</span>
        ) : (
          <span className="text-faint">Press ↵ to ask · ⇧↵ for a new line</span>
        )}
      </div>
    </div>
  );
}
