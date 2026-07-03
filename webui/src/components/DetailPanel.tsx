import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import {
  X,
  MessageSquareText,
  ListPlus,
  Reply,
  Copy,
  Check,
  ChevronUp,
  ChevronDown,
} from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { ItemMeta } from "./ItemMeta";
import { StatusBadge, type StatusVariant } from "./ui";
import { api } from "../lib/api";
import { useToast } from "../lib/toast";

/** The item's psyché facets (WP36.c, R4) — what the dossier already knows about the
 *  item: which subject it belongs to, its thread-closure/contradiction/decision state,
 *  and whether it reached the subject directly (order 1) or via a neighbor (order 2). */
export interface DetailFacets {
  subject?: string;
  closed?: boolean;
  contradicted?: boolean;
  decisionStatus?: string;
  order?: number;
  viaNeighbor?: string;
}

export interface DetailData {
  title: string;
  meta: string[];
  /** Item body. May be empty when opened from a dossier — the panel then lazy-loads
   *  the full text from GET /knowledge/{id} using contentId. */
  body: string;
  /** When set, the panel shows "Ask in chat" + "Create task" (and "Draft reply"
   *  for mail) and loads the item's project/tags. */
  contentId?: string;
  /** Item source type — enables the mail-only "Draft reply" action. */
  sourceType?: string;
  /** Psyché facets from the opening dossier (WP36.c). */
  facets?: DetailFacets;
  /** Timeline prev/next navigation (WP36.c) — undefined disables the control. */
  onPrev?: () => void;
  onNext?: () => void;
  /** Optional actions rendered in the panel header (e.g. Edit, Delete). */
  actions?: ReactNode;
}

const OpenContext = createContext<(d: DetailData) => void>(() => {});

// eslint-disable-next-line react-refresh/only-export-components -- hook co-located with its provider (HMR-only rule; splitting it is needless churn)
export function useDetail() {
  return useContext(OpenContext);
}

const isMail = (st?: string) => st === "mail" || st === "email";

export function DetailPanelProvider({ children }: { children: ReactNode }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const [data, setData] = useState<DetailData | null>(null);
  const [taskDone, setTaskDone] = useState(false);
  const [taskError, setTaskError] = useState(false);
  const [draft, setDraft] = useState<string | null>(null);
  const [drafting, setDrafting] = useState(false);
  const [copied, setCopied] = useState(false);

  const open = useCallback((d: DetailData) => {
    setData(d);
    setTaskDone(false);
    setTaskError(false);
    setDraft(null);
    setDrafting(false);
    setCopied(false);
  }, []);
  const close = useCallback(() => setData(null), []);

  const askInChat = useCallback(() => {
    if (!data?.contentId) return;
    const params = new URLSearchParams({
      attach: data.contentId,
      attachTitle: data.title ?? "",
    });
    setData(null);
    navigate(`/?${params.toString()}`);
  }, [data, navigate]);

  const createTask = useCallback(async () => {
    if (!data) return;
    setTaskError(false);
    try {
      await api.createTask({
        title: data.title || "Untitled",
      });
      qc.invalidateQueries({ queryKey: ["tasks"] });
      setTaskDone(true);
    } catch (e) {
      setTaskError(true);
      toast.error(`Couldn't create the task: ${(e as Error).message}`);
    }
  }, [data, qc, toast]);

  const draftReply = useCallback(async () => {
    if (!data?.contentId) return;
    setDrafting(true);
    try {
      const r = await api.draftReply(data.contentId);
      setDraft(r.draft || "(no draft)");
    } catch {
      setDraft("Couldn't draft a reply right now.");
    } finally {
      setDrafting(false);
    }
  }, [data]);

  useEffect(() => {
    if (!data) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [data, close]);

  const isOpen = data !== null;

  return (
    <OpenContext.Provider value={open}>
      {children}

      <div
        onClick={close}
        aria-hidden
        className={`fixed inset-0 z-10 bg-text/25 transition-opacity duration-200 ${
          isOpen ? "opacity-100" : "pointer-events-none opacity-0"
        }`}
      />

      <aside
        role="dialog"
        aria-hidden={!isOpen}
        className={`fixed inset-y-0 right-0 z-20 flex w-[min(460px,94vw)] flex-col border-l border-border bg-surface transition-transform duration-200 ease-out ${
          isOpen ? "translate-x-0" : "translate-x-full"
        } motion-reduce:transition-none`}
      >
        {data && (
          <>
            <div className="flex items-start gap-3 border-b border-border px-5 pb-4 pt-[calc(1rem_+_env(safe-area-inset-top))]">
              <div className="min-w-0 flex-1">
                <h2 className="font-display text-[18px] font-medium leading-snug">
                  {data.title || "(untitled)"}
                </h2>
                {data.meta.length > 0 && (
                  <div className="mt-1.5 flex flex-wrap gap-x-3 gap-y-1 text-[12px] text-muted">
                    {data.meta.map((m, i) => (
                      <span key={i} className="tnum">
                        {m}
                      </span>
                    ))}
                  </div>
                )}
              </div>
              <div className="flex shrink-0 items-center gap-1">
                {data.actions}
                {(data.onPrev || data.onNext) && (
                  <div className="mr-1 flex items-center">
                    <button
                      onClick={data.onPrev}
                      disabled={!data.onPrev}
                      aria-label="Previous item"
                      className="rounded-md p-1 text-muted transition-colors hover:bg-surface2 hover:text-text disabled:opacity-30"
                    >
                      <ChevronUp size={18} strokeWidth={1.75} />
                    </button>
                    <button
                      onClick={data.onNext}
                      disabled={!data.onNext}
                      aria-label="Next item"
                      className="rounded-md p-1 text-muted transition-colors hover:bg-surface2 hover:text-text disabled:opacity-30"
                    >
                      <ChevronDown size={18} strokeWidth={1.75} />
                    </button>
                  </div>
                )}
                <button
                  onClick={close}
                  aria-label="Close"
                  className="-mr-1 rounded-md p-1 text-muted transition-colors hover:bg-surface2 hover:text-text"
                >
                  <X size={18} strokeWidth={1.75} />
                </button>
              </div>
            </div>

            {/* What Hygur can do with this item — the assistant's value, up front. */}
            {data.contentId && (
              <div className="flex flex-wrap items-center gap-2 border-b border-border px-5 py-3">
                <button
                  onClick={askInChat}
                  className="inline-flex min-h-9 items-center gap-1.5 rounded-lg bg-accent px-3.5 py-2 text-[13px] font-medium text-white shadow-[var(--shadow-soft)] transition-opacity hover:opacity-90"
                >
                  <MessageSquareText size={15} strokeWidth={2} />
                  Ask Hygur
                </button>
                {isMail(data.sourceType) && (
                  <button
                    onClick={draftReply}
                    disabled={drafting}
                    className="inline-flex min-h-9 items-center gap-1.5 rounded-lg bg-accent-weak px-3.5 py-2 text-[13px] font-medium text-accent transition-colors hover:bg-accent-weak/70 disabled:opacity-50"
                  >
                    <Reply size={15} strokeWidth={2} />
                    {drafting ? "Drafting…" : "Draft reply"}
                  </button>
                )}
                <button
                  onClick={createTask}
                  className={`inline-flex min-h-9 items-center gap-1.5 rounded-lg px-3.5 py-2 text-[13px] font-medium transition-colors ${
                    taskError
                      ? "bg-danger/10 text-danger hover:bg-danger/20"
                      : "bg-accent-weak text-accent hover:bg-accent-weak/70"
                  }`}
                >
                  {taskDone ? (
                    <Check size={15} strokeWidth={2.2} />
                  ) : (
                    <ListPlus size={15} strokeWidth={2} />
                  )}
                  {taskDone ? "Task added" : taskError ? "Couldn't add — retry" : "Make a task"}
                </button>
              </div>
            )}
            <div className="overflow-auto px-5 py-5">
              {data.facets && <PsycheFacets facets={data.facets} contentId={data.contentId} />}
              {data.contentId && <ItemMeta contentId={data.contentId} />}

              {draft !== null && (
                <div className="mb-5 rounded-xl border border-accent/30 bg-accent-weak/30 px-4 py-3">
                  <div className="mb-1.5 flex items-center justify-between">
                    <span className="text-[11px] font-medium uppercase tracking-wide text-accent">
                      Draft reply
                    </span>
                    <button
                      onClick={() => {
                        navigator.clipboard?.writeText(draft).then(
                          () => {
                            setCopied(true);
                            window.setTimeout(() => setCopied(false), 1500);
                          },
                          () => {},
                        );
                      }}
                      className="inline-flex items-center gap-1 text-[12px] text-muted transition-colors hover:text-accent"
                    >
                      {copied ? <Check size={13} strokeWidth={2} /> : <Copy size={13} strokeWidth={1.75} />}
                      {copied ? "Copied" : "Copy"}
                    </button>
                  </div>
                  <p className="whitespace-pre-line text-[13.5px] leading-relaxed text-text">
                    {draft}
                  </p>
                </div>
              )}

              <DetailBody body={data.body} contentId={data.contentId} />
            </div>
          </>
        )}
      </aside>
    </OpenContext.Provider>
  );
}

/** Item body: renders the passed text, or lazy-loads the full text via GET
 *  /knowledge/{id} when opened from a dossier with no body in hand. */
function DetailBody({ body, contentId }: { body: string; contentId?: string }) {
  const need = !body && !!contentId;
  const { data, isLoading } = useQuery({
    queryKey: ["kb-item", contentId],
    queryFn: () => api.knowledgeItem(contentId as string),
    enabled: need,
  });
  const text = body || data?.normalized_text || "";
  if (need && isLoading && !text) {
    return <p className="text-[13px] text-muted">Loading…</p>;
  }
  return (
    <div className="prose-answer text-[14px] leading-relaxed text-text">
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{text || "_(empty)_"}</ReactMarkdown>
    </div>
  );
}

interface RawClaim {
  attribute?: string;
  value?: string;
  polarity?: string;
}

/** Psyché facets (WP36.c, R4): the item's state as the dossier knows it — owning
 *  subject, thread closure, contradiction, decision, order — plus the claims
 *  extracted from the item (read from its cached metadata). */
function PsycheFacets({ facets, contentId }: { facets: DetailFacets; contentId?: string }) {
  const { data: item } = useQuery({
    queryKey: ["kb-item", contentId],
    queryFn: () => api.knowledgeItem(contentId as string),
    enabled: !!contentId,
  });
  const rawClaims = (item?.metadata?.extracted_claims as RawClaim[] | undefined) ?? [];
  const claims = rawClaims.filter((c) => c.attribute && c.value).slice(0, 6);

  const stateBadges: { variant: StatusVariant; label: string }[] = [];
  if (facets.closed) stateBadges.push({ variant: "closed", label: "thread closed" });
  if (facets.contradicted)
    stateBadges.push({ variant: "contradiction", label: "contradiction" });
  if (facets.decisionStatus)
    stateBadges.push({ variant: "decision", label: facets.decisionStatus });

  const hasState = stateBadges.length > 0;
  const hasClaims = claims.length > 0;
  if (!facets.subject && !hasState && !hasClaims && facets.order == null) return null;

  return (
    <div className="mb-5 rounded-xl border border-border bg-surface2/50 px-4 py-3">
      <div className="mb-2 text-[11px] font-medium uppercase tracking-wide text-muted">
        In your psyché
      </div>
      <div className="flex flex-col gap-2 text-[13px]">
        {facets.subject && (
          <div className="flex flex-wrap items-baseline gap-x-2">
            <span className="text-muted">subject</span>
            <span className="text-text">{facets.subject}</span>
            {facets.order != null && (
              <span className="text-faint">
                · {facets.order === 2 ? "related" : "direct"}
                {facets.viaNeighbor ? ` via ${facets.viaNeighbor}` : ""}
              </span>
            )}
          </div>
        )}
        {hasState && (
          <div className="flex flex-wrap gap-1.5">
            {stateBadges.map((b) => (
              <StatusBadge key={b.label} variant={b.variant}>
                {b.label}
              </StatusBadge>
            ))}
          </div>
        )}
        {hasClaims && (
          <div>
            <div className="mb-1 text-[12px] text-muted">Extracted claims</div>
            <ul className="flex flex-col gap-1">
              {claims.map((c, i) => (
                <li key={i} className="text-[13px] text-text">
                  <span className="text-muted">{c.attribute}:</span> {c.value}
                  {c.polarity === "negate" && (
                    <span className="ml-1 text-faint">(negated)</span>
                  )}
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </div>
  );
}
