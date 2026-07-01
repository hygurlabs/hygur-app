import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import { X, MessageSquareText, ListPlus, Reply, Copy, Check } from "lucide-react";
import { useNavigate } from "react-router-dom";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { ItemMeta } from "./ItemMeta";
import { api } from "../lib/api";

export interface DetailData {
  title: string;
  meta: string[];
  body: string;
  /** When set, the panel shows "Ask in chat" + "Create task" (and "Draft reply"
   *  for mail) and loads the item's project/tags. */
  contentId?: string;
  /** Item source type — enables the mail-only "Draft reply" action. */
  sourceType?: string;
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
  const [data, setData] = useState<DetailData | null>(null);
  const [taskDone, setTaskDone] = useState(false);
  const [draft, setDraft] = useState<string | null>(null);
  const [drafting, setDrafting] = useState(false);
  const [copied, setCopied] = useState(false);

  const open = useCallback((d: DetailData) => {
    setData(d);
    setTaskDone(false);
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
    try {
      await api.createTask({
        title: data.title || "Untitled",
      });
      setTaskDone(true);
    } catch {
      /* surfaced by the disabled→retry affordance; keep panel usable */
    }
  }, [data]);

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
                  className="inline-flex min-h-9 items-center gap-1.5 rounded-lg bg-accent-weak px-3.5 py-2 text-[13px] font-medium text-accent transition-colors hover:bg-accent-weak/70"
                >
                  {taskDone ? (
                    <Check size={15} strokeWidth={2.2} />
                  ) : (
                    <ListPlus size={15} strokeWidth={2} />
                  )}
                  {taskDone ? "Task added" : "Make a task"}
                </button>
              </div>
            )}
            <div className="overflow-auto px-5 py-5">
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

              <div className="prose-answer text-[14px] leading-relaxed text-text">
                <ReactMarkdown remarkPlugins={[remarkGfm]}>
                  {data.body || "_(empty)_"}
                </ReactMarkdown>
              </div>
            </div>
          </>
        )}
      </aside>
    </OpenContext.Provider>
  );
}
