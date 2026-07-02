import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { AlertTriangle, CheckCircle2, Info, X } from "lucide-react";

type ToastKind = "success" | "error" | "info";

interface ToastItem {
  id: number;
  kind: ToastKind;
  message: string;
}

interface ToastApi {
  success: (message: string) => void;
  error: (message: string) => void;
  info: (message: string) => void;
}

const noop = () => {};
const ToastContext = createContext<ToastApi>({ success: noop, error: noop, info: noop });

// eslint-disable-next-line react-refresh/only-export-components -- hook co-located with its provider (HMR-only rule; splitting it is needless churn)
export function useToast() {
  return useContext(ToastContext);
}

// A confirmation is glanceable; a failure is worth reading twice, so errors linger.
const DISMISS_MS: Record<ToastKind, number> = {
  success: 5000,
  info: 5000,
  error: 9000,
};

/** App-wide transient feedback. Failures are always toasted; successes only for
 *  notable user actions. Mount once at the top of the tree (App). */
export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([]);
  const nextId = useRef(0);

  const dismiss = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const push = useCallback(
    (kind: ToastKind, message: string) => {
      const id = ++nextId.current;
      setToasts((prev) => [...prev, { id, kind, message }]);
      window.setTimeout(() => dismiss(id), DISMISS_MS[kind]);
    },
    [dismiss],
  );

  const api = useMemo<ToastApi>(
    () => ({
      success: (m) => push("success", m),
      error: (m) => push("error", m),
      info: (m) => push("info", m),
    }),
    [push],
  );

  return (
    <ToastContext.Provider value={api}>
      {children}
      {/* Bottom-right stack, above the update banner / install prompt (z-70).
          Respects the iOS home-bar inset. */}
      <div
        aria-live="polite"
        className="pointer-events-none fixed bottom-0 right-0 z-[80] flex w-full max-w-[380px] flex-col gap-2 p-4 pb-[calc(1rem_+_env(safe-area-inset-bottom))] print:hidden"
      >
        {toasts.map((t) => (
          <ToastRow key={t.id} toast={t} onDismiss={dismiss} />
        ))}
      </div>
    </ToastContext.Provider>
  );
}

// Success = success token, error = danger, info = neutral — all design tokens so
// dark mode is automatic.
const KIND_STYLE: Record<ToastKind, { border: string; icon: string; Icon: typeof Info }> = {
  success: { border: "border-success/50", icon: "text-success", Icon: CheckCircle2 },
  error: { border: "border-danger/50", icon: "text-danger", Icon: AlertTriangle },
  info: { border: "border-border", icon: "text-muted", Icon: Info },
};

function ToastRow({ toast, onDismiss }: { toast: ToastItem; onDismiss: (id: number) => void }) {
  const { border, icon, Icon } = KIND_STYLE[toast.kind];
  return (
    <div
      className={`view-enter pointer-events-auto flex items-start gap-2.5 rounded-lg border ${border} bg-surface px-3.5 py-2.5 text-[13px] shadow-lg`}
    >
      <Icon size={16} strokeWidth={2} className={`mt-px shrink-0 ${icon}`} />
      <span className="min-w-0 flex-1 break-words text-text">{toast.message}</span>
      <button
        onClick={() => onDismiss(toast.id)}
        aria-label="Dismiss"
        className="-mr-1 -mt-0.5 shrink-0 rounded-md p-1 text-faint transition-colors hover:text-text"
      >
        <X size={14} strokeWidth={2} />
      </button>
    </div>
  );
}
