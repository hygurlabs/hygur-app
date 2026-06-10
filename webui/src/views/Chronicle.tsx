import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { ChevronLeft, ChevronRight, Sparkles } from "lucide-react";
import { api } from "../lib/api";
import type { ChronicleAct } from "../lib/types";
import { Button, EmptyState, Page, PageHeader, Skeleton } from "../components/ui";

// v1: the always-open "life" chapter.
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

export function Chronicle() {
  const qc = useQueryClient();
  // Pre-first-run the chapter 404s — treat as "nothing yet" (don't retry/alarm).
  const { data, isLoading } = useQuery({
    queryKey: ["chronicle", LIFE],
    queryFn: () => api.chronicleChapter(LIFE),
    retry: false,
  });
  const acts: ChronicleAct[] = data?.acts ?? [];
  const total = acts.length;

  // 1-based page; null = default to the latest entry (open the book where it's at).
  const [page, setPage] = useState<number | null>(null);
  const current = total === 0 ? 0 : Math.min(Math.max(page ?? total, 1), total);
  const act = current > 0 ? acts[current - 1] : undefined;

  const run = useMutation({
    mutationFn: () => api.chronicleRun(),
    onSuccess: () => {
      setPage(null); // jump to the freshly written latest entry
      qc.invalidateQueries({ queryKey: ["chronicle"] });
    },
  });

  return (
    <Page>
      <PageHeader
        title="Chronicle"
        subtitle="A grounded narrative of your world, written one entry a night from your own records."
        actions={
          <Button onClick={() => run.mutate()} disabled={run.isPending}>
            <Sparkles size={15} strokeWidth={1.9} />
            {run.isPending ? "Writing…" : "Write today's entry"}
          </Button>
        }
      />

      {run.error && (
        <p className="mb-4 text-[12.5px] text-danger">Couldn't write the entry — try again.</p>
      )}

      {isLoading ? (
        <Skeleton rows={6} />
      ) : total === 0 || !act ? (
        <EmptyState
          title="No chronicle yet"
          hint="Hygur writes one entry a night. Generate the first one now to see how it reads."
        />
      ) : (
        <>
          {/* The page — a non-editable, book-like reading frame. */}
          <article className="mx-auto min-h-[360px] max-w-[58ch] rounded-xl border border-border bg-surface px-7 py-8 sm:px-9 sm:py-10">
            <h2 className="mb-5 font-display text-[15px] font-medium uppercase tracking-[0.14em] text-faint">
              {act.title}
            </h2>
            <div className="prose-answer font-display text-[16px] leading-[1.75] text-text [&_p]:mb-4">
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{act.markdown}</ReactMarkdown>
            </div>
          </article>

          {/* Page numbers — click to flip. */}
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
