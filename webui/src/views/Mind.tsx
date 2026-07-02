import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";
import { Brain, Info } from "lucide-react";
import { api } from "../lib/api";
import { ToggleGroup } from "../components/ui";
import type { LearningProgress } from "../lib/types";
import { Decisions } from "./Decisions";
import { Contradictions } from "./Contradictions";
import { MemoryView } from "./Memory";
import { Chronicle } from "./Chronicle";

type Tab = "decisions" | "contradictions" | "memory" | "chronicle";

const TABS: { key: Tab; label: string }[] = [
  { key: "decisions", label: "Decisions" },
  { key: "contradictions", label: "Contradictions" },
  { key: "memory", label: "Memory" },
  { key: "chronicle", label: "Chronicle" },
];

// Mind — the consolidated psyché hub ("what Hygur knows about you"). Its header
// makes the feedback loop explicit: a gauge that climbs as the user confirms or
// corrects. The tabs mount the existing self-contained views unchanged; their
// per-item actions feed the gauge (it polls /insights/learning-progress).
export function Mind() {
  // Orphan routes (/decisions, /contradictions, …) redirect here as
  // /mind?tab=<name>; read that and open the matching tab.
  const [params] = useSearchParams();
  const paramTab = params.get("tab");
  const isTab = (v: string | null): v is Tab => TABS.some((t) => t.key === v);
  const [tab, setTab] = useState<Tab>(isTab(paramTab) ? paramTab : "decisions");
  const [showWhy, setShowWhy] = useState(false);

  useEffect(() => {
    if (paramTab && TABS.some((t) => t.key === paramTab)) setTab(paramTab as Tab);
  }, [paramTab]);

  const { data: lp, isError: lpError } = useQuery<LearningProgress>({
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
                Hygur knows you
              </div>
              <div
                className="tnum text-[15px] font-semibold text-accent"
                title={lpError ? "Couldn't load your progress right now." : undefined}
              >
                {lpError ? "—%" : `${pct}%`}
              </div>
            </div>
            <div className="mt-2 h-2 w-full overflow-hidden rounded-full bg-border">
              <div
                className="h-full rounded-full bg-accent transition-all duration-500"
                style={{ width: `${pct}%` }}
              />
            </div>
            <p className="mt-2.5 text-[13px] leading-relaxed text-muted">
              Your feedback here helps Hygur get to know you — the more you confirm or correct, the
              more relevant it becomes over time.{" "}
              <button
                type="button"
                onClick={() => setShowWhy((v) => !v)}
                className="inline-flex items-center gap-1 text-accent hover:underline"
              >
                <Info size={12} strokeWidth={2} /> why
              </button>
            </p>
            {showWhy && lp && (
              <div className="mt-3 rounded-lg border border-border bg-accent-weak/30 p-3 text-[12.5px]">
                {lp.next_step_hint && (
                  <p className="mb-2 text-text">Next step: {lp.next_step_hint}</p>
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

          <ToggleGroup
            variant="tabs"
            ariaLabel="Mind section"
            className="mt-4"
            value={tab}
            onChange={setTab}
            options={TABS.map((t) => ({ value: t.key, label: t.label }))}
          />
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
