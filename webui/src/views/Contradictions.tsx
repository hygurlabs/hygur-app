import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import {
  ContradictionList,
  useDismissContradiction,
  useOpenSource,
} from "../components/ContradictionList";
import { useSlow } from "../lib/slow";
import { EmptyState, ErrorBanner, Page, PageHeader, Skeleton } from "../components/ui";

type Filter = "all" | "conflict" | "supersedes";

/** Dedicated surface for the W6 reconciled contradictions — the "most complete"
 *  placement: every cross-source divergence, filterable by conflict vs evolution,
 *  each value cited and clickable, dismissable (and restorable via "Show dismissed"). */
export function Contradictions() {
  const openItem = useOpenSource();
  const dismiss = useDismissContradiction();
  const [filter, setFilter] = useState<Filter>("all");
  const [showDismissed, setShowDismissed] = useState(false);
  const { data, isLoading, error, refetch } = useQuery({
    // Shares the filtered cache with the home card / brief callout when not
    // showing dismissed; a separate key when the manage view pulls them in.
    queryKey: showDismissed ? ["claim-contradictions", "", "all"] : ["claim-contradictions", ""],
    queryFn: () => api.claimContradictions(undefined, showDismissed),
  });
  // The reconciliation is LLM-backed and occasionally slow; reassure rather than
  // leave a bare skeleton (mirrors the visibility work elsewhere).
  const slow = useSlow(isLoading, 10000);

  const all = data?.contradictions ?? [];
  const conflicts = all.filter((c) => c.verdict.kind === "conflict");
  const evolutions = all.filter((c) => c.verdict.kind !== "conflict");
  const shown =
    filter === "conflict" ? conflicts : filter === "supersedes" ? evolutions : all;

  const TABS: { id: Filter; label: string; count: number }[] = [
    { id: "all", label: "All", count: all.length },
    { id: "conflict", label: "Conflicts", count: conflicts.length },
    { id: "supersedes", label: "Evolutions", count: evolutions.length },
  ];

  return (
    <Page>
      <PageHeader
        title="Contradictions"
        subtitle="Where two of your own sources disagree on the same fact — each value cited verbatim. A conflict can't both be true; an evolution is a later update to an earlier value."
        actions={
          <button
            onClick={() => setShowDismissed((v) => !v)}
            aria-pressed={showDismissed}
            className={`rounded-lg border px-3 py-1.5 text-[13px] transition-colors ${
              showDismissed
                ? "border-accent/40 bg-accent-weak text-accent"
                : "border-border text-muted hover:text-text"
            }`}
          >
            {showDismissed ? "Hide dismissed" : "Show dismissed"}
          </button>
        }
      />

      {all.length > 0 && (
        <div className="mb-5 inline-flex rounded-lg border border-border bg-surface p-0.5 text-[13px]">
          {TABS.map((t) => (
            <button
              key={t.id}
              onClick={() => setFilter(t.id)}
              className={`rounded-md px-3 py-1.5 transition-colors ${
                filter === t.id
                  ? "bg-accent-weak font-medium text-accent"
                  : "text-muted hover:text-text"
              }`}
            >
              {t.label} <span className="tnum text-faint">{t.count}</span>
            </button>
          ))}
        </div>
      )}

      {error && (
        <ErrorBanner
          message={`Couldn't load contradictions: ${(error as Error).message}`}
          onRetry={() => refetch()}
        />
      )}

      {isLoading ? (
        <>
          {slow && (
            <div className="mb-3 flex items-center gap-2 text-[12.5px] text-muted">
              <span className="size-1.5 rounded-full bg-warn" />
              Still analysing your sources…
            </div>
          )}
          <Skeleton rows={4} />
        </>
      ) : shown.length > 0 ? (
        <ContradictionList items={shown} onOpenSource={openItem} onDismiss={dismiss} />
      ) : all.length > 0 ? (
        <EmptyState title="Nothing here" hint="No items match this filter." />
      ) : (
        <EmptyState
          title="No contradictions found"
          hint="When two of your sources disagree on the same fact, they show up here — cited, so you can check for yourself."
        />
      )}
    </Page>
  );
}
