import { useQuery } from "@tanstack/react-query";
import { GitCompareArrows } from "lucide-react";
import { api } from "../lib/api";
import { useDetail } from "../components/DetailPanel";
import { fmtDateTime } from "../lib/format";
import {
  EmptyState,
  ErrorBanner,
  Page,
  PageHeader,
  Skeleton,
} from "../components/ui";
import type { Conflict } from "../lib/types";

const TYPE_LABEL: Record<string, string> = {
  amount: "Amount",
  due_date: "Due date",
  iban: "IBAN",
  vat: "VAT number",
  structured_comm: "Payment reference",
};

export function FollowUp() {
  const openDetail = useDetail();
  const q = useQuery({
    queryKey: ["contradictions"],
    queryFn: () => api.contradictions(),
  });

  // Open the source item in the reader panel (fetches the body lazily; the
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

  const conflicts = q.data?.conflicts ?? [];

  return (
    <Page>
      <PageHeader
        title="Follow-up"
        subtitle="Divergent facts Hygur spots across a mail thread — both sides cited, so you decide. Hygur signals, it never asserts."
      />

      {q.isLoading ? (
        <Skeleton rows={4} />
      ) : q.isError ? (
        <ErrorBanner
          message="Couldn't load contradictions."
          onRetry={() => q.refetch()}
        />
      ) : conflicts.length === 0 ? (
        <EmptyState
          title="No contradictions found"
          hint={`Hygur scanned ${q.data?.scanned ?? 0} mails and found no conflicting amounts, dates, IBANs, VAT numbers or payment references within a thread. New conflicts will appear here as mail arrives.`}
        />
      ) : (
        <>
          <p className="mb-5 text-[13px] text-muted">
            {conflicts.length} point{conflicts.length === 1 ? "" : "s"} to clarify
            across {q.data?.scanned ?? 0} mails.
          </p>
          <ul className="flex flex-col gap-4">
            {conflicts.map((c, i) => (
              <ConflictCard key={i} conflict={c} onOpen={openItem} />
            ))}
          </ul>
        </>
      )}
    </Page>
  );
}

function ConflictCard({
  conflict,
  onOpen,
}: {
  conflict: Conflict;
  onOpen: (contentId: string, title: string) => void;
}) {
  const high = conflict.severity === "high";
  return (
    <li
      className={`rounded-xl border bg-surface ${
        high ? "border-danger/40" : "border-border"
      }`}
    >
      <div className="flex items-center gap-2 border-b border-border px-4 py-2.5">
        <GitCompareArrows
          size={15}
          strokeWidth={1.9}
          className={high ? "text-danger" : "text-muted"}
        />
        <span
          className={`text-[11px] font-semibold uppercase tracking-wide ${
            high ? "text-danger" : "text-muted"
          }`}
        >
          {TYPE_LABEL[conflict.type] ?? conflict.type}
        </span>
        <span className="min-w-0 flex-1 truncate text-[13px] text-muted">
          {conflict.cluster}
        </span>
      </div>
      <ul>
        {conflict.members.map((m, j) => (
          <li
            key={`${m.content_id}-${j}`}
            onClick={() => onOpen(m.content_id, m.title)}
            className="grid cursor-pointer grid-cols-[1fr_auto] items-baseline gap-x-4 border-b border-border px-4 py-3 last:border-b-0 transition-colors hover:bg-surface2"
          >
            <span className="tnum truncate font-medium text-text">{m.value}</span>
            <span className="tnum whitespace-nowrap text-[12px] text-muted">
              {fmtDateTime(m.date)}
            </span>
            {m.from && (
              <span className="col-span-2 truncate text-[12.5px] text-muted">
                {m.from}
              </span>
            )}
          </li>
        ))}
      </ul>
    </li>
  );
}
