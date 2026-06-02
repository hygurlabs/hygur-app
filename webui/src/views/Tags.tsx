import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { RecordList, type RecordRow } from "../components/RecordList";
import {
  EmptyState,
  ErrorBanner,
  Page,
  PageHeader,
  Skeleton,
} from "../components/ui";

export function Tags() {
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["tags"],
    queryFn: () => api.tags(),
  });

  const rows: RecordRow[] = useMemo(() => {
    const tags = [...(data?.tags ?? [])].sort(
      (a, b) => (b.usage_count ?? 0) - (a.usage_count ?? 0),
    );
    return tags.map((t) => ({
      id: t.id,
      title: t.name,
      accent: t.color,
      badge: t.is_auto ? "auto" : undefined,
      meta: String(t.usage_count ?? 0),
    }));
  }, [data]);

  return (
    <Page>
      <PageHeader
        title="Tags"
        subtitle="Automatic and manual tags, by how often they're used."
      />

      {error && (
        <ErrorBanner
          message={`Couldn't load tags: ${(error as Error).message}`}
          onRetry={() => refetch()}
        />
      )}

      {isLoading ? (
        <Skeleton rows={5} />
      ) : rows.length > 0 ? (
        <RecordList rows={rows} />
      ) : (
        <EmptyState
          title="No tags yet"
          hint="Tags appear as your mail and documents get classified."
        />
      )}
    </Page>
  );
}
