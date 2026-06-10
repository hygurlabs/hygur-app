import { useEffect, useState } from "react";
import App from "./App.tsx";
import { Onboarding } from "./onboarding/Onboarding";
import { Connect } from "./onboarding/Connect";
import { ModePicker } from "./onboarding/ModePicker";
import { isOnboardingComplete } from "./lib/onboarding";
import {
  apiKey,
  isRemote,
  needsConnection,
  refreshAccessToken,
  SIGNED_OUT_EVENT,
} from "./lib/connection";
import { isDesktop, getDesktopConfig } from "./lib/desktop";

/** Gates the app behind first-run mode selection + onboarding. `done`/`modeChosen
 *  === null` are the brief async checks; the WebShellView paints its own
 *  "Starting Hygur…" cover until the page is ready, so a blank frame here is
 *  invisible to the user. */
export function Root() {
  const [done, setDone] = useState<boolean | null>(null);
  // True only on the transition out of onboarding, so App can play its reveal
  // fade-in once (and not on every plain launch).
  const [justOnboarded, setJustOnboarded] = useState(false);
  // Desktop-only: has the user picked local vs cloud yet? Synchronously `true` in
  // a plain browser (no picker there); `null` on desktop until the async check.
  const [modeChosen, setModeChosen] = useState<boolean | null>(() =>
    isDesktop() ? null : true,
  );
  // Bumped on sign-out so the connection gate below re-evaluates: a dead session
  // (refresh failed → clearTokens cleared the endpoint) reactively routes to
  // Connect instead of stranding the user on a 401-ing app shell.
  const [, setSignedOutTick] = useState(0);
  // Cloud shell: the access token lives in memory (Phase B), so on a fresh load
  // we restore it from the refresh cookie before showing the app. Only when we're
  // remote with no key yet (a persisted/manual key or the desktop META token make
  // this false → no bootstrap needed).
  const [booting, setBooting] = useState<boolean>(() => isRemote() && !apiKey());

  useEffect(() => {
    const onSignedOut = () => setSignedOutTick((n) => n + 1);
    window.addEventListener(SIGNED_OUT_EVENT, onSignedOut);
    return () => window.removeEventListener(SIGNED_OUT_EVENT, onSignedOut);
  }, []);

  useEffect(() => {
    if (!booting) return;
    let cancelled = false;
    // Failure (dead cookie) calls clearTokens → SIGNED_OUT → re-render to Connect.
    void refreshAccessToken().finally(() => {
      if (!cancelled) setBooting(false);
    });
    return () => {
      cancelled = true;
    };
  }, [booting]);

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
  // Cloud shell restoring its in-memory access token from the refresh cookie.
  if (booting) return null;
  // Desktop first run: choose the engine mode before anything else.
  if (modeChosen === null) return null;
  if (!modeChosen) return <ModePicker onDone={() => setModeChosen(true)} />;
  if (done === null) return null;
  if (!done)
    return (
      <Onboarding
        onComplete={() => {
          setJustOnboarded(true);
          setDone(true);
        }}
      />
    );
  return <App revealOnMount={justOnboarded} />;
}
