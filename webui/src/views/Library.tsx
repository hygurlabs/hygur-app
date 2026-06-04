import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { SearchResult } from "../lib/types";
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

// Unified Library: an empty box browses recently-indexed items; typing runs the
// real hybrid (lexical + semantic) search across the *entire* index — so the
// "everything in one place" promise actually holds. (Replaces the old split
// where Library filtered only the 200 loaded items client-side and Search was
// a separate view.)
export function Library() {
  const openDetail = useDetail();
  const [input, setInput] = useState("");
  const [query, setQuery] = useState("");

  // Debounce so we don't hit the server on every keystroke.
  useEffect(() => {
    const t = setTimeout(() => setQuery(input.trim()), 250);
    return () => clearTimeout(t);
  }, [input]);

  const searching = query !== "";

  const browse = useQuery({
    queryKey: ["knowledge-items"],
    queryFn: () => api.knowledgeItems(200),
    enabled: !searching,
  });

  const search = useQuery({
    queryKey: ["library-search", query],
    queryFn: () => api.search(query),
    enabled: searching,
  });

  const isLoading = searching ? search.isLoading : browse.isLoading;
  const error = (searching ? search.error : browse.error) as Error | null;
  const refetch = () => (searching ? search.refetch() : browse.refetch());

  const rows: RecordRow[] = useMemo(() => {
    if (searching) {
      const results: SearchResult[] = search.data?.results ?? [];
      return results.map((r, i) => {
        const date = r.date || r.mail_date;
        return {
          id: r.chunk_id || `${r.content_id}-${i}`,
          title: r.title,
          icon: <SourceIcon type={r.source_type} />,
          badge: srcLabel(r.source_type),
          meta: fmtDate(date),
          excerpt: r.excerpt,
          onClick: () =>
            openDetail({
              title: r.title,
              contentId: r.content_id,
              meta: [
                srcLabel(r.source_type),
                fmtDate(date),
                r.mail_from ? `from ${r.mail_from}` : "",
              ].filter(Boolean),
              body: r.excerpt,
            }),
        };
      });
    }
    const items = browse.data?.items ?? [];
    return items.map((it) => ({
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
  }, [searching, search.data, browse.data, openDetail]);

  const total = searching ? search.data?.search_stats?.total_results : undefined;

  return (
    <Page>
      <PageHeader
        title="Library"
        subtitle="Everything indexed — mail, notes, and documents. Browse, or search across all of it."
      />

      <div className="mb-5">
        <TextInput
          type="search"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="Search everything indexed — a name, a code, a date, or a question…"
        />
      </div>

      {error && (
        <ErrorBanner
          message={`Couldn't load: ${error.message}`}
          onRetry={() => refetch()}
        />
      )}

      {searching && !isLoading && total != null && (
        <p className="mb-2 text-[13px] text-muted">
          <span className="tnum">{total}</span> result{total === 1 ? "" : "s"} for “{query}”
        </p>
      )}

      {isLoading ? (
        <Skeleton rows={5} />
      ) : rows.length > 0 ? (
        <RecordList rows={rows} />
      ) : searching ? (
        <EmptyState
          title="No matches"
          hint="Try fewer or broader terms — or an exact code, amount, or date."
        />
      ) : (
        <EmptyState
          title="Nothing indexed yet"
          hint="Connect your mail or add notes and documents — they'll appear here once indexed."
        />
      )}
    </Page>
  );
}
