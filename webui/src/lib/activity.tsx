import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { subscribeEvents, type HygurEvent } from "./events";
import { native } from "./native";

/** Sidecar event → native banner, gated by the matching notification toggle
 *  (mirrors the old SwiftUI NotificationsService). Banners go through
 *  native.notify, which uses the OS bridge natively or the Web Notifications
 *  API in Tauri/browser. */
const NOTIFY: Record<string, { key: string; title: string }> = {
  mail_digest: { key: "notify.priorityMail", title: "Important mail" },
  brief: { key: "notify.dailyBrief", title: "Daily brief" },
  agenda_alert: { key: "notify.agendaAlerts", title: "Deadline" },
};

function maybeNotify(e: HygurEvent): void {
  const n = NOTIFY[e.type];
  if (!n) return;
  void native.prefs.getBool(n.key).then((on) => {
    if (on) void native.notify(`Hygur — ${n.title}`, e.message ?? "");
  });
}

export interface SyncProgress {
  processed: number;
  total: number;
  etaSeconds: number;
}

export interface Activity {
  busy: boolean;
  label: string;
  /** Present while a sync reports thread counts, for a determinate loading bar. */
  progress?: SyncProgress;
}

const ActivityContext = createContext<Activity>({ busy: false, label: "" });

/** Pulls processed/total/eta_seconds off a sync event's data payload. */
function readProgress(e: HygurEvent): SyncProgress | undefined {
  const d = e.data;
  if (!d) return undefined;
  const processed = typeof d.processed === "number" ? d.processed : undefined;
  const total = typeof d.total === "number" ? d.total : undefined;
  if (processed === undefined || total === undefined || total <= 0) return undefined;
  return {
    processed,
    total,
    etaSeconds: typeof d.eta_seconds === "number" ? d.eta_seconds : 0,
  };
}

/** Reports whether a background sync/indexation is currently running, so the UI
 *  can surface a live indicator. Fed by the sidecar `/events` SSE stream. */
// eslint-disable-next-line react-refresh/only-export-components -- hook co-located with its provider (HMR-only rule; splitting it is needless churn)
export function useActivity(): Activity {
  return useContext(ActivityContext);
}

/** Maps an in-progress event to a short label, or null when the event isn't a
 *  "work in progress" signal (terminal events are handled by the caller). */
function activityLabel(e: HygurEvent): string | null {
  const s = (e.status ?? "").toLowerCase();
  const running = s === "running" || s === "pending";
  switch (e.type) {
    case "ingest_start":
    case "ingest_progress":
      return "Indexing…";
    case "ingest":
      return running ? "Indexing…" : null;
    case "sync":
      return running ? "Syncing…" : null;
    case "connectors":
      return running ? "Syncing connectors…" : null;
    case "mail":
      return running ? "Syncing mail…" : null;
    default:
      return running ? "Working…" : null;
  }
}

export function ActivityProvider({ children }: { children: ReactNode }) {
  const [activity, setActivity] = useState<Activity>({ busy: false, label: "" });
  const timer = useRef<number | null>(null);

  useEffect(() => {
    const ctrl = new AbortController();

    const clearTimer = () => {
      if (timer.current !== null) {
        window.clearTimeout(timer.current);
        timer.current = null;
      }
    };

    void subscribeEvents((e) => {
      maybeNotify(e); // fire a native banner for priority events (pref-gated)

      const status = (e.status ?? "").toLowerCase();
      const terminal =
        e.type === "ingest_complete" ||
        e.type === "mail_digest" ||
        status === "completed" ||
        status === "failed";

      if (terminal) {
        // Let the indicator linger briefly so a quick task is still noticed.
        clearTimer();
        timer.current = window.setTimeout(
          () => setActivity({ busy: false, label: "", progress: undefined }),
          1500,
        );
        return;
      }

      const label = activityLabel(e);
      if (label) {
        const prog = readProgress(e);
        // Keep the last known progress when an interleaved event lacks counts.
        setActivity((prev) => ({ busy: true, label, progress: prog ?? prev.progress }));
        // Watchdog: if events stop flowing (missed completion), auto-clear.
        clearTimer();
        timer.current = window.setTimeout(
          () => setActivity({ busy: false, label: "", progress: undefined }),
          8000,
        );
      }
    }, ctrl.signal);

    return () => {
      ctrl.abort();
      clearTimer();
    };
  }, []);

  return (
    <ActivityContext.Provider value={activity}>
      {children}
    </ActivityContext.Provider>
  );
}
