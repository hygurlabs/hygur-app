import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Brain, Info } from "lucide-react";
import { api } from "../lib/api";
import type { LearningProgress } from "../lib/types";
import { Decisions } from "./Decisions";
import { Contradictions } from "./Contradictions";
import { MemoryView } from "./Memory";
import { Chronicle } from "./Chronicle";

type Tab = "decisions" | "contradictions" | "memory" | "chronicle";

const TABS: { key: Tab; label: string }[] = [
  { key: "decisions", label: "Décisions" },
  { key: "contradictions", label: "Contradictions" },
  { key: "memory", label: "Mémoire" },
  { key: "chronicle", label: "Chronique" },
];

// Esprit — le hub psyché consolidé (« ce que Hygur sait de toi »). Son en-tête rend
// la boucle de feedback explicite : une jauge qui monte à mesure que tu confirmes ou
// corriges. Les onglets montent les vues existantes telles quelles ; leurs actions
// nourrissent la jauge (elle interroge /insights/learning-progress).
export function Mind() {
  const [tab, setTab] = useState<Tab>("decisions");
  const [showWhy, setShowWhy] = useState(false);

  const { data: lp } = useQuery<LearningProgress>({
    queryKey: ["learning-progress"],
    queryFn: () => api.learningProgress(),
    refetchInterval: 8000,
    refetchOnWindowFocus: true,
  });
  const pct = Math.round((lp?.coverage ?? 0) * 100);

  return (
    <div className="flex h-full flex-col">
      <div className="shrink-0 px-4 pt-6 sm:px-7">
        <div className="mx-auto max-w-[760px]">
          <div className="rounded-xl border border-border bg-surface2 p-4">
            <div className="flex items-center justify-between gap-3">
              <div className="flex items-center gap-2 text-[14px] font-medium text-text">
                <Brain size={17} strokeWidth={1.75} className="text-accent" />
                Hygur te connaît
              </div>
              <div className="tnum text-[15px] font-semibold text-accent">{pct}%</div>
            </div>
            <div className="mt-2 h-2 w-full overflow-hidden rounded-full bg-border">
              <div
                className="h-full rounded-full bg-accent transition-all duration-500"
                style={{ width: `${pct}%` }}
              />
            </div>
            <p className="mt-2.5 text-[13px] leading-relaxed text-muted">
              Ton feedback ici l'aide à mieux te cerner — plus tu confirmes ou corriges, plus Hygur
              devient pertinent au fil de l'usage.{" "}
              <button
                type="button"
                onClick={() => setShowWhy((v) => !v)}
                className="inline-flex items-center gap-1 text-accent hover:underline"
              >
                <Info size={12} strokeWidth={2} /> pourquoi
              </button>
            </p>
            {showWhy && lp && (
              <div className="mt-3 rounded-lg border border-border bg-accent-weak/30 p-3 text-[12.5px]">
                {lp.next_step_hint && (
                  <p className="mb-2 text-text">Prochain pas : {lp.next_step_hint}</p>
                )}
                <ul className="flex flex-col gap-1">
                  {lp.pillars.map((p) => (
                    <li key={p.key} className="flex items-center justify-between gap-2 text-muted">
                      <span>{p.label}</span>
                      <span className="tnum">
                        {p.current}/{p.target}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>

          <div className="mt-4 flex flex-wrap gap-1 border-b border-border">
            {TABS.map((t) => (
              <button
                key={t.key}
                type="button"
                onClick={() => setTab(t.key)}
                className={`-mb-px rounded-t-lg px-3 py-2 text-[14px] transition-colors ${
                  tab === t.key
                    ? "border-b-2 border-accent font-medium text-accent"
                    : "text-muted hover:text-text"
                }`}
              >
                {t.label}
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* The active view brings its own <Page> (h-full scroll); flex-1 lets it fill. */}
      <div className="min-h-0 flex-1">
        {tab === "decisions" && <Decisions />}
        {tab === "contradictions" && <Contradictions />}
        {tab === "memory" && <MemoryView />}
        {tab === "chronicle" && <Chronicle />}
      </div>
    </div>
  );
}
