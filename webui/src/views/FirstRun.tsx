import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { Loader2, Sparkles, PlugZap } from "lucide-react";
import { api } from "../lib/api";
import { useActivity } from "../lib/activity";
import { Button } from "../components/ui";
import logo from "../assets/logo.png";

/** Compact ETA: "45 s", "3 min", "1 h". */
function fmtETA(s: number): string {
  if (s < 60) return `${Math.round(s)} s`;
  const m = Math.round(s / 60);
  return m < 60 ? `${m} min` : `${Math.round(m / 60)} h`;
}

// Enough indexed material to be worth a first brief.
const READY_THRESHOLD = 5;

/** The rich first run: a full-screen cover shown once right after onboarding.
 *  Indexing → live progress; ready → offer the first brief; empty → connect a
 *  source. Always skippable. Lives inside ActivityProvider so it reads the live
 *  ingestion progress off the events stream. */
export function FirstRun({ onDone }: { onDone: () => void }) {
  const navigate = useNavigate();
  const { busy, label, progress } = useActivity();
  const { data } = useQuery({
    queryKey: ["kb-total"],
    queryFn: () => api.knowledgeTotal(),
    refetchInterval: busy ? 3000 : 6000,
  });
  const total = data?.total ?? 0;

  const runBrief = useMutation({
    mutationFn: () => api.runBrief({}),
    onSuccess: () => {
      navigate("/briefings"); // the brief is async — the list shows it as it lands
      onDone();
    },
  });

  const ready = !busy && total >= READY_THRESHOLD;

  return (
    <div className="fixed inset-0 z-50 flex flex-col items-center justify-center bg-bg px-6">
      <div className="view-enter flex w-full max-w-[460px] flex-col items-center text-center">
        <img src={logo} alt="" className="mb-6 size-16 rounded-[22%] shadow-sm" />

        {busy ? (
          <>
            <h1 className="font-display text-[24px] font-semibold tracking-tight">
              Setting up your knowledge base
            </h1>
            <p className="mt-2 max-w-[44ch] text-[13.5px] leading-relaxed text-muted">
              {label || "Indexing your sources…"} This runs once, then stays
              incremental — you can start using Hygur right away.
            </p>
            {progress ? (
              <div className="mt-7 w-full">
                <div className="h-1.5 w-full overflow-hidden rounded-full bg-border">
                  <div
                    className="h-full rounded-full bg-accent transition-all duration-500"
                    style={{
                      width: `${Math.min(100, Math.round((progress.processed / progress.total) * 100))}%`,
                    }}
                  />
                </div>
                <div className="tnum mt-2 flex justify-between text-[12px] text-muted">
                  <span>
                    {progress.processed}/{progress.total} indexed
                  </span>
                  {progress.etaSeconds > 0 && <span>~{fmtETA(progress.etaSeconds)} left</span>}
                </div>
              </div>
            ) : (
              <div className="mt-7 flex items-center gap-2 text-[13px] text-accent">
                <Loader2 size={15} strokeWidth={2} className="animate-spin" />
                Working…
              </div>
            )}
            <button
              onClick={onDone}
              className="mt-8 text-[13px] text-muted transition-colors hover:text-text"
            >
              Go to the app →
            </button>
          </>
        ) : ready ? (
          <>
            <h1 className="font-display text-[24px] font-semibold tracking-tight">
              Your knowledge base is ready
            </h1>
            <p className="mt-2 text-[13.5px] leading-relaxed text-muted">
              <span className="tnum">{total}</span> items indexed. Want a first read of what
              matters?
            </p>
            <div className="mt-7 flex w-full flex-col items-stretch gap-2.5">
              <Button onClick={() => runBrief.mutate()} disabled={runBrief.isPending}>
                <Sparkles size={16} strokeWidth={1.9} />
                {runBrief.isPending ? "Preparing your brief…" : "Generate your first brief"}
              </Button>
              <button
                onClick={onDone}
                className="text-[13px] text-muted transition-colors hover:text-text"
              >
                Go to the app →
              </button>
            </div>
          </>
        ) : (
          <>
            <h1 className="font-display text-[24px] font-semibold tracking-tight">
              Let's bring in your world
            </h1>
            <p className="mt-2 max-w-[42ch] text-[13.5px] leading-relaxed text-muted">
              Connect a source or add documents so Hygur can answer with your own context,
              not the public web.
            </p>
            <div className="mt-7 flex w-full flex-col items-stretch gap-2.5">
              <Button
                onClick={() => {
                  navigate("/connectors");
                  onDone();
                }}
              >
                <PlugZap size={16} strokeWidth={1.9} />
                Connect a source
              </Button>
              <button
                onClick={onDone}
                className="text-[13px] text-muted transition-colors hover:text-text"
              >
                Explore the app first →
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
