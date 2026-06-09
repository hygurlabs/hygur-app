import { useEffect, useRef, useState } from "react";
import { AlertTriangle, ChevronRight, FolderKanban, Sparkles } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api, streamFollowupReport } from "../lib/api";
import { useDetail } from "../components/DetailPanel";
import { fmtDate, fmtDateTime } from "../lib/format";
import { ErrorBanner, Page, PageHeader } from "../components/ui";
import type { DigestEntry } from "../lib/types";

export function FollowUp() {
  const openDetail = useDetail();
  // "" = global (recent mail & notes); otherwise a project id (W7 scope).
  const [projectId, setProjectId] = useState("");

  const projects = useQuery({ queryKey: ["projects"], queryFn: () => api.projects() });

  const digest = useQuery({
    queryKey: ["followup", projectId],
    queryFn: () => api.followup(projectId || undefined),
  });
  const contradictions = digest.data?.contradictions ?? [];
  const topics = digest.data?.topics ?? [];

  const timeline = useQuery({
    queryKey: ["project-timeline", projectId],
    queryFn: () => api.projectTimeline(projectId),
    enabled: projectId !== "",
  });

  const openItem = async (contentId: string, fallbackTitle: string) => {
    try {
      const it = await api.knowledgeItem(contentId);
      openDetail({
        title: it.title || fallbackTitle,
        contentId,
        sourceType: it.source_type,
        meta: [it.date ? fmtDateTime(it.date) : "", it.source_type].filter(
          Boolean,
        ) as string[],
        body: it.normalized_text || "",
      });
    } catch {
      openDetail({ title: fallbackTitle, contentId, meta: [], body: "" });
    }
  };

  const rows = timeline.data?.items ?? [];

  return (
    <Page>
      <PageHeader
        title="Follow-up"
        subtitle="A grounded read of what's going on and what to focus on next. Refreshes hourly; every fact comes from your own messages."
        actions={
          <label className="inline-flex items-center gap-2 rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[13px] focus-within:border-accent">
            <FolderKanban size={14} strokeWidth={1.75} className="shrink-0 text-muted" />
            <select
              value={projectId}
              onChange={(e) => setProjectId(e.target.value)}
              aria-label="Scope"
              className="min-w-0 max-w-[12rem] cursor-pointer truncate bg-transparent pr-1 outline-none"
            >
              <option value="">Recent mail &amp; notes</option>
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

      {/* Contradictions — the verified, cited signal stays visible. */}
      {contradictions.length > 0 && (
        <section className="mb-8">
          <Label tone="warn">To clarify</Label>
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
          <Label>Exchange timeline</Label>
          {rows.length > 0 ? (
            <ul className="border-t border-border">
              {rows.map((r) => (
                <li
                  key={r.content_id}
                  onClick={() => openItem(r.content_id, r.title)}
                  className="grid cursor-pointer grid-cols-[1fr_auto] items-baseline gap-x-4 border-b border-border px-1 py-2.5 transition-colors hover:bg-surface2"
                >
                  <span className="truncate text-[13.5px] text-text">
                    {r.title || "(untitled)"}
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
            <p className="text-[13px] text-muted">Loading…</p>
          ) : (
            <p className="text-[13px] text-muted">No items linked to this project yet.</p>
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
            Active topics ({topics.length})
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
          message="Couldn't load topics & contradictions."
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

  return (
    <section className="mb-9">
      {paragraphs.length === 0 && streaming && !reportErr ? (
        <div className="flex items-center gap-2.5 rounded-xl border border-accent/30 bg-accent-weak/40 px-4 py-3.5 text-[13.5px] text-accent">
          <Sparkles size={15} strokeWidth={2} className="animate-pulse" />
          Hygur is synthesizing your knowledge to focus on what matters next…
        </div>
      ) : reportErr && paragraphs.length === 0 ? (
        <p className="text-[13.5px] text-muted">
          The report is unavailable right now. The details below still work.
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
