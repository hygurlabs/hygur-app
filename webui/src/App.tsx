import { useEffect, useState } from "react";
import { Routes, Route, Navigate, useLocation } from "react-router-dom";
import { Menu, RefreshCw, X } from "lucide-react";
import { Sidebar } from "./components/Sidebar";
import { QuickCapture } from "./views/QuickCapture";
import { Digest } from "./views/Digest";
import { DetailPanelProvider } from "./components/DetailPanel";
import { ActivityProvider } from "./lib/activity";
import { useUpdateAvailable } from "./lib/version";
import { Ask } from "./views/Ask";
import { Library } from "./views/Library";
import { Notes } from "./views/Notes";
import { Projects } from "./views/Projects";
import { Briefings } from "./views/Briefings";
import { Connectors } from "./views/Connectors";
import { Settings } from "./views/Settings";
import { Tags } from "./views/Tags";
import { Calendar } from "./views/Calendar";
import { FollowUp } from "./views/FollowUp";
import { Contradictions } from "./views/Contradictions";
import { Decisions } from "./views/Decisions";
import { Chronicle } from "./views/Chronicle";
import { MemoryView } from "./views/Memory";
import { Tasks } from "./views/Tasks";
import { FirstRun } from "./views/FirstRun";
import { InstallPrompt } from "./components/InstallPrompt";

export default function App({ revealOnMount = false }: { revealOnMount?: boolean }) {
  // Left nav is a static column on desktop and an off-canvas drawer on mobile.
  const [navOpen, setNavOpen] = useState(false);
  // The rich first run shows once, right after onboarding (revealOnMount): index
  // progress → first brief, or a connect CTA. Never re-shown / never for existing
  // users (it rides the same in-memory transition as the reveal).
  const [firstRun, setFirstRun] = useState(revealOnMount);
  // Gentle fade-in only when arriving from onboarding (the "reveal"); a normal
  // launch starts already-revealed so reloads don't fade every time.
  const [revealed, setRevealed] = useState(!revealOnMount);
  useEffect(() => {
    if (revealed) return;
    const id = requestAnimationFrame(() => setRevealed(true));
    return () => cancelAnimationFrame(id);
  }, [revealed]);
  // A new web build has been deployed → offer a reload (web-only).
  const updateAvailable = useUpdateAvailable();
  const [updateDismissed, setUpdateDismissed] = useState(false);
  const { pathname } = useLocation();

  // The quick-capture palette runs in its own frameless Tauri window — render
  // it bare, without the app shell (sidebar / top bar).
  if (pathname === "/quick") return <QuickCapture />;

  return (
    <ActivityProvider>
      <DetailPanelProvider>
        {firstRun ? (
          <FirstRun onDone={() => setFirstRun(false)} />
        ) : (
        <div
          className={`relative flex h-dvh transition-opacity duration-500 ease-out ${
            revealed ? "opacity-100" : "opacity-0"
          }`}
        >
          <Sidebar open={navOpen} onClose={() => setNavOpen(false)} />
          {navOpen && (
            <div
              aria-hidden
              onClick={() => setNavOpen(false)}
              className="fixed inset-0 z-30 bg-text/25 md:hidden"
            />
          )}
          <main className="relative flex min-w-0 flex-1 flex-col overflow-hidden">
            {/* Mobile-only top bar with the menu toggle (desktop uses the sidebar). */}
            <div className="flex items-center gap-2 border-b border-border px-3 pb-2 pt-[calc(0.5rem_+_env(safe-area-inset-top))] md:hidden print:hidden">
              <button
                onClick={() => setNavOpen(true)}
                aria-label="Open menu"
                className="rounded-md p-1.5 text-muted transition-colors hover:bg-surface2 hover:text-text"
              >
                <Menu size={20} strokeWidth={1.9} />
              </button>
              <span className="font-display text-[15px] font-semibold tracking-tight">
                Hygur
              </span>
            </div>
            <div className="relative min-h-0 flex-1 overflow-hidden">
              <Routes>
                {/* Land on Today (the daily brief + what needs you), not a blank
                    chat — the psyche's value greets the user first. Ask moves to /ask. */}
                <Route path="/" element={<Navigate to="/digest" replace />} />
                <Route path="/ask" element={<Ask />} />
                <Route path="/digest" element={<Digest />} />
                {/* Search merged into Library; redirect old links. */}
                <Route path="/search" element={<Navigate to="/library" replace />} />
                <Route path="/library" element={<Library />} />
                <Route path="/notes" element={<Notes />} />
                <Route path="/projects" element={<Projects />} />
                <Route path="/briefings" element={<Briefings />} />
                <Route path="/tags" element={<Tags />} />
                <Route path="/calendar" element={<Calendar />} />
                <Route path="/tasks" element={<Tasks />} />
                <Route path="/follow-up" element={<FollowUp />} />
                <Route path="/contradictions" element={<Contradictions />} />
                <Route path="/decisions" element={<Decisions />} />
                <Route path="/chronicle" element={<Chronicle />} />
                <Route path="/memory" element={<MemoryView />} />
                <Route path="/connectors" element={<Connectors />} />
                <Route path="/settings" element={<Settings />} />
                <Route path="*" element={<Navigate to="/" replace />} />
              </Routes>
            </div>
          </main>
        </div>
        )}

        {updateAvailable && !updateDismissed && (
          <div className="fixed bottom-[calc(1rem_+_env(safe-area-inset-bottom))] left-1/2 z-[70] flex -translate-x-1/2 items-center gap-3 rounded-full border border-border bg-surface px-4 py-2.5 text-[13px] shadow-lg print:hidden">
            <span className="text-text">A new version of Hygur is available.</span>
            <button
              onClick={() => window.location.reload()}
              className="inline-flex items-center gap-1.5 rounded-full bg-accent px-3 py-1 font-medium text-white transition-opacity hover:opacity-90"
            >
              <RefreshCw size={13} strokeWidth={2} />
              Reload
            </button>
            <button
              onClick={() => setUpdateDismissed(true)}
              aria-label="Dismiss"
              className="rounded-md p-0.5 text-faint transition-colors hover:text-text"
            >
              <X size={14} strokeWidth={2} />
            </button>
          </div>
        )}

        {/* Mobile-web: nudge to add the PWA to the home screen (self-guards:
            mobile browser, not already standalone, not the native shell). */}
        <InstallPrompt />
      </DetailPanelProvider>
    </ActivityProvider>
  );
}
