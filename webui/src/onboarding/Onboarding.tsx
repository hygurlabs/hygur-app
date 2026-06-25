import { useEffect, useMemo, useState } from "react";
import { markOnboardingComplete } from "../lib/onboarding";
import { native } from "../lib/native";
import { isRemote } from "../lib/connection";
import { mobileOS } from "../lib/platform";
import { api } from "../lib/api";
import {
  StepAccounts,
  StepMobile,
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

const ALL_STEPS: StepDef[] = [
  { id: "welcome", skippable: false, ownsPrimary: false, primaryLabel: "Get started" },
  { id: "permissions", skippable: false, ownsPrimary: false, primaryLabel: "Continue" },
  { id: "model", skippable: true, ownsPrimary: true },
  { id: "accounts", skippable: true, ownsPrimary: true },
  { id: "notifications", skippable: false, ownsPrimary: false, primaryLabel: "Continue" },
  { id: "mobile", skippable: true, ownsPrimary: false, primaryLabel: "Continue" },
  { id: "ready", skippable: false, ownsPrimary: false, primaryLabel: "Start using Hygur" },
];

// Steps depend on context:
//  - "permissions" (macOS perms) only makes sense in a native shell — skip in the
//    browser / Tauri web client (no native bridge).
//  - "model" (LLM endpoints) is for a LOCAL server you configure yourself; on a
//    remote OR managed cloud tenant the server owns its LLM (endpoints redacted),
//    so skip it. `managed` covers the proxy thin client, where isRemote() is false
//    (same-origin) but /config reports managed.
function visibleSteps(managed: boolean): StepDef[] {
  const remote = isRemote() || managed;
  return ALL_STEPS.filter((s) => {
    if (s.id === "permissions") return native.available;
    if (s.id === "model") return !remote;
    // "Connect your phone" QR targets a cloud instance and is shown on desktop
    // only — on a mobile browser the install nudge handles it, so skip it there.
    if (s.id === "mobile") return remote && mobileOS() === null;
    return true;
  });
}

/** Full-screen first-run wizard. Replaces the app shell until the user finishes
 *  or skips through it; `onComplete` swaps the real app in. */
export function Onboarding({ onComplete }: { onComplete: () => void }) {
  // Resolve managed (server owns its LLM) before fixing the step list, so the
  // "model" step is correctly skipped on a cloud tenant — including the proxy
  // thin client where isRemote() is false. Hold the first paint until known
  // (the shell shows its own "Starting Hygur…" cover meanwhile).
  const [managed, setManaged] = useState<boolean | null>(null);
  useEffect(() => {
    let cancelled = false;
    void api
      .config()
      .then((c) => {
        if (!cancelled) setManaged(!!c.managed);
      })
      .catch(() => {
        if (!cancelled) setManaged(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);
  const STEPS = useMemo(
    () => (managed === null ? null : visibleSteps(managed)),
    [managed],
  );
  const [index, setIndex] = useState(0);
  // Plays the "reveal" exit (the wizard lifts away) before the app swaps in.
  const [leaving, setLeaving] = useState(false);

  if (!STEPS) return null; // brief async check; nothing painted yet
  const step = STEPS[index];
  const isLast = index === STEPS.length - 1;
  // Hosted experience (remote tenant or a managed/proxy server that owns the
  // LLM) → context-correct copy, never the "runs on this Mac" local story.
  const cloud = isRemote() || !!managed;

  const complete = (route?: string) => {
    if (route) window.location.hash = route;
    markOnboardingComplete();
    setLeaving(true);
    window.setTimeout(onComplete, 450); // let the reveal animation play first
  };
  const next = () => {
    if (isLast) complete();
    else setIndex((i) => Math.min(STEPS.length - 1, i + 1));
  };
  const back = () => setIndex((i) => Math.max(0, i - 1));

  const ctx: StepContext = { next, complete };

  return (
    <div
      className={`fixed inset-0 z-50 flex flex-col bg-bg transition-[opacity,transform] duration-[450ms] ease-out ${
        leaving ? "pointer-events-none scale-[1.04] opacity-0" : "scale-100 opacity-100"
      }`}
    >
      <div className="flex-1 overflow-y-auto">
        <div
          key={step.id}
          className="view-enter mx-auto flex min-h-full max-w-[560px] flex-col justify-center px-8 py-12"
        >
          {step.id === "welcome" && <StepWelcome cloud={cloud} />}
          {step.id === "permissions" && <StepPermissions />}
          {step.id === "model" && <StepModel ctx={ctx} />}
          {step.id === "accounts" && <StepAccounts ctx={ctx} cloud={cloud} />}
          {step.id === "notifications" && <StepNotifications />}
          {step.id === "mobile" && <StepMobile />}
          {step.id === "ready" && <StepReady cloud={cloud} />}
        </div>
      </div>

      <footer className="flex items-center gap-4 border-t border-border px-8 pt-4 pb-[calc(1rem_+_env(safe-area-inset-bottom))]">
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
