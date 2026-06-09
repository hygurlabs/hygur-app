import { useEffect, useState } from "react";
import App from "./App.tsx";
import { Onboarding } from "./onboarding/Onboarding";
import { Connect } from "./onboarding/Connect";
import { ModePicker } from "./onboarding/ModePicker";
import { isOnboardingComplete } from "./lib/onboarding";
import { needsConnection, SIGNED_OUT_EVENT } from "./lib/connection";
import { isDesktop, getDesktopConfig } from "./lib/desktop";

/** Gates the app behind first-run mode selection + onboarding. `done`/`modeChosen
 *  === null` are the brief async checks; the WebShellView paints its own
 *  "Starting Hygur…" cover until the page is ready, so a blank frame here is
 *  invisible to the user. */
export function Root() {
  const [done, setDone] = useState<boolean | null>(null);
  // Desktop-only: has the user picked local vs cloud yet? Synchronously `true` in
  // a plain browser (no picker there); `null` on desktop until the async check.
  const [modeChosen, setModeChosen] = useState<boolean | null>(() =>
    isDesktop() ? null : true,
  );
  // Bumped on sign-out so the connection gate below re-evaluates: a dead session
  // (refresh failed → clearTokens cleared the endpoint) reactively routes to
  // Connect instead of stranding the user on a 401-ing app shell.
  const [, setSignedOutTick] = useState(0);

  useEffect(() => {
    const onSignedOut = () => setSignedOutTick((n) => n + 1);
    window.addEventListener(SIGNED_OUT_EVENT, onSignedOut);
    return () => window.removeEventListener(SIGNED_OUT_EVENT, onSignedOut);
  }, []);

  useEffect(() => {
    let cancelled = false;
    void isOnboardingComplete().then((v) => {
      if (!cancelled) setDone(v);
    });
    if (isDesktop()) {
      void getDesktopConfig()
        .then((c) => {
          if (!cancelled) setModeChosen(c.mode === "cloud" || c.mode === "local");
        })
        .catch(() => {
          if (!cancelled) setModeChosen(true); // don't block on a read error
        });
    }
    return () => {
      cancelled = true;
    };
  }, []);

  // No server reachable yet (packaged client / bare browser) → connect first.
  if (needsConnection()) return <Connect />;
  // Desktop first run: choose the engine mode before anything else.
  if (modeChosen === null) return null;
  if (!modeChosen) return <ModePicker onDone={() => setModeChosen(true)} />;
  if (done === null) return null;
  if (!done) return <Onboarding onComplete={() => setDone(true)} />;
  return <App />;
}
