import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { BookCheck, BookOpen, ChevronLeft, ChevronRight, Sparkles } from "lucide-react";
import { api } from "../lib/api";
import type { ChronicleAct } from "../lib/types";
import { useOpenSource } from "../components/ContradictionList";
import { Button, EmptyState, Page, PageHeader, Skeleton } from "../components/ui";

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
  const run = useMutation({ mutationFn: () => api.chronicleRun(), onSuccess: scheduleRefetch });

  const [closeOpen, setCloseOpen] = useState(false);
  const [closeNote, setCloseNote] = useState("");
  const closeChapter = useMutation({
    mutationFn: () => api.closeChronicleChapter(selected, closeNote.trim()),
    onSuccess: () => {
      setCloseOpen(false);
      setCloseNote("");
      scheduleRefetch();
    },
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
  });

  const isProject = selected !== LIFE;
  const chapterStatus = chapterQ.data?.status ?? "open";
  const isClosed = chapterStatus === "closed";
  const canClose = isProject && !isClosed && total > 0;

  // Chapter rail: existing chapters (Life first), or just Life before the first run.
  const rail =
    chapters.length > 0 ? chapters : [{ id: LIFE, title: "Vie", status: "open", act_count: 0 }];

  return (
    <Page>
      <PageHeader
        title="Chronique"
        subtitle="Un récit ancré de votre monde — une entrée par nuit et par chapitre, rédigée à partir de vos propres enregistrements."
        actions={
          <Button onClick={() => run.mutate()} disabled={run.isPending || generating}>
            <Sparkles size={15} strokeWidth={1.9} />
            {generating ? "Génération…" : "Écrire l’entrée du jour"}
          </Button>
        }
      />

      {generating && (
        <p className="mb-3 text-[12.5px] text-muted">
          Rédaction en arrière-plan — cela apparaîtra ici sous peu.
        </p>
      )}

      {/* Chapter rail */}
      <div className="mb-5 flex flex-wrap gap-1.5">
        {rail.map((c) => (
          <button
            key={c.id}
            onClick={() => {
              setSelected(c.id);
              setPage(null);
            }}
            className={`rounded-full border px-3 py-1 text-[13px] transition-colors ${
              c.id === selected
                ? "border-accent bg-accent-weak font-medium text-accent"
                : "border-border text-muted hover:text-text"
            } ${c.status === "closed" ? "opacity-60" : ""}`}
          >
            {c.title}
            {c.act_count > 0 && <span className="tnum ml-1.5 text-faint">{c.act_count}</span>}
          </button>
        ))}
      </div>

      {/* Lifecycle control — project chapters only ("Life" never closes) */}
      {isProject && (
        <div className="mb-4">
          {isClosed ? (
            reopenOpen ? (
              <div className="rounded-lg border border-border bg-surface p-3">
                <label className="mb-1.5 block text-[12.5px] text-muted">
                  Rouvrir ce chapitre — dites à Hygur pourquoi en quelques mots. Il raconte la reprise
                  à partir de cela dans la prochaine entrée, avec les mails ou notes qui l’étayent.
                </label>
                <textarea
                  value={reopenNote}
                  onChange={(e) => setReopenNote(e.target.value)}
                  rows={2}
                  placeholder="p. ex. le client est revenu — il veut une deuxième phase."
                  className="w-full resize-y rounded-md border border-border bg-bg px-2.5 py-1.5 text-[13px] text-text placeholder:text-faint focus:border-accent focus:outline-none"
                />
                <div className="mt-2 flex items-center gap-3">
                  <Button
                    onClick={() => reopenChapter.mutate()}
                    disabled={reopenChapter.isPending || !reopenNote.trim()}
                  >
                    <BookOpen size={14} strokeWidth={1.9} />
                    {reopenChapter.isPending ? "Réouverture…" : "Rouvrir le chapitre"}
                  </Button>
                  <button
                    onClick={() => {
                      setReopenOpen(false);
                      setReopenNote("");
                    }}
                    className="text-[13px] text-muted transition-colors hover:text-text"
                  >
                    Annuler
                  </button>
                </div>
              </div>
            ) : (
              <div className="flex flex-wrap items-center gap-3">
                <span className="inline-flex items-center gap-1.5 rounded-md bg-surface2 px-2.5 py-1 text-[12px] text-muted">
                  <BookCheck size={13} strokeWidth={1.9} /> Ce chapitre est clos.
                </span>
                <button
                  onClick={() => setReopenOpen(true)}
                  className="inline-flex items-center gap-1.5 text-[12.5px] text-accent transition-colors hover:underline"
                >
                  <BookOpen size={13} strokeWidth={1.9} /> Rouvrir
                </button>
              </div>
            )
          ) : closeOpen ? (
            <div className="rounded-lg border border-border bg-surface p-3">
              <label className="mb-1.5 block text-[12.5px] text-muted">
                Clore ce chapitre — Hygur écrit une entrée finale. Ajoutez une ligne sur la façon
                dont il se termine (facultatif).
              </label>
              <textarea
                value={closeNote}
                onChange={(e) => setCloseNote(e.target.value)}
                rows={2}
                placeholder="p. ex. livré et transmis — plus rien d’attendu ici."
                className="w-full resize-y rounded-md border border-border bg-bg px-2.5 py-1.5 text-[13px] text-text placeholder:text-faint focus:border-accent focus:outline-none"
              />
              <div className="mt-2 flex items-center gap-3">
                <Button onClick={() => closeChapter.mutate()} disabled={closeChapter.isPending}>
                  <BookCheck size={14} strokeWidth={1.9} />
                  {closeChapter.isPending ? "Clôture…" : "Clore le chapitre"}
                </Button>
                <button
                  onClick={() => {
                    setCloseOpen(false);
                    setCloseNote("");
                  }}
                  className="text-[13px] text-muted transition-colors hover:text-text"
                >
                  Annuler
                </button>
              </div>
            </div>
          ) : canClose ? (
            <button
              onClick={() => setCloseOpen(true)}
              className="inline-flex items-center gap-1.5 text-[12.5px] text-muted transition-colors hover:text-text"
            >
              <BookCheck size={13} strokeWidth={1.9} /> Clore ce chapitre
            </button>
          ) : null}
          {reopenedHint && (
            <p className="mt-2 text-[12.5px] text-muted">
              Rouvert — la prochaine entrée reprend l’histoire à partir de votre note. Utilisez
              « Écrire l’entrée du jour » pour la rédiger maintenant.
            </p>
          )}
        </div>
      )}

      {chapterQ.isLoading ? (
        <Skeleton rows={6} />
      ) : total === 0 || !act ? (
        <EmptyState
          title="Aucune entrée pour l’instant"
          hint="Hygur écrit une entrée par nuit et par chapitre. Générez celle d’aujourd’hui pour voir ce que ça donne."
        />
      ) : (
        <>
          <article className="mx-auto min-h-[360px] max-w-[58ch] rounded-xl border border-border bg-surface px-7 py-8 sm:px-9 sm:py-10">
            <h2 className="mb-5 font-display text-[15px] font-medium uppercase tracking-[0.14em] text-faint">
              {act.title}
              {act.closing && (
                <span className="ml-2 normal-case tracking-normal text-muted">
                  · entrée finale
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
                      title="Ouvrir la source"
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
              aria-label="Page précédente"
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
              aria-label="Page suivante"
              className="grid size-8 place-items-center rounded-lg text-muted transition-colors hover:bg-surface2 hover:text-text disabled:opacity-30"
            >
              <ChevronRight size={16} strokeWidth={2} />
            </button>
          </nav>
          <p className="mt-2 text-center text-[12px] text-faint">
            Page {current} sur {total}
          </p>
        </>
      )}
    </Page>
  );
}
