import { useEffect, useRef, useState } from "react";
import { AlertTriangle, ChevronRight, FolderKanban, Sparkles } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api, streamFollowupReport } from "../lib/api";
import {
  ContradictionList,
  useDismissContradiction,
  useOpenSource,
} from "../components/ContradictionList";
import { fmtDate } from "../lib/format";
import { useSlow } from "../lib/slow";
import { ErrorBanner, Page, PageHeader } from "../components/ui";
import type { DigestEntry } from "../lib/types";

export function FollowUp() {
  const openItem = useOpenSource();
  const dismiss = useDismissContradiction();
  // "" = global (recent mail & notes); otherwise a project id (W7 scope).
  const [projectId, setProjectId] = useState("");

  const projects = useQuery({ queryKey: ["projects"], queryFn: () => api.projects() });

  const digest = useQuery({
    queryKey: ["followup", projectId],
    queryFn: () => api.followup(projectId || undefined),
  });
  const contradictions = digest.data?.contradictions ?? [];
  const topics = digest.data?.topics ?? [];
  const dueTasks = digest.data?.due_tasks ?? [];
  const today = new Date().toISOString().slice(0, 10);

  const timeline = useQuery({
    queryKey: ["project-timeline", projectId],
    queryFn: () => api.projectTimeline(projectId),
    enabled: projectId !== "",
  });

  // W6 REDUCE: cross-source claim conflicts, reconciled + cited.
  const claimConflicts = useQuery({
    queryKey: ["claim-contradictions", projectId],
    queryFn: () => api.claimContradictions(projectId || undefined),
  });
  const reconciled = claimConflicts.data?.contradictions ?? [];

  const rows = timeline.data?.items ?? [];

  return (
    <Page>
      <PageHeader
        title="Suivi"
        subtitle="Une lecture ancrée de ce qui se passe et de ce sur quoi vous concentrer ensuite. Actualisé chaque heure ; chaque fait provient de vos propres messages."
        actions={
          <label className="inline-flex items-center gap-2 rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[13px] focus-within:border-accent">
            <FolderKanban size={14} strokeWidth={1.75} className="shrink-0 text-muted" />
            <select
              value={projectId}
              onChange={(e) => setProjectId(e.target.value)}
              aria-label="Portée"
              className="min-w-0 max-w-[12rem] cursor-pointer truncate bg-transparent pr-1 outline-none"
            >
              <option value="">Courriers et notes récents</option>
              {(projects.data ?? []).map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </label>
        }
      />

      {/* Report — streamed like an assistant writing. Keyed by scope so it
          re-streams from a clean slate when the project changes. */}
      <ReportStream key={projectId || "all"} projectId={projectId || undefined} />

      {/* Deadlines — open tasks due soon or overdue, surfaced proactively. */}
      {dueTasks.length > 0 && (
        <section className="mb-8">
          <Label tone="warn">Échéances</Label>
          <ul className="flex flex-col gap-2">
            {dueTasks.map((t) => {
              const overdue = t.due_date.slice(0, 10) < today;
              return (
                <li
                  key={t.id}
                  onClick={() => openItem(t.id, t.title)}
                  className="flex cursor-pointer items-center justify-between gap-3 rounded-xl border border-border bg-surface px-4 py-2.5 transition-colors hover:border-accent/40"
                >
                  <span className="truncate text-[14px] text-text">{t.title}</span>
                  <span
                    className={`tnum shrink-0 text-[12px] ${overdue ? "text-danger" : "text-muted"}`}
                  >
                    {overdue ? "en retard · " : "échéance "}
                    {fmtDate(t.due_date)}
                  </span>
                </li>
              );
            })}
          </ul>
        </section>
      )}

      {/* Cited contradictions (W6) — cross-source divergences reconciled by the LLM
          into a real conflict vs an evolution, each backed by a verbatim quote. */}
      {reconciled.length > 0 && (
        <section className="mb-8">
          <Label tone="warn">Contradictions (citées)</Label>
          <ContradictionList
            items={reconciled}
            onOpenSource={openItem}
            onDismiss={dismiss}
          />
        </section>
      )}

      {/* Contradictions — the verified, cited signal stays visible. */}
      {contradictions.length > 0 && (
        <section className="mb-8">
          <Label tone="warn">À clarifier</Label>
          <ul className="flex flex-col gap-3">
            {contradictions.map((c, i) => (
              <EntryCard key={i} entry={c} warn onOpen={openItem} />
            ))}
          </ul>
        </section>
      )}

      {/* Exchange timeline — project scope only: who, when, what, clickable. */}
      {projectId !== "" && (
        <section className="mb-8">
          <Label>Chronologie des échanges</Label>
          {rows.length > 0 ? (
            <ul className="border-t border-border">
              {rows.map((r) => (
                <li
                  key={r.content_id}
                  onClick={() => openItem(r.content_id, r.title)}
                  className="grid cursor-pointer grid-cols-[1fr_auto] items-baseline gap-x-4 border-b border-border px-1 py-2.5 transition-colors hover:bg-surface2"
                >
                  <span className="truncate text-[13.5px] text-text">
                    {r.title || "(sans titre)"}
                  </span>
                  <span className="tnum whitespace-nowrap text-[12px] text-muted">
                    {fmtDate(r.date)}
                  </span>
                  {r.from && (
                    <span className="col-span-2 truncate text-[12px] text-muted">
                      {r.from}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          ) : timeline.isLoading ? (
            <p className="text-[13px] text-muted">Chargement…</p>
          ) : (
            <p className="text-[13px] text-muted">Aucun élément lié à ce projet pour l’instant.</p>
          )}
        </section>
      )}

      {/* Active topics — hidden by default to keep the report front and centre. */}
      {topics.length > 0 && (
        <details className="group">
          <summary className="flex cursor-pointer list-none items-center gap-1.5 text-[11.5px] font-medium uppercase tracking-[0.09em] text-faint transition-colors hover:text-muted">
            <ChevronRight
              size={13}
              strokeWidth={2.2}
              className="transition-transform group-open:rotate-90"
            />
            Sujets actifs ({topics.length})
          </summary>
          <ul className="mt-3 flex flex-col gap-3">
            {topics.map((t, i) => (
              <EntryCard key={i} entry={t} onOpen={openItem} />
            ))}
          </ul>
        </details>
      )}

      {digest.isError && (
        <ErrorBanner
          message="Impossible de charger les sujets et contradictions."
          onRetry={() => digest.refetch()}
        />
      )}
    </Page>
  );
}

/** Streamed natural-language report. Owns its own stream + type-out animation;
 *  the parent remounts it (via key) when the scope changes. */
function ReportStream({ projectId }: { projectId?: string }) {
  const targetRef = useRef("");
  const [target, setTarget] = useState("");
  const [shown, setShown] = useState(0);
  const [streaming, setStreaming] = useState(true);
  const [reportErr, setReportErr] = useState<string | null>(null);

  useEffect(() => {
    const ctrl = new AbortController();
    targetRef.current = "";
    streamFollowupReport(
      {
        onDelta: (d) => {
          targetRef.current += d;
          setTarget(targetRef.current);
        },
        onDone: () => setStreaming(false),
        onError: (m) => {
          setReportErr(m);
          setStreaming(false);
        },
      },
      ctrl.signal,
      projectId,
    ).catch(() => {});
    return () => ctrl.abort();
  }, [projectId]);

  // Reveal ~180 chars/s toward whatever has streamed in.
  useEffect(() => {
    let raf = 0;
    let last = 0;
    const tick = (t: number) => {
      if (!last) last = t;
      const dt = t - last;
      last = t;
      setShown((s) => {
        const len = targetRef.current.length;
        return s >= len ? s : Math.min(len, s + Math.max(1, Math.round(dt * 0.18)));
      });
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, []);

  const visible = target.slice(0, shown);
  const paragraphs = visible.split(/\n{2,}/).filter((p) => p.trim().length > 0);
  const typing = streaming || shown < target.length;
  // Stall awareness: the stream is open but has gone quiet (resetKey = bytes
  // received so far). The report is a heavier synthesis than a chat reply, so a
  // longer threshold before the "taking longer" hint.
  const stalled = useSlow(streaming && !reportErr, 12000, target.length);

  return (
    <section className="mb-9">
      {paragraphs.length === 0 && streaming && !reportErr ? (
        <div className="flex items-center gap-2.5 rounded-xl border border-accent/30 bg-accent-weak/40 px-4 py-3.5 text-[13.5px] text-accent">
          <Sparkles size={15} strokeWidth={2} className="animate-pulse" />
          Hygur synthétise vos connaissances pour cibler ce qui compte ensuite…
        </div>
      ) : reportErr && paragraphs.length === 0 ? (
        <p className="text-[13.5px] text-muted">
          Le rapport est indisponible pour le moment. Les détails ci-dessous restent accessibles.
        </p>
      ) : (
        <div className="prose-answer text-[14.5px] leading-relaxed text-text">
          {paragraphs.map((p, i) => (
            <p key={i} className="mb-3 last:mb-0">
              {p}
              {typing && i === paragraphs.length - 1 && (
                <span className="ml-0.5 inline-block h-[1.05em] w-[2px] -translate-y-[1px] animate-pulse bg-accent align-middle" />
              )}
            </p>
          ))}
        </div>
      )}

      {/* Stall hint: still synthesizing, just gone quiet — reassures it's working. */}
      {stalled && !reportErr && (
        <div className="mt-2 flex items-center gap-2 text-[12.5px] text-muted">
          <span className="size-1.5 rounded-full bg-amber-500" />
          Toujours en cours — cela prend plus de temps que d’habitude…
        </div>
      )}
    </section>
  );
}

function Label({ children, tone }: { children: string; tone?: "warn" }) {
  return (
    <h2
      className={`mb-2.5 flex items-center gap-1.5 text-[11.5px] font-medium uppercase tracking-[0.09em] ${
        tone === "warn" ? "text-danger" : "text-faint"
      }`}
    >
      {tone === "warn" && <AlertTriangle size={13} strokeWidth={2} />}
      {children}
    </h2>
  );
}

function EntryCard({
  entry,
  warn,
  onOpen,
}: {
  entry: DigestEntry;
  warn?: boolean;
  onOpen: (contentId: string, title: string) => void;
}) {
  return (
    <li
      className={`rounded-xl border bg-surface px-4 py-3.5 ${
        warn ? "border-danger/40" : "border-border"
      }`}
    >
      {entry.title && (
        <div className="mb-1 font-medium text-text">{entry.title}</div>
      )}
      <p className="text-[14px] leading-relaxed text-text">{entry.note}</p>
      {entry.sources.length > 0 && (
        <div className="mt-2.5 flex flex-wrap gap-1.5">
          {entry.sources.map((s) => (
            <button
              key={s.content_id}
              onClick={() => onOpen(s.content_id, s.title)}
              title={s.title}
              className="tnum max-w-[16rem] truncate rounded-full border border-border px-2.5 py-0.5 text-[11.5px] text-muted transition-colors hover:border-accent hover:text-accent"
            >
              {fmtDate(s.date)}
              {s.from ? ` · ${s.from}` : ""}
            </button>
          ))}
        </div>
      )}
    </li>
  );
}
