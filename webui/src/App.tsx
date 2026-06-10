import { useEffect, useState } from "react";
import { Routes, Route, Navigate, useLocation } from "react-router-dom";
import { Menu } from "lucide-react";
import { Sidebar } from "./components/Sidebar";
import { QuickCapture } from "./views/QuickCapture";
import { DetailPanelProvider } from "./components/DetailPanel";
import { ActivityProvider } from "./lib/activity";
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
import { Tasks } from "./views/Tasks";
import { FirstRun } from "./views/FirstRun";

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
            <div className="flex items-center gap-2 border-b border-border px-3 py-2 md:hidden print:hidden">
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
                <Route path="/" element={<Ask />} />
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
                <Route path="/connectors" element={<Connectors />} />
                <Route path="/settings" element={<Settings />} />
                <Route path="*" element={<Navigate to="/" replace />} />
              </Routes>
            </div>
          </main>
        </div>
        )}
      </DetailPanelProvider>
    </ActivityProvider>
  );
}
