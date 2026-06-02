import { useState } from "react";
import { markOnboardingComplete } from "../lib/onboarding";
import {
  StepAccounts,
  StepModel,
  StepNotifications,
  StepPermissions,
  StepReady,
  StepWelcome,
  type StepContext,
} from "./steps";

interface StepDef {
  id: string;
  /** Shows a "Skip for now" affordance in the footer. */
  skippable: boolean;
  /** The step renders its own primary button (no generic footer one). */
  ownsPrimary: boolean;
  /** Label for the generic footer primary button (when ownsPrimary is false). */
  primaryLabel?: string;
}

const STEPS: StepDef[] = [
  { id: "welcome", skippable: false, ownsPrimary: false, primaryLabel: "Get started" },
  { id: "permissions", skippable: false, ownsPrimary: false, primaryLabel: "Continue" },
  { id: "model", skippable: true, ownsPrimary: true },
  { id: "accounts", skippable: true, ownsPrimary: true },
  { id: "notifications", skippable: false, ownsPrimary: false, primaryLabel: "Continue" },
  { id: "ready", skippable: false, ownsPrimary: false, primaryLabel: "Start using Hygur" },
];

/** Full-screen first-run wizard. Replaces the app shell until the user finishes
 *  or skips through it; `onComplete` swaps the real app in. */
export function Onboarding({ onComplete }: { onComplete: () => void }) {
  const [index, setIndex] = useState(0);
  const step = STEPS[index];
  const isLast = index === STEPS.length - 1;

  const complete = (route?: string) => {
    if (route) window.location.hash = route;
    markOnboardingComplete();
    onComplete();
  };
  const next = () => {
    if (isLast) complete();
    else setIndex((i) => Math.min(STEPS.length - 1, i + 1));
  };
  const back = () => setIndex((i) => Math.max(0, i - 1));

  const ctx: StepContext = { next, complete };

  return (
    <div className="fixed inset-0 z-50 flex flex-col bg-bg">
      <div className="flex-1 overflow-y-auto">
        <div
          key={step.id}
          className="view-enter mx-auto flex min-h-full max-w-[560px] flex-col justify-center px-8 py-12"
        >
          {step.id === "welcome" && <StepWelcome />}
          {step.id === "permissions" && <StepPermissions />}
          {step.id === "model" && <StepModel ctx={ctx} />}
          {step.id === "accounts" && <StepAccounts ctx={ctx} />}
          {step.id === "notifications" && <StepNotifications />}
          {step.id === "ready" && <StepReady />}
        </div>
      </div>

      <footer className="flex items-center gap-4 border-t border-border px-8 py-4">
        <div className="min-w-[80px]">
          {index > 0 && (
            <button
              onClick={back}
              className="rounded-lg px-2 py-1.5 text-[13px] text-muted transition-colors hover:text-text"
            >
              Back
            </button>
          )}
        </div>

        <div className="flex flex-1 items-center justify-center gap-2" aria-hidden>
          {STEPS.map((s, i) => (
            <span
              key={s.id}
              className={`h-1.5 rounded-full transition-all ${
                i === index ? "w-5 bg-accent" : "w-1.5 bg-border"
              }`}
            />
          ))}
        </div>

        <div className="flex min-w-[80px] items-center justify-end gap-3">
          {step.skippable && (
            <button
              onClick={next}
              className="rounded-lg px-2 py-1.5 text-[13px] text-muted transition-colors hover:text-text"
            >
              Skip for now
            </button>
          )}
          {!step.ownsPrimary && (
            <button
              onClick={next}
              className="inline-flex items-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white transition-colors hover:opacity-90"
            >
              {step.primaryLabel}
            </button>
          )}
        </div>
      </footer>
    </div>
  );
}
