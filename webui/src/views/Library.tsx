import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { fmtDate, srcLabel } from "../lib/format";
import { useDetail } from "../components/DetailPanel";
import { RecordList, type RecordRow } from "../components/RecordList";
import { SourceIcon } from "../components/SourceIcon";
import {
  EmptyState,
  ErrorBanner,
  Page,
  PageHeader,
  Skeleton,
  TextInput,
} from "../components/ui";

function metaDate(metadata?: Record<string, unknown> | null): string {
  const d = metadata?.["canonical_date"] ?? metadata?.["mail_date"];
  return typeof d === "string" ? d : "";
}

export function Library() {
  const openDetail = useDetail();
  const [filter, setFilter] = useState("");

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["knowledge-items"],
    queryFn: () => api.knowledgeItems(200),
  });

  const items = useMemo(() => data?.items ?? [], [data]);
  const rows: RecordRow[] = useMemo(() => {
    const f = filter.trim().toLowerCase();
    return items
      .filter((it) => !f || (it.title ?? "").toLowerCase().includes(f))
      .map((it) => ({
        id: it.content_id,
        title: it.title,
        icon: <SourceIcon type={it.source_type} />,
        badge: srcLabel(it.source_type),
        meta: fmtDate(metaDate(it.metadata)),
        excerpt: (it.normalized_text ?? "").slice(0, 180),
        onClick: () =>
          openDetail({
            title: it.title,
            contentId: it.content_id,
            meta: [srcLabel(it.source_type), fmtDate(metaDate(it.metadata))].filter(
              Boolean,
            ),
            body: it.normalized_text ?? "",
          }),
      }));
  }, [items, filter, openDetail]);

  return (
    <Page>
      <PageHeader
        title="Library"
        subtitle="Everything indexed — mail, notes, and documents — in one place."
      />

      <div className="mb-5">
        <TextInput
          type="search"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filter by title…"
        />
      </div>

      {error && (
        <ErrorBanner
          message={`Couldn't load the library: ${(error as Error).message}`}
          onRetry={() => refetch()}
        />
      )}

      {isLoading ? (
        <Skeleton rows={5} />
      ) : rows.length > 0 ? (
        <RecordList rows={rows} />
      ) : items.length === 0 ? (
        <EmptyState
          title="Nothing indexed yet"
          hint="Connect your mail or add notes and documents — they'll appear here once indexed."
        />
      ) : (
        <EmptyState title="No matches" hint="No titles match that filter." />
      )}
    </Page>
  );
}
