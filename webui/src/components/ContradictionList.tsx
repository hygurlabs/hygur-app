import { useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { api } from "../lib/api";
import { fmtDate, fmtDateTime } from "../lib/format";
import { useDetail } from "./DetailPanel";
import type { ReconciledConflict } from "../lib/types";

/** Opens a cited source in the detail panel — the fetch-then-open used wherever
 *  a contradiction citation is clickable. Falls back to a bare panel if the item
 *  can't be loaded. */
// eslint-disable-next-line react-refresh/only-export-components -- hook co-located with its list component (HMR-only rule; a separate file would be needless churn)
export function useOpenSource() {
  const openDetail = useDetail();
  return async (contentId: string, fallbackTitle: string) => {
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
}

/** Dismiss / restore a contradiction by its stable key, used by every placement.
 *  Optimistically drops it from all cached scopes so the card disappears at once,
 *  then reconciles with the server. */
// eslint-disable-next-line react-refresh/only-export-components -- hook co-located with its list component (HMR-only rule; a separate file would be needless churn)
export function useDismissContradiction() {
  const qc = useQueryClient();
  return async (key: string, undo = false) => {
    if (!undo) {
      qc.setQueriesData<{ contradictions?: ReconciledConflict[]; scanned?: number }>(
        { queryKey: ["claim-contradictions"] },
        (old) =>
          old
            ? { ...old, contradictions: (old.contradictions ?? []).filter((c) => c.key !== key) }
            : old,
      );
    }
    try {
      await api.dismissContradiction(key, undo);
    } finally {
      void qc.invalidateQueries({ queryKey: ["claim-contradictions"] });
    }
  };
}

/** The W6 reconciled contradictions, rendered as cited cards: a Conflict/Evolution
 *  badge, the entity·attribute, the one-line reason, and each divergent value with
 *  its verbatim quote + date, clickable to the source. Shared by every placement
 *  (Follow-up, home card, brief callout, the dedicated view) so they stay identical.
 *  `limit` truncates to the first N (compact placements). */
export function ContradictionList({
  items,
  onOpenSource,
  onDismiss,
  limit,
}: {
  items: ReconciledConflict[];
  onOpenSource: (contentId: string, fallbackTitle: string) => void;
  /** When set, each card shows a dismiss (×) / restore affordance. */
  onDismiss?: (key: string, undo: boolean) => void;
  limit?: number;
}) {
  const shown = limit ? items.slice(0, limit) : items;
  return (
    <ul className="flex flex-col gap-3">
      {shown.map((c, i) => (
        <li
          key={c.key || i}
          className={`rounded-xl border border-border bg-surface px-4 py-3 ${
            c.dismissed ? "opacity-60" : ""
          }`}
        >
          <div className="mb-1 flex items-start justify-between gap-2">
            <div className="flex flex-wrap items-center gap-2">
              <span
                className={`rounded-full px-2 py-0.5 text-[10.5px] font-medium uppercase tracking-wide ${
                  c.verdict.kind === "conflict"
                    ? "bg-danger/10 text-danger"
                    : "bg-accent-weak text-accent"
                }`}
              >
                {c.verdict.kind === "conflict" ? "Conflit" : "Évolution"}
              </span>
              <span className="text-[13.5px] font-medium text-text">
                {c.entity} · {c.attribute}
              </span>
            </div>
            {onDismiss &&
              (c.dismissed ? (
                <button
                  onClick={() => onDismiss(c.key, true)}
                  className="shrink-0 text-[11.5px] text-faint transition-colors hover:text-accent"
                >
                  Restaurer
                </button>
              ) : (
                <button
                  onClick={() => onDismiss(c.key, false)}
                  aria-label="Écarter"
                  title="Écarter — je l’ai vue"
                  className="shrink-0 rounded-md p-1 text-faint transition-colors hover:bg-surface2 hover:text-text"
                >
                  <X size={13} strokeWidth={2} />
                </button>
              ))}
          </div>
          {c.verdict.reason && (
            <p className="mb-2 text-[12.5px] text-muted">{c.verdict.reason}</p>
          )}
          <ul className="flex flex-col gap-1">
            {c.members.map((m, j) => (
              <li
                key={j}
                onClick={() => onOpenSource(m.source_id, m.value)}
                className="cursor-pointer rounded-md px-2 py-1 transition-colors hover:bg-surface2"
              >
                <span className="text-[13px] font-medium text-text">{m.value}</span>
                {m.asserted_at && (
                  <span className="tnum ml-2 text-[11px] text-faint">
                    {fmtDate(m.asserted_at)}
                  </span>
                )}
                {m.quote && (
                  <span className="block truncate text-[12px] text-muted">«{m.quote}»</span>
                )}
              </li>
            ))}
          </ul>
        </li>
      ))}
    </ul>
  );
}
