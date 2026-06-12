import { useEffect } from "react";
import { NavLink } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  MessageSquareText,
  Sunrise,
  Library,
  StickyNote,
  Tag,
  CalendarDays,
  CheckSquare,
  FolderKanban,
  GitCompareArrows,
  Scale,
  Gavel,
  Newspaper,
  BookOpen,
  Brain,
  Plug,
  LogOut,
  Settings as SettingsIcon,
  Loader2,
  type LucideIcon,
} from "lucide-react";
import { api } from "../lib/api";
import { useActivity } from "../lib/activity";
import { clearTokens, isRemote } from "../lib/connection";
import { getDesktopConfig, isDesktop, signOutDesktop, waitForSidecarThenReload } from "../lib/desktop";

interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
  end?: boolean;
}

const NAV: NavItem[] = [
  { to: "/", label: "Ask", icon: MessageSquareText, end: true },
  { to: "/digest", label: "Today", icon: Sunrise },
  { to: "/library", label: "Library", icon: Library },
  { to: "/notes", label: "Notes", icon: StickyNote },
  { to: "/projects", label: "Projects", icon: FolderKanban },
  { to: "/briefings", label: "Briefings", icon: Newspaper },
  { to: "/chronicle", label: "Chronicle", icon: BookOpen },
  { to: "/tags", label: "Tags", icon: Tag },
  { to: "/calendar", label: "Calendar", icon: CalendarDays },
  { to: "/tasks", label: "Tasks", icon: CheckSquare },
  { to: "/follow-up", label: "Follow-up", icon: GitCompareArrows },
  { to: "/contradictions", label: "Contradictions", icon: Scale },
  { to: "/decisions", label: "Decisions", icon: Gavel },
  { to: "/memory", label: "Memory", icon: Brain },
];

// Pinned to the bottom, just above the health indicator. Settings joins this
// group once the integrated Settings panel lands.
const BOTTOM_NAV: NavItem[] = [
  { to: "/connectors", label: "Connectors", icon: Plug },
  { to: "/settings", label: "Settings", icon: SettingsIcon },
];

const linkClass = ({ isActive }: { isActive: boolean }) =>
  `flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-[14px] transition-colors ${
    isActive
      ? "bg-accent-weak font-medium text-accent"
      : "text-muted hover:bg-accent-weak/50 hover:text-text"
  }`;

/** Compact ETA: "45 s", "3 min", "1 h". */
function fmtETA(s: number): string {
  if (s < 60) return `${Math.round(s)} s`;
  const m = Math.round(s / 60);
  return m < 60 ? `${m} min` : `${Math.round(m / 60)} h`;
}

export function Sidebar({
  open = false,
  onClose,
}: {
  open?: boolean;
  onClose?: () => void;
}) {
  const { busy, label, progress } = useActivity();
  const qc = useQueryClient();
  const { data: healthy } = useQuery({
    queryKey: ["health"],
    queryFn: async () => {
      const h = await api.health();
      return h?.status === "ok";
    },
    refetchInterval: 15000,
    refetchOnReconnect: true,
    retry: false,
  });

  // Re-check health the moment the network or window wakes (laptop sleep +
  // network change otherwise leaves it "offline" until the next 15s tick).
  useEffect(() => {
    const recheck = () => qc.invalidateQueries({ queryKey: ["health"] });
    window.addEventListener("online", recheck);
    window.addEventListener("focus", recheck);
    return () => {
      window.removeEventListener("online", recheck);
      window.removeEventListener("focus", recheck);
    };
  }, [qc]);

  // On desktop the cloud session lives in the Tauri config (the sidecar proxies
  // with it), not localStorage — so isRemote() is false. Detect a cloud account
  // via the config so "Sign out" shows, and route it through the Tauri command.
  const { data: desktopCloud } = useQuery({
    queryKey: ["desktop", "mode"],
    queryFn: async () => (await getDesktopConfig()).mode === "cloud",
    enabled: isDesktop(),
    retry: false,
  });
  const canSignOut = isRemote() || desktopCloud === true;
  const signOut = async () => {
    onClose?.();
    if (isDesktop()) {
      await signOutDesktop();
      await waitForSidecarThenReload();
    } else {
      clearTokens();
    }
  };

  return (
    <nav
      className={`fixed inset-y-0 left-0 z-40 flex w-60 flex-col gap-0.5 overflow-y-auto border-r border-border bg-surface2 px-2.5 py-4 transition-transform duration-200 ease-out md:static md:z-auto md:w-[212px] md:translate-x-0 print:hidden ${
        open ? "translate-x-0" : "-translate-x-full"
      }`}
    >
      <div className="px-2.5 pb-4 pt-1 font-display text-[21px] font-semibold tracking-tight">
        Hygur
      </div>

      {NAV.map(({ to, label, icon: Icon, end }) => (
        <NavLink key={to} to={to} end={end} className={linkClass} onClick={onClose}>
          <Icon size={17} strokeWidth={1.75} />
          {label}
        </NavLink>
      ))}

      <div className="mt-auto flex flex-col gap-0.5 pt-3">
        {BOTTOM_NAV.map(({ to, label, icon: Icon, end }) => (
          <NavLink key={to} to={to} end={end} className={linkClass} onClick={onClose}>
            <Icon size={17} strokeWidth={1.75} />
            {label}
          </NavLink>
        ))}

        {/* Sign out — only when there's a cloud account behind the session (a
            purely-local desktop has none). On web, clearTokens() purges + reroutes
            to Connect via SIGNED_OUT_EVENT; on desktop, the Tauri command drops the
            cloud binding from the config + restarts the sidecar → ModePicker. */}
        {canSignOut && (
          <button
            type="button"
            onClick={() => void signOut()}
            className="flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[14px] text-muted transition-colors hover:bg-accent-weak/50 hover:text-text"
          >
            <LogOut size={17} strokeWidth={1.75} />
            Sign out
          </button>
        )}

        {busy ? (
          <div className="px-2.5 pt-3 text-[12px] text-accent">
            <div className="flex items-center gap-2">
              <Loader2 size={13} strokeWidth={2} className="animate-spin" />
              <span className="truncate">{label || "Working…"}</span>
            </div>
            {progress && (
              <div className="mt-1.5">
                <div className="h-1 w-full overflow-hidden rounded-full bg-border">
                  <div
                    className="h-full rounded-full bg-accent transition-all duration-500"
                    style={{
                      width: `${Math.min(100, Math.round((progress.processed / progress.total) * 100))}%`,
                    }}
                  />
                </div>
                <div className="tnum mt-1 flex justify-between text-[10.5px] text-muted">
                  <span>
                    {progress.processed}/{progress.total}
                  </span>
                  {progress.etaSeconds > 0 && <span>~{fmtETA(progress.etaSeconds)}</span>}
                </div>
              </div>
            )}
          </div>
        ) : (
          <div className="flex items-center gap-2 px-2.5 pt-3 text-[12px] text-muted">
            <span
              className={`size-2 rounded-full transition-colors ${
                healthy === undefined
                  ? "bg-faint"
                  : healthy
                    ? "bg-accent"
                    : "bg-danger"
              }`}
            />
            {healthy === undefined
              ? "connecting…"
              : healthy
                ? "connected"
                : "offline"}
          </div>
        )}
      </div>
    </nav>
  );
}
