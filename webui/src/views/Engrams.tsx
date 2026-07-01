import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import { api } from "../lib/api";
import { fmtDate } from "../lib/format";
import type { Engram } from "../lib/types";
import { RecordList, type RecordRow } from "../components/RecordList";
import {
  Page,
  PageHeader,
  Skeleton,
  EmptyState,
  ErrorBanner,
  Button,
} from "../components/ui";

/** Engrams — the discovered subjects and the memory Hygur consolidated around each.
 *  A subject list drills into a dossier: NPMI network + strength-ordered timeline. */
export function Engrams() {
  const [selected, setSelected] = useState<string | null>(null);
  const { data, isLoading, error } = useQuery({
    queryKey: ["engrams"],
    queryFn: () => api.engrams(),
  });

  if (selected) {
    return <Dossier norm={selected} onBack={() => setSelected(null)} onOpen={setSelected} />;
  }

  const subjects = data?.subjects ?? [];
  const rows: RecordRow[] = subjects.map((s) => ({
    id: s.norm,
    title: s.norm,
    badge: s.type || "claim",
    meta: `${s.mentions} mention${s.mentions === 1 ? "" : "s"}`,
    onClick: () => setSelected(s.norm),
  }));

  return (
    <Page>
      <PageHeader
        title="Engrams"
        subtitle="Discovered subjects — people, organizations and projects — and the memory Hygur has consolidated around each."
      />
      {isLoading ? (
        <Skeleton rows={6} />
      ) : error ? (
        <ErrorBanner message="Could not load subjects." />
      ) : subjects.length === 0 ? (
        <EmptyState
          title="No subjects yet"
          hint="Named entities surface here as Hygur indexes your mail and notes."
        />
      ) : (
        <RecordList rows={rows} />
      )}
    </Page>
  );
}

function Dossier({
  norm,
  onBack,
  onOpen,
}: {
  norm: string;
  onBack: () => void;
  onOpen: (n: string) => void;
}) {
  const { data, isLoading, error } = useQuery<Engram>({
    queryKey: ["engram", norm],
    queryFn: () => api.engram(norm),
  });
  const back = (
    <Button variant="ghost" onClick={onBack}>
      <ArrowLeft size={15} strokeWidth={1.9} /> Subjects
    </Button>
  );

  if (isLoading) {
    return (
      <Page>
        <PageHeader title={norm} actions={back} />
        <Skeleton rows={6} />
      </Page>
    );
  }
  if (error || !data) {
    return (
      <Page>
        <PageHeader title={norm} actions={back} />
        <ErrorBanner message="No dossier for this subject." />
      </Page>
    );
  }

  const tl = data.timeline ?? [];
  const rows: RecordRow[] = tl.map((it) => ({
    id: it.content_id,
    title: it.title || "(untitled)",
    badge: it.closed
      ? "closed"
      : it.contradicted
        ? "contradiction"
        : it.decision_status || (it.order === 2 ? "linked" : it.source_type),
    meta: [
      it.date_missing ? "no date" : fmtDate(it.date),
      it.via_neighbor ? `via ${it.via_neighbor}` : "",
    ]
      .filter(Boolean)
      .join(" · "),
  }));

  return (
    <Page>
      <PageHeader
        title={norm}
        subtitle={`${data.subject.type} · ${data.network.length} in the network · ${tl.length} memories`}
        actions={back}
      />
      {data.network.length > 0 && (
        <div className="mb-6 flex flex-wrap gap-2">
          {data.network.map((n) => (
            <button
              key={n.norm}
              onClick={() => onOpen(n.norm)}
              className="rounded-full border border-border bg-surface2 px-2.5 py-1 text-[12px] text-text transition-colors hover:border-accent hover:text-accent"
            >
              {n.norm}
              {n.type && <span className="text-muted"> · {n.type}</span>}
            </button>
          ))}
        </div>
      )}
      {tl.length === 0 ? (
        <EmptyState title="No memories" hint="This subject has no consolidated timeline yet." />
      ) : (
        <RecordList rows={rows} />
      )}
    </Page>
  );
}
