import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import { X, MessageSquareText } from "lucide-react";
import { useNavigate } from "react-router-dom";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { ItemMeta } from "./ItemMeta";

export interface DetailData {
  title: string;
  meta: string[];
  body: string;
  /** When set, the panel shows an "Ask in chat" action that attaches this
   *  document to a new question in the Ask view. */
  contentId?: string;
  /** Optional actions rendered in the panel header (e.g. Edit, Delete). */
  actions?: ReactNode;
}

const OpenContext = createContext<(d: DetailData) => void>(() => {});

/** Any view can call `useDetail()(…)` to slide the reader panel in. Centralising
 *  it here keeps a single panel instance + one scrim, avoiding z-index sprawl. */
// eslint-disable-next-line react-refresh/only-export-components -- hook co-located with its provider (HMR-only rule; splitting it is needless churn)
export function useDetail() {
  return useContext(OpenContext);
}

export function DetailPanelProvider({ children }: { children: ReactNode }) {
  const navigate = useNavigate();
  const [data, setData] = useState<DetailData | null>(null);
  const open = useCallback((d: DetailData) => setData(d), []);
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
            <div className="flex items-start gap-3 border-b border-border px-5 py-4">
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
                {data.contentId && (
                  <button
                    onClick={askInChat}
                    title="Ask Hygur about this"
                    className="inline-flex items-center gap-1.5 rounded-md border border-border px-2 py-1 text-[12.5px] text-muted transition-colors hover:border-accent hover:text-accent"
                  >
                    <MessageSquareText size={14} strokeWidth={1.75} />
                    Ask
                  </button>
                )}
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
            <div className="overflow-auto px-5 py-5">
              {/* Project + tags sit above the body so they're visible at a
                  glance — even when the document/mail body is long. ItemMeta
                  owns its own separator and renders nothing until the item
                  loads, so there's no empty band. */}
              {data.contentId && <ItemMeta contentId={data.contentId} />}
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
