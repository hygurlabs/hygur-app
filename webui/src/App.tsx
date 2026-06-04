import { Routes, Route, Navigate } from "react-router-dom";
import { Sidebar } from "./components/Sidebar";
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

export default function App() {
  return (
    <ActivityProvider>
      <DetailPanelProvider>
        <div className="grid h-dvh grid-cols-[212px_1fr]">
          <Sidebar />
          <main className="relative min-w-0 overflow-hidden">
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
              <Route path="/connectors" element={<Connectors />} />
              <Route path="/settings" element={<Settings />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </main>
        </div>
      </DetailPanelProvider>
    </ActivityProvider>
  );
}
