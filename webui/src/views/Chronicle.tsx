import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { BookCheck, BookOpen, ChevronLeft, ChevronRight, Sparkles } from "lucide-react";
import { api } from "../lib/api";
import type { ChronicleAct } from "../lib/types";
import { useOpenSource } from "../components/ContradictionList";
import { useToast } from "../lib/toast";
import {
  Button,
  EmptyState,
  Page,
  PageHeader,
  Skeleton,
  ToggleGroup,
} from "../components/ui";

const LIFE = "life";

/** Windowed 1-based page list with "…" gaps: 1 … 12 [13] 14 … N. */
function pageWindow(current: number, total: number): (number | "…")[] {
  const want = new Set([1, total, current, current - 1, current + 1]);
  const sorted = [...want].filter((p) => p >= 1 && p <= total).sort((a, b) => a - b);
  const out: (number | "…")[] = [];
  let prev = 0;
  for (const p of sorted) {
    if (prev && p - prev > 1) out.push("…");
    out.push(p);
    prev = p;
  }
  return out;
}

/** The distinct, in-range [n] refs the prose actually cites, sorted. */
function citedRefs(markdown: string, sources: string[]): number[] {
  const nums = new Set<number>();
  for (const m of markdown.matchAll(/\[(\d+)\]/g)) {
    const n = Number(m[1]);
    if (n >= 1 && n <= sources.length) nums.add(n);
  }
  return [...nums].sort((a, b) => a - b);
}

export function Chronicle() {
  const qc = useQueryClient();
  const openSource = useOpenSource();
  const toast = useToast();
  const chaptersQ = useQuery({ queryKey: ["chronicle", "chapters"], queryFn: () => api.chronicle() });
  const chapters = chaptersQ.data?.chapters ?? [];

  const [selected, setSelected] = useState<string>(LIFE);
  const chapterQ = useQuery({
    queryKey: ["chronicle", "chapter", selected],
    queryFn: () => api.chronicleChapter(selected),
    retry: false, // a not-yet-written chapter 404s — treat as empty
  });
  const acts: ChronicleAct[] = chapterQ.data?.acts ?? [];
  const total = acts.length;

  const [page, setPage] = useState<number | null>(null);
  const current = total === 0 ? 0 : Math.min(Math.max(page ?? total, 1), total);
  const act = current > 0 ? acts[current - 1] : undefined;

  const [generating, setGenerating] = useState(false);
  // The writer works in the background (LLM calls) — flag it and refetch as it lands.
  const scheduleRefetch = () => {
    setGenerating(true);
    setPage(null);
    window.setTimeout(() => qc.invalidateQueries({ queryKey: ["chronicle"] }), 8000);
    window.setTimeout(() => qc.invalidateQueries({ queryKey: ["chronicle"] }), 22000);
    window.setTimeout(() => setGenerating(false), 24000);
  };
  const run = useMutation({
    mutationFn: () => api.chronicleRun(),
    onSuccess: () => {
      toast.success("Writing today's entry — it'll appear shortly.");
      scheduleRefetch();
    },
    onError: (e) => toast.error(`Couldn't start the entry: ${(e as Error).message}`),
  });

  const [closeOpen, setCloseOpen] = useState(false);
  const [closeNote, setCloseNote] = useState("");
  const closeChapter = useMutation({
    mutationFn: () => api.closeChronicleChapter(selected, closeNote.trim()),
    onSuccess: () => {
      setCloseOpen(false);
      setCloseNote("");
      scheduleRefetch();
    },
    onError: (e) => toast.error(`Couldn't close the chapter: ${(e as Error).message}`),
  });

  const [reopenOpen, setReopenOpen] = useState(false);
  const [reopenNote, setReopenNote] = useState("");
  const [reopenedHint, setReopenedHint] = useState(false);
  const reopenChapter = useMutation({
    mutationFn: () => api.reopenChronicleChapter(selected, reopenNote.trim()),
    onSuccess: () => {
      setReopenOpen(false);
      setReopenNote("");
      setReopenedHint(true);
      qc.invalidateQueries({ queryKey: ["chronicle"] }); // status flips back to open
      window.setTimeout(() => setReopenedHint(false), 14000);
    },
    onError: (e) => toast.error(`Couldn't reopen the chapter: ${(e as Error).message}`),
  });

  const isProject = selected !== LIFE;
  const chapterStatus = chapterQ.data?.status ?? "open";
  const isClosed = chapterStatus === "closed";
  const canClose = isProject && !isClosed && total > 0;

  // Chapter rail: existing chapters (Life first), or just Life before the first run.
  const rail =
    chapters.length > 0 ? chapters : [{ id: LIFE, title: "Life", status: "open", act_count: 0 }];

  return (
    <Page>
      <PageHeader
        title="Chronicle"
        subtitle="A grounded narrative of your world — one entry a night per chapter, written from your own records."
        actions={
          <Button onClick={() => run.mutate()} disabled={run.isPending || generating}>
            <Sparkles size={15} strokeWidth={1.9} />
            {generating ? "Generating…" : "Write today's entry"}
          </Button>
        }
      />

      {generating && (
        <p className="mb-3 text-[12.5px] text-muted">
          Writing in the background — it'll appear here shortly.
        </p>
      )}

      {/* Chapter rail */}
      <ToggleGroup
        variant="chips"
        ariaLabel="Chapters"
        className="mb-5"
        value={selected}
        onChange={(id) => {
          setSelected(id);
          setPage(null);
        }}
        options={rail.map((c) => ({
          value: c.id,
          label: c.title,
          count: c.act_count > 0 ? c.act_count : undefined,
          dimmed: c.status === "closed",
        }))}
      />

      {/* Lifecycle control — project chapters only ("Life" never closes) */}
      {isProject && (
        <div className="mb-4">
          {isClosed ? (
            reopenOpen ? (
              <div className="rounded-lg border border-border bg-surface p-3">
                <label className="mb-1.5 block text-[12.5px] text-muted">
                  Reopen this chapter — tell Hygur why in a few words. It narrates the resumption
                  from this on the next entry, with any mails or notes that back it up.
                </label>
                <textarea
                  value={reopenNote}
                  onChange={(e) => setReopenNote(e.target.value)}
                  rows={2}
                  placeholder="e.g. the client came back — they want a second phase."
                  className="w-full resize-y rounded-md border border-border bg-bg px-2.5 py-1.5 text-[13px] text-text placeholder:text-faint focus:border-accent focus:outline-none"
                />
                <div className="mt-2 flex items-center gap-3">
                  <Button
                    onClick={() => reopenChapter.mutate()}
                    disabled={reopenChapter.isPending || !reopenNote.trim()}
                  >
                    <BookOpen size={14} strokeWidth={1.9} />
                    {reopenChapter.isPending ? "Reopening…" : "Reopen chapter"}
                  </Button>
                  <button
                    onClick={() => {
                      setReopenOpen(false);
                      setReopenNote("");
                    }}
                    className="text-[13px] text-muted transition-colors hover:text-text"
                  >
                    Cancel
                  </button>
                </div>
              </div>
            ) : (
              <div className="flex flex-wrap items-center gap-3">
                <span className="inline-flex items-center gap-1.5 rounded-md bg-surface2 px-2.5 py-1 text-[12px] text-muted">
                  <BookCheck size={13} strokeWidth={1.9} /> This chapter is closed.
                </span>
                <button
                  onClick={() => setReopenOpen(true)}
                  className="inline-flex items-center gap-1.5 text-[12.5px] text-accent transition-colors hover:underline"
                >
                  <BookOpen size={13} strokeWidth={1.9} /> Reopen
                </button>
              </div>
            )
          ) : closeOpen ? (
            <div className="rounded-lg border border-border bg-surface p-3">
              <label className="mb-1.5 block text-[12.5px] text-muted">
                Close this chapter — Hygur writes a final entry. Add a line on how it ends
                (optional).
              </label>
              <textarea
                value={closeNote}
                onChange={(e) => setCloseNote(e.target.value)}
                rows={2}
                placeholder="e.g. shipped and handed over — nothing more expected here."
                className="w-full resize-y rounded-md border border-border bg-bg px-2.5 py-1.5 text-[13px] text-text placeholder:text-faint focus:border-accent focus:outline-none"
              />
              <div className="mt-2 flex items-center gap-3">
                <Button onClick={() => closeChapter.mutate()} disabled={closeChapter.isPending}>
                  <BookCheck size={14} strokeWidth={1.9} />
                  {closeChapter.isPending ? "Closing…" : "Close chapter"}
                </Button>
                <button
                  onClick={() => {
                    setCloseOpen(false);
                    setCloseNote("");
                  }}
                  className="text-[13px] text-muted transition-colors hover:text-text"
                >
                  Cancel
                </button>
              </div>
            </div>
          ) : canClose ? (
            <button
              onClick={() => setCloseOpen(true)}
              className="inline-flex items-center gap-1.5 text-[12.5px] text-muted transition-colors hover:text-text"
            >
              <BookCheck size={13} strokeWidth={1.9} /> Close this chapter
            </button>
          ) : null}
          {reopenedHint && (
            <p className="mt-2 text-[12.5px] text-muted">
              Reopened — the next entry resumes the story from your note. Use “Write today's
              entry” to narrate it now.
            </p>
          )}
        </div>
      )}

      {chapterQ.isLoading ? (
        <Skeleton rows={6} />
      ) : total === 0 || !act ? (
        <EmptyState
          title="No entries yet"
          hint="Hygur writes one entry a night per chapter. Generate today's now to see how it reads."
        />
      ) : (
        <>
          <article className="mx-auto min-h-[360px] max-w-[58ch] rounded-xl border border-border bg-surface px-7 py-8 sm:px-9 sm:py-10">
            <h2 className="mb-5 font-display text-[15px] font-medium uppercase tracking-[0.14em] text-faint">
              {act.title}
              {act.closing && (
                <span className="ml-2 normal-case tracking-normal text-muted">
                  · final entry
                </span>
              )}
            </h2>
            <div className="prose-answer font-display text-[16px] leading-[1.75] text-text [&_p]:mb-4">
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{act.markdown}</ReactMarkdown>
            </div>
            {(() => {
              const refs = citedRefs(act.markdown, act.sources ?? []);
              if (refs.length === 0) return null;
              return (
                <div className="mt-6 flex flex-wrap items-center gap-1.5 border-t border-border pt-4">
                  <span className="mr-1 text-[11.5px] uppercase tracking-[0.09em] text-faint">
                    Sources
                  </span>
                  {refs.map((n) => (
                    <button
                      key={n}
                      onClick={() => openSource(act.sources[n - 1], `Source [${n}]`)}
                      title="Open the source"
                      className="tnum grid size-6 place-items-center rounded-md border border-border text-[12px] text-muted transition-colors hover:border-accent hover:text-accent"
                    >
                      {n}
                    </button>
                  ))}
                </div>
              );
            })()}
          </article>

          <nav className="mt-5 flex items-center justify-center gap-1.5 text-[13px]">
            <button
              onClick={() => setPage(Math.max(1, current - 1))}
              disabled={current <= 1}
              aria-label="Previous page"
              className="grid size-8 place-items-center rounded-lg text-muted transition-colors hover:bg-surface2 hover:text-text disabled:opacity-30"
            >
              <ChevronLeft size={16} strokeWidth={2} />
            </button>
            {pageWindow(current, total).map((p, i) =>
              p === "…" ? (
                <span key={`gap-${i}`} className="px-1 text-faint">
                  …
                </span>
              ) : (
                <button
                  key={p}
                  onClick={() => setPage(p)}
                  className={`tnum grid size-8 place-items-center rounded-lg transition-colors ${
                    p === current
                      ? "bg-accent font-medium text-white"
                      : "text-muted hover:bg-surface2 hover:text-text"
                  }`}
                >
                  {p}
                </button>
              ),
            )}
            <button
              onClick={() => setPage(Math.min(total, current + 1))}
              disabled={current >= total}
              aria-label="Next page"
              className="grid size-8 place-items-center rounded-lg text-muted transition-colors hover:bg-surface2 hover:text-text disabled:opacity-30"
            >
              <ChevronRight size={16} strokeWidth={2} />
            </button>
          </nav>
          <p className="mt-2 text-center text-[12px] text-faint">
            Page {current} of {total}
          </p>
        </>
      )}
    </Page>
  );
}
