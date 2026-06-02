import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Sparkles, X, FolderKanban, StickyNote, Mail, FileText } from "lucide-react";
import { api } from "../lib/api";
import { fmtDateTime } from "../lib/format";
import type { Mention } from "../lib/types";
import { useDetail } from "../components/DetailPanel";
import { RecordList, type RecordRow } from "../components/RecordList";
import {
  Button,
  EmptyState,
  ErrorBanner,
  Page,
  PageHeader,
  Skeleton,
  TextInput,
} from "../components/ui";

interface ContextRef {
  id: string;
  type: Mention["type"];
  title: string;
}

export function Briefings() {
  const openDetail = useDetail();
  const qc = useQueryClient();
  const [composing, setComposing] = useState(false);

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["briefings"],
    queryFn: () => api.briefings(),
    refetchInterval: 30_000,
  });

  const briefings = data?.briefings ?? [];
  const rows: RecordRow[] = briefings.map((b) => ({
    id: b.content_id,
    title: b.title,
    badge: b.kind === "meeting_brief" ? "meeting" : "daily",
    meta: fmtDateTime(b.when || b.created_at),
    excerpt: b.content.replace(/[#*`>_]/g, "").slice(0, 180),
    onClick: () =>
      openDetail({
        title: b.title,
        meta: [
          b.kind === "meeting_brief" ? "meeting brief" : "daily brief",
          fmtDateTime(b.when || b.created_at),
        ],
        body: b.content,
      }),
  }));

  return (
    <Page>
      <PageHeader
        title="Briefings"
        subtitle="Daily digests and the heads-up Hygur prepares before meetings and deadlines."
        actions={
          <Button onClick={() => setComposing((v) => !v)}>
            <Sparkles size={15} strokeWidth={1.9} />
            New briefing
          </Button>
        }
      />

      {composing && (
        <NewBriefingForm
          onClose={() => setComposing(false)}
          onQueued={() => {
            setComposing(false);
            // The brief is async; refetch shortly after it should have landed.
            window.setTimeout(() => qc.invalidateQueries({ queryKey: ["briefings"] }), 6000);
            window.setTimeout(() => qc.invalidateQueries({ queryKey: ["briefings"] }), 20000);
          }}
        />
      )}

      {error && (
        <ErrorBanner
          message={`Couldn't load briefings: ${(error as Error).message}`}
          onRetry={() => refetch()}
        />
      )}

      {isLoading ? (
        <Skeleton rows={4} />
      ) : rows.length > 0 ? (
        <RecordList rows={rows} />
      ) : (
        <EmptyState
          title="No briefings yet"
          hint="Use “New briefing” to generate one now, or wait for the daily digest."
        />
      )}
    </Page>
  );
}

function MentionGlyph({ type }: { type: Mention["type"] }) {
  const cls = "shrink-0 text-faint";
  if (type === "project") return <FolderKanban size={14} strokeWidth={1.75} className={cls} />;
  if (type === "note") return <StickyNote size={14} strokeWidth={1.75} className={cls} />;
  if (type === "mail") return <Mail size={14} strokeWidth={1.75} className={cls} />;
  return <FileText size={14} strokeWidth={1.75} className={cls} />;
}

function NewBriefingForm({
  onClose,
  onQueued,
}: {
  onClose: () => void;
  onQueued: () => void;
}) {
  const [instructions, setInstructions] = useState("");
  const [context, setContext] = useState<ContextRef[]>([]);
  const [query, setQuery] = useState("");

  // Reuse the @-mention search to pick projects / notes / mails / documents.
  const { data: mentionData } = useQuery({
    queryKey: ["mentions", query],
    queryFn: () => api.mentions(query),
    enabled: query.trim().length > 0,
  });
  const matches = (mentionData?.mentions ?? []).filter(
    (m) => m.type !== "tag" && !context.some((c) => c.id === m.id),
  );

  const run = useMutation({
    mutationFn: () =>
      api.runBrief({
        project_ids: context.filter((c) => c.type === "project").map((c) => c.id),
        content_ids: context.filter((c) => c.type !== "project").map((c) => c.id),
        instructions: instructions.trim() || undefined,
      }),
    onSuccess: onQueued,
  });

  const canRun = instructions.trim() !== "" || context.length > 0;

  return (
    <div className="mb-6 rounded-xl border border-border bg-surface p-4">
      <div className="mb-3 flex items-center justify-between">
        <span className="text-[11.5px] font-medium uppercase tracking-[0.09em] text-faint">
          New briefing
        </span>
        <button
          onClick={onClose}
          aria-label="Close"
          className="rounded-md p-1 text-muted transition-colors hover:bg-surface2 hover:text-text"
        >
          <X size={15} strokeWidth={1.75} />
        </button>
      </div>

      <textarea
        value={instructions}
        onChange={(e) => setInstructions(e.target.value)}
        rows={2}
        placeholder="What should this briefing focus on? (free text)"
        className="mb-3 w-full resize-y rounded-lg border border-border bg-bg px-3 py-2 text-[14px] outline-none focus:border-accent placeholder:text-faint"
      />

      {context.length > 0 && (
        <div className="mb-2 flex flex-wrap gap-1.5">
          {context.map((c) => (
            <span
              key={c.id}
              className="inline-flex items-center gap-1.5 rounded-full border border-border bg-surface2 py-1 pl-2.5 pr-1.5 text-[12.5px]"
            >
              <MentionGlyph type={c.type} />
              <span className="max-w-[200px] truncate">{c.title}</span>
              <button
                onClick={() => setContext((prev) => prev.filter((x) => x.id !== c.id))}
                aria-label="Remove"
                className="rounded-full p-0.5 text-faint hover:text-danger"
              >
                <X size={12} strokeWidth={2} />
              </button>
            </span>
          ))}
        </div>
      )}

      <div className="relative mb-3">
        <TextInput
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Add context — projects, notes, mails, documents…"
        />
        {query.trim() && matches.length > 0 && (
          <ul className="absolute z-30 mt-1 max-h-56 w-full overflow-auto rounded-lg border border-border bg-surface py-1 shadow-lg">
            {matches.map((m) => (
              <li key={`${m.type}-${m.id}`}>
                <button
                  onClick={() => {
                    setContext((prev) => [...prev, { id: m.id, type: m.type, title: m.title }]);
                    setQuery("");
                  }}
                  className="flex w-full items-center gap-2.5 px-3 py-2 text-left text-[13.5px] transition-colors hover:bg-accent-weak/60"
                >
                  <MentionGlyph type={m.type} />
                  <span className="truncate">{m.title}</span>
                  <span className="ml-auto text-[11px] uppercase tracking-wide text-faint">
                    {m.type}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      {run.error && (
        <ErrorBanner message={`Couldn't start briefing: ${(run.error as Error).message}`} />
      )}

      <div className="flex justify-end">
        <Button onClick={() => run.mutate()} disabled={!canRun || run.isPending}>
          {run.isPending ? "Starting…" : "Generate briefing"}
        </Button>
      </div>
    </div>
  );
}
