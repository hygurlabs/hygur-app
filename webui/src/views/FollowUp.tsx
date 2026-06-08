import { useQuery } from "@tanstack/react-query";
import { AlertTriangle } from "lucide-react";
import { api } from "../lib/api";
import { useDetail } from "../components/DetailPanel";
import { fmtDate, fmtDateTime } from "../lib/format";
import {
  EmptyState,
  ErrorBanner,
  Page,
  PageHeader,
  Skeleton,
} from "../components/ui";
import type { DigestEntry } from "../lib/types";

export function FollowUp() {
  const openDetail = useDetail();
  const q = useQuery({
    queryKey: ["followup"],
    queryFn: () => api.followup(),
  });

  // Open the cited source item in the reader panel (lazy body fetch; the
  // panel's ItemMeta loads project/tags by content_id on its own).
  const openItem = async (contentId: string, fallbackTitle: string) => {
    try {
      const it = await api.knowledgeItem(contentId);
      openDetail({
        title: it.title || fallbackTitle,
        contentId,
        meta: [it.date ? fmtDateTime(it.date) : "", it.source_type].filter(
          Boolean,
        ) as string[],
        body: it.normalized_text || "",
      });
    } catch {
      openDetail({ title: fallbackTitle, contentId, meta: [], body: "" });
    }
  };

  const topics = q.data?.topics ?? [];
  const contradictions = q.data?.contradictions ?? [];
  const empty = topics.length === 0 && contradictions.length === 0;

  return (
    <Page>
      <PageHeader
        title="Follow-up"
        subtitle="A grounded read of your recent mail & notes — the active topics, and anything that genuinely contradicts. Every line is cited; nothing is invented."
      />

      {q.isLoading ? (
        <Skeleton rows={5} />
      ) : q.isError ? (
        <ErrorBanner
          message="Couldn't load the follow-up digest."
          onRetry={() => q.refetch()}
        />
      ) : empty ? (
        <EmptyState
          title="Nothing to report"
          hint={`Hygur read ${q.data?.scanned ?? 0} recent mails & notes and found no active topic or contradiction worth flagging. This refreshes as new mail arrives.`}
        />
      ) : (
        <>
          {contradictions.length > 0 && (
            <section className="mb-9">
              <Label tone="warn">To clarify</Label>
              <ul className="flex flex-col gap-3">
                {contradictions.map((c, i) => (
                  <EntryCard key={i} entry={c} warn onOpen={openItem} />
                ))}
              </ul>
            </section>
          )}

          <section>
            <Label>Active topics</Label>
            {topics.length > 0 ? (
              <ul className="flex flex-col gap-3">
                {topics.map((t, i) => (
                  <EntryCard key={i} entry={t} onOpen={openItem} />
                ))}
              </ul>
            ) : (
              <p className="text-[13.5px] text-muted">
                No distinct topic stood out in your recent mail.
              </p>
            )}
          </section>
        </>
      )}
    </Page>
  );
}

function Label({
  children,
  tone,
}: {
  children: string;
  tone?: "warn";
}) {
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
