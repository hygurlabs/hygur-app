import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  Check,
  FolderKanban,
  Gavel,
  RotateCcw,
  Sparkles,
  Trash2,
  X,
} from "lucide-react";
import { api } from "../lib/api";
import { fmtDate } from "../lib/format";
import type { Decision } from "../lib/types";
import { useOpenSource } from "../components/ContradictionList";
import { Button, EmptyState, ErrorBanner, Page, PageHeader, Skeleton } from "../components/ui";

export function Decisions() {
  const qc = useQueryClient();
  const openSource = useOpenSource();

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["decisions"],
    queryFn: () => api.decisions(),
  });
  const projectsQ = useQuery({ queryKey: ["projects"], queryFn: () => api.projects() });
  const projectName = (id?: string) =>
    id ? projectsQ.data?.find((p) => p.id === id)?.name : undefined;

  const invalidate = () => qc.invalidateQueries({ queryKey: ["decisions"] });

  // Manual log form.
  const [statement, setStatement] = useState("");
  const [rationale, setRationale] = useState("");
  const [projectId, setProjectId] = useState("");
  const [decidedOn, setDecidedOn] = useState("");
  const create = useMutation({
    mutationFn: () =>
      api.createDecision({
        statement: statement.trim(),
        rationale: rationale.trim() || undefined,
        project_id: projectId || undefined,
        decided_on: decidedOn ? new Date(decidedOn).toISOString() : undefined,
      }),
    onSuccess: () => {
      setStatement("");
      setRationale("");
      setProjectId("");
      setDecidedOn("");
      invalidate();
    },
  });

  const confirm = useMutation({
    mutationFn: (id: string) => api.updateDecision(id, { status: "standing" }),
    onSuccess: invalidate,
  });
  const supersede = useMutation({
    mutationFn: ({ id, to }: { id: string; to: string }) =>
      api.updateDecision(id, { status: to }),
    onSuccess: invalidate,
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.deleteDecision(id),
    onSuccess: invalidate,
  });

  // The scan calls the LLM in the background (202) — flag it and refetch as
  // proposals land.
  const [scanning, setScanning] = useState(false);
  const scan = useMutation({
    mutationFn: () => api.scanDecisions(),
    onSuccess: () => {
      setScanning(true);
      window.setTimeout(() => invalidate(), 8000);
      window.setTimeout(() => invalidate(), 20000);
      window.setTimeout(() => setScanning(false), 22000);
    },
  });

  const decisions = data?.decisions ?? [];
  const proposed = decisions.filter((d) => d.status === "proposed");
  const settled = decisions.filter((d) => d.status !== "proposed");

  const sourceChips = (d: Decision) =>
    d.source_refs.length > 0 && (
      <span className="inline-flex flex-wrap items-center gap-1">
        {d.source_refs.map((ref, i) => (
          <button
            key={ref}
            onClick={() => openSource(ref, `Source [${i + 1}]`)}
            title="Ouvrir la source"
            className="tnum grid size-[18px] place-items-center rounded border border-border text-[11px] text-muted transition-colors hover:border-accent hover:text-accent"
          >
            {i + 1}
          </button>
        ))}
      </span>
    );

  return (
    <Page>
      <PageHeader
        title="Décisions"
        subtitle="Vos décisions et engagements, conservés comme des enregistrements à part entière — ancrés dans vos propres mails et notes, et réexaminables. Hygur propose celles qu’il repère ; vous confirmez."
        actions={
          <Button onClick={() => scan.mutate()} disabled={scan.isPending || scanning}>
            <Sparkles size={15} strokeWidth={1.9} />
            {scanning ? "Analyse…" : "Analyser maintenant"}
          </Button>
        }
      />

      {scanning && (
        <p className="mb-3 text-[12.5px] text-muted">
          Analyse de vos enregistrements récents à la recherche de décisions — les propositions apparaîtront ici sous peu.
        </p>
      )}

      {error && (
        <ErrorBanner
          message={`Impossible de charger les décisions : ${(error as Error).message}`}
          onRetry={() => refetch()}
        />
      )}

      {/* Log a decision (manual) */}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (statement.trim()) create.mutate();
        }}
        className="mb-6 rounded-xl border border-border bg-surface p-3.5"
      >
        {/* Decision — the primary field. Labelled and chromed so it reads as
            the input to fill, not the quieter rationale below it. */}
        <label
          htmlFor="decision-statement"
          className="mb-1.5 block text-[11px] font-medium uppercase tracking-[0.08em] text-faint"
        >
          Décision
        </label>
        <input
          id="decision-statement"
          value={statement}
          onChange={(e) => setStatement(e.target.value)}
          placeholder="Qu’avez-vous décidé ?"
          className="w-full rounded-lg border border-border bg-bg px-3 py-2.5 text-[15px] font-medium text-text outline-none transition-colors placeholder:font-normal placeholder:text-faint focus:border-accent"
        />
        <textarea
          value={rationale}
          onChange={(e) => setRationale(e.target.value)}
          rows={2}
          placeholder="Pourquoi (facultatif) — le raisonnement que vous voudrez retrouver en y revenant."
          className="mt-2 w-full resize-y rounded-lg border border-border/60 bg-transparent px-2.5 py-1.5 text-[13px] text-muted outline-none placeholder:text-faint focus:border-accent"
        />
        <div className="mt-2.5 flex flex-wrap items-center gap-2.5">
          <select
            value={projectId}
            onChange={(e) => setProjectId(e.target.value)}
            className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[13px] outline-none focus:border-accent"
          >
            <option value="">Aucun projet</option>
            {(projectsQ.data ?? []).map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
          <input
            type="date"
            value={decidedOn}
            onChange={(e) => setDecidedOn(e.target.value)}
            title="Date de la décision"
            className="rounded-lg border border-border bg-surface px-2 py-1.5 text-[13px] text-text outline-none focus:border-accent"
          />
          <div className="ml-auto">
            <Button type="submit" disabled={!statement.trim() || create.isPending}>
              <Gavel size={15} strokeWidth={1.9} />
              {create.isPending ? "Consignation…" : "Consigner la décision"}
            </Button>
          </div>
        </div>
        {create.error && (
          <p className="mt-2 text-[12px] text-danger">
            Impossible de la consigner : {(create.error as Error).message}
          </p>
        )}
      </form>

      {isLoading ? (
        <Skeleton rows={5} />
      ) : decisions.length === 0 ? (
        <EmptyState
          title="Aucune décision pour l’instant"
          hint="Consignez-en une ci-dessus, ou cliquez sur « Analyser maintenant » pour laisser Hygur faire ressortir les décisions de vos mails et notes récents."
        />
      ) : (
        <>
          {/* Proposed — awaiting confirmation */}
          {proposed.length > 0 && (
            <section className="mb-7">
              <h2 className="mb-2 text-[12px] font-medium uppercase tracking-[0.09em] text-faint">
                Proposées · {proposed.length}
              </h2>
              <ul className="divide-y divide-border rounded-xl border border-accent/40 bg-accent-weak/20">
                {proposed.map((d) => (
                  <li key={d.id} className="flex items-start gap-3 px-3.5 py-3">
                    <div className="min-w-0 flex-1">
                      <p className="text-[14px] text-text">{d.statement}</p>
                      <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px] text-muted">
                        {d.decided_on && <span className="tnum">{fmtDate(d.decided_on)}</span>}
                        {projectName(d.project_id) && (
                          <span className="inline-flex items-center gap-1">
                            <FolderKanban size={12} strokeWidth={1.75} />
                            {projectName(d.project_id)}
                          </span>
                        )}
                        {sourceChips(d)}
                      </div>
                    </div>
                    <div className="flex shrink-0 items-center gap-1.5">
                      <button
                        onClick={() => confirm.mutate(d.id)}
                        disabled={confirm.isPending}
                        title="Confirmer — c’est une vraie décision"
                        className="inline-flex items-center gap-1 rounded-md border border-accent bg-accent px-2 py-1 text-[12.5px] font-medium text-white transition-opacity hover:opacity-90"
                      >
                        <Check size={13} strokeWidth={2.25} /> Confirmer
                      </button>
                      <button
                        onClick={() => remove.mutate(d.id)}
                        disabled={remove.isPending}
                        aria-label="Écarter"
                        title="Écarter — ce n’est pas une décision"
                        className="rounded-md p-1.5 text-muted transition-colors hover:bg-danger/10 hover:text-danger"
                      >
                        <X size={15} strokeWidth={1.9} />
                      </button>
                    </div>
                  </li>
                ))}
              </ul>
            </section>
          )}

          {/* Standing + superseded */}
          {settled.length > 0 && (
            <section>
              <h2 className="mb-2 text-[12px] font-medium uppercase tracking-[0.09em] text-faint">
                Actées
              </h2>
              <ul className="divide-y divide-border rounded-xl border border-border bg-surface">
                {settled.map((d) => {
                  const superseded = d.status === "superseded";
                  return (
                    <li key={d.id} className="flex items-start gap-3 px-3.5 py-3">
                      <div className="min-w-0 flex-1">
                        <p
                          className={`text-[14px] ${
                            superseded ? "text-muted line-through" : "text-text"
                          }`}
                        >
                          {d.statement}
                        </p>
                        {d.rationale && (
                          <div className="prose-answer mt-1 text-[13px] text-muted [&_p]:mb-1">
                            <ReactMarkdown remarkPlugins={[remarkGfm]}>{d.rationale}</ReactMarkdown>
                          </div>
                        )}
                        {d.updates_statement && (
                          <p className="mt-1 flex items-start gap-1 text-[12px] text-muted">
                            <RotateCcw size={12} strokeWidth={1.75} className="mt-[2px] shrink-0" />
                            <span>
                              Met à jour votre décision antérieure :{" "}
                              <span className="text-text">{d.updates_statement}</span>
                            </span>
                          </p>
                        )}
                        <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px] text-muted">
                          {superseded && (
                            <span className="rounded bg-surface2 px-1.5 py-0.5 text-faint">
                              remplacée
                            </span>
                          )}
                          {d.decided_on && <span className="tnum">{fmtDate(d.decided_on)}</span>}
                          {projectName(d.project_id) && (
                            <span className="inline-flex items-center gap-1">
                              <FolderKanban size={12} strokeWidth={1.75} />
                              {projectName(d.project_id)}
                            </span>
                          )}
                          {sourceChips(d)}
                        </div>
                      </div>
                      <div className="flex shrink-0 items-center gap-1">
                        <button
                          onClick={() =>
                            supersede.mutate({ id: d.id, to: superseded ? "standing" : "superseded" })
                          }
                          disabled={supersede.isPending}
                          title={superseded ? "Réinstaurer cette décision" : "Marquer comme caduque"}
                          className="rounded-md p-1.5 text-muted transition-colors hover:bg-surface2 hover:text-text"
                        >
                          {superseded ? (
                            <RotateCcw size={15} strokeWidth={1.75} />
                          ) : (
                            <Gavel size={15} strokeWidth={1.75} />
                          )}
                        </button>
                        <button
                          onClick={() => remove.mutate(d.id)}
                          disabled={remove.isPending}
                          aria-label="Supprimer la décision"
                          className="rounded-md p-1.5 text-muted transition-colors hover:bg-danger/10 hover:text-danger"
                        >
                          <Trash2 size={15} strokeWidth={1.75} />
                        </button>
                      </div>
                    </li>
                  );
                })}
              </ul>
            </section>
          )}
        </>
      )}
    </Page>
  );
}
