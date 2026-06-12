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
import { desktopHandoff } from "./lib/passkey";
import logo from "./assets/logo.png";

/** Set when the web shell was opened by the desktop app for a passkey handoff
 *  (cloud.hygur.ai/?desktop=<state>). The handoff must run whether or not the
 *  browser already has a session — when it does, Connect (login) never mounts,
 *  so Root performs the handback itself instead of dropping into the app. */
const DESKTOP_STATE = new URLSearchParams(window.location.search).get("desktop");

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
  // Desktop handoff (cloud.hygur.ai/?desktop=<state>) when the browser is ALREADY
  // signed in: Connect never mounts, so Root stashes the session for the native
  // app and bounces back via hygur://. "" until attempted, then "done"/an error.
  const [handoff, setHandoff] = useState<"idle" | "done" | "error">("idle");
  const [handoffErr, setHandoffErr] = useState<string | null>(null);

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

  // Desktop handoff for an ALREADY-signed-in browser: once the token is restored
  // (booting done) and we have a key, stash the session for the native app and
  // bounce via hygur://. When NOT signed in, needsConnection() routes to Connect,
  // which runs the same handoff after login — so this only covers the gap.
  useEffect(() => {
    if (!DESKTOP_STATE || booting || handoff !== "idle" || !apiKey()) return;
    void desktopHandoff(DESKTOP_STATE)
      .then(() => {
        setHandoff("done");
        window.location.href = `hygur://auth?state=${encodeURIComponent(DESKTOP_STATE)}`;
      })
      .catch((e: unknown) => {
        setHandoffErr(e instanceof Error ? e.message : String(e));
        setHandoff("error");
      });
  }, [booting, handoff]);

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
  // When opened for a desktop handoff while signed OUT, Connect runs the handoff
  // itself after login.
  if (needsConnection()) return <Connect />;
  // Cloud shell restoring its in-memory access token from the refresh cookie.
  if (booting) return null;
  // Signed in AND opened by the desktop app → hand the session back, don't show
  // the web app (that's the bug: the browser would just load the user's data).
  if (DESKTOP_STATE) return <DesktopHandback phase={handoff} error={handoffErr} />;
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

/** Shown on the web shell when the desktop app opened it for a passkey handoff and
 *  the browser is already signed in: the session is being stashed + handed back to
 *  the native app via hygur://. */
function DesktopHandback({
  phase,
  error,
}: {
  phase: "idle" | "done" | "error";
  error: string | null;
}) {
  return (
    <div className="fixed inset-0 z-50 flex flex-col items-center justify-center bg-bg px-8 text-center">
      <img src={logo} alt="" className="mb-5 size-16 rounded-[22%] shadow-sm" />
      {phase === "error" ? (
        <>
          <h1 className="font-display text-[22px] font-semibold tracking-tight">
            Couldn't hand off
          </h1>
          <p className="mt-2 max-w-[42ch] text-[13.5px] leading-relaxed text-muted">
            {error || "The desktop sign-in could not be prepared."} Close this tab and try “Sign in
            with a passkey” again from the app.
          </p>
        </>
      ) : phase === "done" ? (
        <>
          <h1 className="font-display text-[22px] font-semibold tracking-tight">Back to the app</h1>
          <p className="mt-2 max-w-[42ch] text-[13.5px] leading-relaxed text-muted">
            You're signed in. Return to the Hygur app — if it didn't open automatically, your
            browser will ask for permission to launch it. You can close this tab.
          </p>
        </>
      ) : (
        <>
          <h1 className="font-display text-[22px] font-semibold tracking-tight">Signing you in…</h1>
          <p className="mt-2 max-w-[42ch] text-[13.5px] leading-relaxed text-muted">
            Handing your session back to the Hygur app.
          </p>
        </>
      )}
    </div>
  );
}
