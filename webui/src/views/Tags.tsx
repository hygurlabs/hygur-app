import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2, ChevronLeft, ChevronRight } from "lucide-react";
import { api } from "../lib/api";
import { srcLabel } from "../lib/format";
import type { Tag } from "../lib/types";
import { useOpenSource } from "../components/ContradictionList";
import { RecordList, type RecordRow } from "../components/RecordList";
import { SourceIcon } from "../components/SourceIcon";
import {
  Badge,
  EmptyState,
  ErrorBanner,
  Page,
  PageHeader,
  Skeleton,
} from "../components/ui";

export function Tags() {
  const qc = useQueryClient();
  const [confirmId, setConfirmId] = useState<string | null>(null);
  // When set, drill into a tag's items instead of the tag list.
  const [selected, setSelected] = useState<{ id: string; name: string } | null>(null);

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["tags"],
    queryFn: () => api.tags(),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.deleteTag(id),
    onSuccess: () => {
      setConfirmId(null);
      // Tags drive notes/items/mentions — refresh the lot.
      qc.invalidateQueries({ queryKey: ["tags"] });
      qc.invalidateQueries({ queryKey: ["notes"] });
      qc.invalidateQueries({ queryKey: ["knowledge-items"] });
    },
  });

  const tags: Tag[] = useMemo(
    () =>
      [...(data?.tags ?? [])].sort(
        (a, b) => (b.usage_count ?? 0) - (a.usage_count ?? 0),
      ),
    [data],
  );

  if (selected) {
    return <TagItems tag={selected} onBack={() => setSelected(null)} />;
  }

  return (
    <Page>
      <PageHeader
        title="Tags"
        subtitle="Automatic and manual tags, by how often they're used. Click one to see its items."
      />

      {error && (
        <ErrorBanner
          message={`Couldn't load tags: ${(error as Error).message}`}
          onRetry={() => refetch()}
        />
      )}
      {remove.error && (
        <ErrorBanner
          message={`Couldn't delete the tag: ${(remove.error as Error).message}`}
        />
      )}

      {isLoading ? (
        <Skeleton rows={5} />
      ) : tags.length > 0 ? (
        <ul className="border-t border-border">
          {tags.map((t) => (
            <li
              key={t.id}
              className="group flex items-center gap-3 border-b border-border px-1 py-3.5"
            >
              <span
                aria-hidden
                className="size-2.5 shrink-0 rounded-full"
                style={{ background: t.color || "#3B82F6" }}
              />
              <button
                onClick={() => setSelected({ id: t.id, name: t.name })}
                className="flex min-w-0 flex-1 items-center gap-1.5 truncate text-left font-medium transition-colors hover:text-accent"
              >
                <span className="truncate">{t.name}</span>
                <ChevronRight
                  size={14}
                  strokeWidth={2}
                  className="shrink-0 text-faint opacity-0 transition-opacity group-hover:opacity-100"
                />
              </button>
              {t.is_auto && <Badge>auto</Badge>}
              <span className="tnum w-8 text-right text-[12.5px] text-muted">
                {t.usage_count ?? 0}
              </span>
              {confirmId === t.id ? (
                <span className="flex items-center gap-1.5 text-[12.5px]">
                  <span className="text-muted">Supprimer&nbsp;?</span>
                  <button
                    onClick={() => remove.mutate(t.id)}
                    disabled={remove.isPending}
                    className="rounded-md px-2 py-0.5 font-medium text-danger transition-colors hover:bg-danger/10"
                  >
                    {remove.isPending ? "…" : "Oui"}
                  </button>
                  <button
                    onClick={() => setConfirmId(null)}
                    className="rounded-md px-2 py-0.5 text-muted transition-colors hover:bg-surface2 hover:text-text"
                  >
                    Annuler
                  </button>
                </span>
              ) : (
                <button
                  onClick={() => setConfirmId(t.id)}
                  aria-label={`Delete tag ${t.name}`}
                  className="rounded-md p-1 text-faint opacity-0 transition-all hover:bg-danger/10 hover:text-danger focus:opacity-100 group-hover:opacity-100"
                >
                  <Trash2 size={15} strokeWidth={1.75} />
                </button>
              )}
            </li>
          ))}
        </ul>
      ) : (
        <EmptyState
          title="No tags yet"
          hint="Tags appear as your mail and documents get classified."
        />
      )}
    </Page>
  );
}

/** The items carrying a tag — click one to open it in the detail panel. */
function TagItems({
  tag,
  onBack,
}: {
  tag: { id: string; name: string };
  onBack: () => void;
}) {
  const openSource = useOpenSource();
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["tag-items", tag.id],
    queryFn: () => api.tagItems(tag.id),
  });
  const items = data?.items ?? [];
  const rows: RecordRow[] = items.map((it) => ({
    id: it.id,
    title: it.title || "(untitled)",
    icon: <SourceIcon type={it.source_type} />,
    badge: srcLabel(it.source_type),
    onClick: () => openSource(it.id, it.title),
  }));

  return (
    <Page>
      <button
        onClick={onBack}
        className="mb-4 inline-flex items-center gap-1 text-[13px] text-muted transition-colors hover:text-text"
      >
        <ChevronLeft size={15} strokeWidth={2} />
        Tags
      </button>
      <PageHeader
        title={tag.name}
        subtitle={`${items.length} item${items.length === 1 ? "" : "s"} with this tag.`}
      />
      {error && (
        <ErrorBanner
          message={`Couldn't load items: ${(error as Error).message}`}
          onRetry={() => refetch()}
        />
      )}
      {isLoading ? (
        <Skeleton rows={5} />
      ) : rows.length > 0 ? (
        <RecordList rows={rows} />
      ) : (
        <EmptyState title="No items" hint="Nothing carries this tag yet." />
      )}
    </Page>
  );
}
