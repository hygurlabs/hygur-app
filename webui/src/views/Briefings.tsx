import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Sparkles, X, FolderKanban, StickyNote, Mail, FileText } from "lucide-react";
import { api } from "../lib/api";
import { fmtDateTime } from "../lib/format";
import type { Mention } from "../lib/types";
import { useDetail } from "../components/DetailPanel";
import {
  ContradictionList,
  useDismissContradiction,
  useOpenSource,
} from "../components/ContradictionList";
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
    badge: b.kind === "meeting_brief" ? "réunion" : "quotidien",
    meta: fmtDateTime(b.when || b.created_at),
    excerpt: b.content.replace(/[#*`>_]/g, "").slice(0, 180),
    onClick: () =>
      openDetail({
        title: b.title,
        meta: [
          b.kind === "meeting_brief" ? "synthèse de réunion" : "synthèse quotidienne",
          fmtDateTime(b.when || b.created_at),
        ],
        body: b.content,
      }),
  }));

  return (
    <Page>
      <PageHeader
        title="Synthèses"
        subtitle="Récapitulatifs quotidiens et les rappels que Hygur prépare avant les réunions et les échéances."
        actions={
          <Button onClick={() => setComposing((v) => !v)}>
            <Sparkles size={15} strokeWidth={1.9} />
            Nouvelle synthèse
          </Button>
        }
      />

      <BriefContradictions />

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
          message={`Impossible de charger les synthèses : ${(error as Error).message}`}
          onRetry={() => refetch()}
        />
      )}

      {isLoading ? (
        <Skeleton rows={4} />
      ) : rows.length > 0 ? (
        <RecordList rows={rows} />
      ) : (
        <EmptyState
          title="Aucune synthèse pour l’instant"
          hint="Utilisez « Nouvelle synthèse » pour en générer une maintenant, ou attendez le récapitulatif quotidien."
        />
      )}
    </Page>
  );
}

/** Contradiction callout inside the brief surface (placement: "in the daily
 *  brief"). Flags where two sources disagree, contextualized above the briefings.
 *  Renders nothing when there are none. */
function BriefContradictions() {
  const openSource = useOpenSource();
  const dismiss = useDismissContradiction();
  const { data } = useQuery({
    queryKey: ["claim-contradictions", ""],
    queryFn: () => api.claimContradictions(),
  });
  const items = data?.contradictions ?? [];
  if (items.length === 0) return null;
  return (
    <section className="mb-6 rounded-xl border border-danger/30 bg-danger/5 p-4">
      <h2 className="flex items-center gap-1.5 text-[11.5px] font-medium uppercase tracking-[0.09em] text-danger">
        <AlertTriangle size={13} strokeWidth={2} />
        Vos sources se contredisent
      </h2>
      <p className="mb-3 mt-1.5 text-[13px] text-muted">
        {items.length} point{items.length === 1 ? "" : "s"} où deux de vos sources se
        contredisent — à vérifier avant de vous y fier.
      </p>
      <ContradictionList
        items={items}
        onOpenSource={openSource}
        onDismiss={dismiss}
        limit={3}
      />
    </section>
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
          Nouvelle synthèse
        </span>
        <button
          onClick={onClose}
          aria-label="Fermer"
          className="rounded-md p-1 text-muted transition-colors hover:bg-surface2 hover:text-text"
        >
          <X size={15} strokeWidth={1.75} />
        </button>
      </div>

      <textarea
        value={instructions}
        onChange={(e) => setInstructions(e.target.value)}
        rows={2}
        placeholder="Sur quoi cette synthèse doit-elle se concentrer ? (texte libre)"
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
                aria-label="Retirer"
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
          placeholder="Ajouter du contexte — projets, notes, courriers, documents…"
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
        <ErrorBanner message={`Impossible de démarrer la synthèse : ${(run.error as Error).message}`} />
      )}

      <div className="flex justify-end">
        <Button onClick={() => run.mutate()} disabled={!canRun || run.isPending}>
          {run.isPending ? "Démarrage…" : "Générer la synthèse"}
        </Button>
      </div>
    </div>
  );
}
