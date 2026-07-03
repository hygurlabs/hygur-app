import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import { ArrowLeft, ArrowUpRight, AlertTriangle } from "lucide-react";
import { api } from "../lib/api";
import { fmtDate } from "../lib/format";
import type { Engram, SubjectStat } from "../lib/types";
import { RecordList, type RecordRow } from "../components/RecordList";
import { useDetail } from "../components/DetailPanel";
import {
  Page,
  PageHeader,
  Skeleton,
  EmptyState,
  ErrorBanner,
  Button,
  Badge,
  StatusBadge,
  ToggleGroup,
  TextInput,
  type StatusVariant,
} from "../components/ui";

/** normKey mirrors the server's contradict.NormKey: lowercase, accents stripped,
 *  reduced to space-separated alphanumeric words. Best-effort, for matching the
 *  friction set's raw entity strings against the subjects' normalized norms. */
function normKey(s: string): string {
  return s
    .normalize("NFD")
    .replace(/\p{Diacritic}/gu, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, " ")
    .trim()
    .replace(/\s+/g, " ");
}

type TypeFilter = "person" | "org" | "project" | "other";
type SortKey = "mentions" | "recent";

const TYPE_LABEL: Record<TypeFilter, string> = {
  person: "People",
  org: "Orgs",
  project: "Projects",
  other: "Subjects",
};

/** My World (WP36.b) — the interactive navigator of the subjects Hygur knows.
 *  Search + type filters + sort, drilling into the canonical dossier (WP36.a/c/d). */
export function World() {
  const { norm } = useParams<{ norm: string }>();
  if (norm) return <Dossier norm={decodeURIComponent(norm)} />;
  return <SubjectList />;
}

function SubjectList() {
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [types, setTypes] = useState<Set<TypeFilter>>(new Set());
  const [sort, setSort] = useState<SortKey>("mentions");

  const { data, isLoading, error } = useQuery({
    queryKey: ["engrams"],
    queryFn: () => api.engrams(),
  });
  // Open frictions per subject — the set of entities with an open reconciled
  // contradiction, so a card can flag "open friction" (WP36.b).
  const { data: frictions } = useQuery({
    queryKey: ["claim-contradictions"],
    queryFn: () => api.claimContradictions(),
  });
  const frictionNorms = useMemo(() => {
    const s = new Set<string>();
    for (const c of frictions?.contradictions ?? []) {
      if (!c.dismissed && c.verdict?.kind === "conflict" && c.entity) {
        s.add(normKey(c.entity));
      }
    }
    return s;
  }, [frictions]);

  const subjects = useMemo(() => data?.subjects ?? [], [data]);
  const shown = useMemo(() => {
    const q = normKey(query);
    const bucket = (t?: string): TypeFilter =>
      t === "person" || t === "org" || t === "project" ? t : "other";
    let list = subjects.filter((s) => {
      if (types.size > 0 && !types.has(bucket(s.type))) return false;
      if (!q) return true;
      return normKey(s.norm).includes(q) || (s.raw ? normKey(s.raw).includes(q) : false);
    });
    list = [...list].sort((a, b) =>
      sort === "recent"
        ? (b.last_activity ?? "").localeCompare(a.last_activity ?? "")
        : b.mentions - a.mentions,
    );
    return list;
  }, [subjects, query, types, sort]);

  const toggleType = (t: TypeFilter) =>
    setTypes((prev) => {
      const next = new Set(prev);
      if (next.has(t)) next.delete(t);
      else next.add(t);
      return next;
    });

  return (
    <Page>
      <PageHeader
        title="My World"
        subtitle="Everyone and everything Hygur knows — type a name to open its dossier: identity, beliefs, commitments, frictions and history, assembled."
      />

      <div className="mb-4">
        <TextInput
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search a person, an organization, a project…"
        />
      </div>
      <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
        <ToggleGroup<TypeFilter>
          variant="chips"
          value={[...types]}
          onChange={toggleType}
          ariaLabel="Filter by type"
          options={(["person", "org", "project", "other"] as TypeFilter[]).map((t) => ({
            value: t,
            label: TYPE_LABEL[t],
          }))}
        />
        <ToggleGroup<SortKey>
          variant="segmented"
          size="sm"
          value={sort}
          onChange={setSort}
          ariaLabel="Sort subjects"
          options={[
            { value: "mentions", label: "Most mentioned" },
            { value: "recent", label: "Recent" },
          ]}
        />
      </div>

      {isLoading ? (
        <Skeleton rows={6} />
      ) : error ? (
        <ErrorBanner message="Could not load subjects." />
      ) : subjects.length === 0 ? (
        <EmptyState
          title="No subjects yet"
          hint="Named entities surface here as Hygur indexes your mail and notes."
        />
      ) : shown.length === 0 ? (
        <EmptyState title="No matches" hint="Try a different name or clear the filters." />
      ) : (
        <div className="flex flex-col gap-2">
          {shown.map((s) => (
            <SubjectCard
              key={s.norm}
              subject={s}
              friction={frictionNorms.has(normKey(s.norm))}
              onClick={() => navigate(`/world/${encodeURIComponent(s.norm)}`)}
            />
          ))}
        </div>
      )}
    </Page>
  );
}

function SubjectCard({
  subject,
  friction,
  onClick,
}: {
  subject: SubjectStat;
  friction: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className="group flex items-center justify-between gap-3 rounded-xl border border-border bg-surface px-4 py-3 text-left transition-colors hover:border-accent"
    >
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <span className="truncate font-medium text-text">{subject.norm}</span>
          <Badge>{subject.type || "subject"}</Badge>
          {friction && (
            <StatusBadge variant="contradiction" icon={<AlertTriangle size={11} strokeWidth={2} />}>
              open friction
            </StatusBadge>
          )}
        </div>
        <div className="mt-0.5 flex flex-wrap gap-x-3 text-[12px] text-muted">
          <span className="tnum">
            ×{subject.mentions} mention{subject.mentions === 1 ? "" : "s"}
          </span>
          {subject.last_activity && (
            <span>last sign of life {fmtDate(subject.last_activity)}</span>
          )}
        </div>
      </div>
      <ArrowUpRight
        size={16}
        strokeWidth={1.9}
        className="shrink-0 text-faint transition-colors group-hover:text-accent"
      />
    </button>
  );
}

/** Section header for a dossier block. */
function Block({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <section className="mb-6">
      <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted">
        {label}
      </h3>
      {children}
    </section>
  );
}

function Dossier({ norm }: { norm: string }) {
  const navigate = useNavigate();
  const openDetail = useDetail();
  const [register, setRegister] = useState<1 | 2>(1);
  const { data, isLoading, error } = useQuery<Engram>({
    queryKey: ["engram", norm],
    queryFn: () => api.engram(norm),
  });

  const back = (
    <Button variant="ghost" onClick={() => navigate("/world")}>
      <ArrowLeft size={15} strokeWidth={1.9} /> My World
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

  const identity = data.identity ?? [];
  const claims = data.claims ?? [];
  const decisions = data.decisions ?? [];
  const contradictions = data.contradictions ?? [];
  const network = data.network ?? [];
  const timeline = data.timeline ?? [];

  // Timeline split into the two registers (WP36.d): Direct (order 1) / Related (order 2).
  const inRegister = timeline.filter((it) =>
    register === 2 ? it.order === 2 : it.order !== 2,
  );

  // Open a timeline item in the shared DetailPanel (WP36.c, R4), carrying its psyché
  // facets and prev/next navigation within the active register.
  const openItem = (idx: number) => {
    const it = inRegister[idx];
    if (!it) return;
    openDetail({
      title: it.title || "(untitled)",
      contentId: it.content_id,
      sourceType: it.source_type,
      meta: [
        it.source_type,
        it.date_missing ? "no date" : fmtDate(it.date),
        it.via_neighbor ? `via ${it.via_neighbor}` : "",
      ].filter(Boolean),
      body: "",
      facets: {
        subject: norm,
        closed: it.closed,
        contradicted: it.contradicted,
        decisionStatus: it.decision_status,
        order: it.order,
        viaNeighbor: it.via_neighbor,
      },
      onPrev: idx > 0 ? () => openItem(idx - 1) : undefined,
      onNext: idx < inRegister.length - 1 ? () => openItem(idx + 1) : undefined,
    });
  };

  const tlRows: RecordRow[] = inRegister.map((it, idx) => {
    const badgeVariant: StatusVariant | undefined = it.closed
      ? "closed"
      : it.contradicted
        ? "contradiction"
        : it.decision_status
          ? "decision"
          : undefined;
    return {
      id: it.content_id,
      title: it.title || "(untitled)",
      badge: it.closed
        ? "closed"
        : it.contradicted
          ? "contradiction"
          : it.decision_status || it.source_type,
      badgeVariant,
      meta: [
        it.date_missing ? "no date" : fmtDate(it.date),
        it.via_neighbor ? `via ${it.via_neighbor}` : "",
      ]
        .filter(Boolean)
        .join(" · "),
      onClick: () => openItem(idx),
    };
  });

  return (
    <Page>
      <PageHeader
        title={norm}
        subtitle={`${data.subject.type} · ${network.length} in the network · ${timeline.length} memories`}
        actions={back}
      />

      {identity.length > 0 && (
        <Block label="Identity">
          <div className="flex flex-col gap-2">
            {identity.map((id) => (
              <div
                key={id.type}
                className="flex flex-wrap items-baseline gap-x-2 gap-y-1 text-[14px]"
              >
                <span className="text-muted">{id.label}</span>
                <span className="tnum font-medium text-text">{id.value || id.raw}</span>
                <StatusBadge variant={id.tier === "high" ? "decision" : "superseded"}>
                  {id.tier}
                </StatusBadge>
              </div>
            ))}
          </div>
        </Block>
      )}

      {claims.length > 0 && (
        <Block label="Beliefs">
          <div className="flex flex-col gap-2">
            {claims.map((c) => (
              <div
                key={c.attribute}
                className="flex flex-wrap items-baseline gap-x-2 gap-y-1 text-[14px]"
              >
                <span className="text-muted">{c.attribute}</span>
                <span className="text-text">{c.value}</span>
                {c.state === "contested" ? (
                  <StatusBadge variant="contradiction">contested</StatusBadge>
                ) : (
                  <span className="tnum text-[12px] text-faint">
                    ×{c.corroboration} source{c.corroboration === 1 ? "" : "s"}
                  </span>
                )}
              </div>
            ))}
          </div>
        </Block>
      )}

      {decisions.length > 0 && (
        <Block label="Commitments">
          <RecordList
            rows={decisions.map((d) => ({
              id: d.content_id,
              title: d.title || "(untitled)",
              badge: d.decision_status,
              badgeVariant: "decision" as StatusVariant,
              meta: d.date_missing ? "no date" : fmtDate(d.date),
            }))}
          />
        </Block>
      )}

      {contradictions.length > 0 && (
        <Block label="Frictions">
          <RecordList
            rows={contradictions.map((c) => ({
              id: c.content_id,
              title: c.title || "(untitled)",
              badge: "contradiction",
              badgeVariant: "contradiction" as StatusVariant,
              meta: c.date_missing ? "no date" : fmtDate(c.date),
            }))}
          />
        </Block>
      )}

      {network.length > 0 && (
        <Block label="Network">
          <div className="flex flex-wrap gap-2">
            {network.map((n) => (
              <button
                key={n.norm}
                onClick={() => navigate(`/world/${encodeURIComponent(n.norm)}`)}
                className="rounded-full border border-border bg-surface2 px-2.5 py-1 text-[12px] text-text transition-colors hover:border-accent hover:text-accent"
              >
                {n.norm}
                {(n.label || n.type) && (
                  <span className="text-muted"> · {n.label || n.type}</span>
                )}
              </button>
            ))}
          </div>
        </Block>
      )}

      <Block label="History">
        <div className="mb-3">
          <ToggleGroup<"direct" | "related">
            variant="segmented"
            size="sm"
            value={register === 2 ? "related" : "direct"}
            onChange={(v) => setRegister(v === "related" ? 2 : 1)}
            ariaLabel="Timeline register"
            options={[
              { value: "direct", label: "Direct" },
              { value: "related", label: "Related" },
            ]}
          />
        </div>
        {tlRows.length === 0 ? (
          <EmptyState
            title={register === 2 ? "Nothing related" : "No direct history"}
            hint={
              register === 2
                ? "No memories reach this subject through its network."
                : "This subject has no consolidated timeline yet."
            }
          />
        ) : (
          <RecordList rows={tlRows} />
        )}
      </Block>
    </Page>
  );
}
