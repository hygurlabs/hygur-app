import { useState } from "react";
import { api } from "../lib/api";
import type { SearchResult, SearchStats } from "../lib/types";
import { fmtDate, srcLabel } from "../lib/format";
import { useDetail } from "../components/DetailPanel";
import { RecordList, type RecordRow } from "../components/RecordList";
import { SourceIcon } from "../components/SourceIcon";
import {
  Button,
  EmptyState,
  ErrorBanner,
  Page,
  PageHeader,
  Skeleton,
  TextInput,
} from "../components/ui";

export function SearchView() {
  const openDetail = useDetail();
  const [q, setQ] = useState("");
  const [submitted, setSubmitted] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [results, setResults] = useState<SearchResult[]>([]);
  const [stats, setStats] = useState<SearchStats | null>(null);

  async function run() {
    const query = q.trim();
    if (!query) return;
    setLoading(true);
    setError(null);
    setSubmitted(true);
    try {
      const res = await api.search(query);
      setResults(res.results ?? []);
      setStats(res.search_stats ?? null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setResults([]);
      setStats(null);
    } finally {
      setLoading(false);
    }
  }

  const rows: RecordRow[] = results.map((r, i) => {
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

  return (
    <Page>
      <PageHeader
        title="Search"
        subtitle="Hybrid lexical + semantic search across everything you've indexed."
      />

      <form
        onSubmit={(e) => {
          e.preventDefault();
          run();
        }}
        className="mb-5 flex gap-2.5"
      >
        <TextInput
          type="search"
          autoFocus
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Keywords, a code, a date, or a question…"
        />
        <Button type="submit" disabled={loading || !q.trim()}>
          {loading ? "Searching…" : "Search"}
        </Button>
      </form>

      {error && <ErrorBanner message={`Search failed: ${error}`} onRetry={run} />}

      {stats && !loading && (
        <p className="mb-2 text-[13px] text-muted">
          <span className="tnum">{stats.total_results}</span> result
          {stats.total_results === 1 ? "" : "s"}
          {stats.search_duration_ms != null && (
            <>
              {" · "}
              <span className="tnum">{stats.search_duration_ms}</span> ms
            </>
          )}
        </p>
      )}

      {loading ? (
        <Skeleton rows={4} />
      ) : rows.length > 0 ? (
        <RecordList rows={rows} />
      ) : submitted && !error ? (
        <EmptyState
          title="No matches"
          hint="Try fewer or broader terms — or an exact code, amount, or date."
        />
      ) : null}
    </Page>
  );
}
