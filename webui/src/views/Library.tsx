import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { SearchResult } from "../lib/types";
import { fmtDate, srcLabel } from "../lib/format";
import { useSlow } from "../lib/slow";
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
    // Hide calendar events here — they have the Calendar view; this keeps mail &
    // notes front and centre (search still spans everything).
    queryKey: ["knowledge-items"],
    queryFn: () => api.knowledgeItems(200, undefined, ["event"]),
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
  // A hybrid search occasionally runs long; tell the user it's still going
  // rather than leaving a bare skeleton that reads as "stuck".
  const slow = useSlow(isLoading, 10000);

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
              sourceType: r.source_type,
              meta: [
                srcLabel(r.source_type),
                fmtDate(date),
                r.mail_from ? `de ${r.mail_from}` : "",
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
          sourceType: it.source_type,
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
        title="Bibliothèque"
        subtitle="Tout ce qui est indexé — mails, notes et documents. Parcourez, ou cherchez dans l’ensemble."
      />

      <div className="mb-5">
        <TextInput
          type="search"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="Recherchez dans tout l’index — un nom, un code, une date ou une question…"
        />
      </div>

      {error && (
        <ErrorBanner
          message={`Impossible de charger : ${error.message}`}
          onRetry={() => refetch()}
        />
      )}

      {searching && !isLoading && total != null && (
        <p className="mb-2 text-[13px] text-muted">
          <span className="tnum">{total}</span> résultat{total === 1 ? "" : "s"} pour « {query} »
        </p>
      )}

      {isLoading ? (
        <>
          {slow && (
            <div className="mb-3 flex items-center gap-2 text-[12.5px] text-muted">
              <span className="size-1.5 rounded-full bg-amber-500" />
              {searching ? "Recherche toujours en cours dans votre base de connaissances…" : "Chargement toujours en cours…"}
            </div>
          )}
          <Skeleton rows={5} />
        </>
      ) : rows.length > 0 ? (
        <RecordList rows={rows} />
      ) : searching ? (
        <EmptyState
          title="Aucun résultat"
          hint="Essayez moins de termes ou des termes plus larges — ou un code, un montant ou une date exacts."
        />
      ) : (
        <EmptyState
          title="Rien d’indexé pour l’instant"
          hint="Connectez votre messagerie ou ajoutez des notes et documents — ils apparaîtront ici une fois indexés."
        />
      )}
    </Page>
  );
}
