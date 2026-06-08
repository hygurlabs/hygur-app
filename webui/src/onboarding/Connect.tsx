import { useState } from "react";
import { setConnection } from "../lib/connection";
import {
  desktopHandoff,
  enrollWithCode,
  passkeyLoginBegin,
  passkeyLoginFinish,
  passkeyRegisterBegin,
  passkeyRegisterFinish,
  passkeysSupported,
  type PasskeyChallenge,
} from "../lib/passkey";
import { Button, TextInput } from "../components/ui";
import logo from "../assets/logo.png";

type Mode = "login" | "enroll" | "advanced";

/** Set when the web shell was opened by the desktop app for a passkey handoff
 *  (cloud.hygur.ai/?desktop=<state>). After login we stash a long-lived token
 *  under this state and bounce back to the native app via the hygur:// scheme. */
const DESKTOP_STATE = new URLSearchParams(window.location.search).get("desktop");

/** Hygur Cloud sign-in (the web shell at cloud.hygur.ai). Primary path: instance
 *  slug + passkey. Secondary: redeem a one-time enrollment code, then add a
 *  passkey. Fallback ("advanced"): point at any endpoint with a device key
 *  (self-host / debug). On success it persists the connection and reloads. */
export function Connect() {
  const [mode, setMode] = useState<Mode>(passkeysSupported() ? "login" : "advanced");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [instance, setInstance] = useState("");
  // Two-phase passkey ceremonies (iOS focus rule): begin() fetches the options
  // and stashes the challenge here; a second, fresh tap then runs finish().
  const [loginChallenge, setLoginChallenge] = useState<PasskeyChallenge | null>(null);
  const [registerChallenge, setRegisterChallenge] = useState<PasskeyChallenge | null>(null);
  const [code, setCode] = useState("");
  const [enrolledToken, setEnrolledToken] = useState<string | null>(null);
  const [endpoint, setEndpoint] = useState("https://app.hygur.eu");
  const [key, setKey] = useState("");
  const [handedOff, setHandedOff] = useState(false);

  // After a successful sign-in: a desktop handoff hands the session back to the
  // native app (deep link) instead of using the session here; otherwise reload.
  const finish = async () => {
    if (DESKTOP_STATE) {
      await desktopHandoff(DESKTOP_STATE);
      setHandedOff(true);
      window.location.href = `hygur://auth?state=${encodeURIComponent(DESKTOP_STATE)}`;
      return;
    }
    window.location.reload();
  };

  // Runs an async action with busy/error handling. On success the caller reloads.
  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError((e as Error).message);
      setBusy(false);
    }
  };

  // First tap: fetch the assertion options, drop the keyboard, and wait for a
  // confirming tap. Second tap: run the WebAuthn ceremony as the first awaited
  // call in the gesture (iOS throws "document is not focused" otherwise).
  const doLogin = () => {
    if (loginChallenge) {
      run(async () => {
        await passkeyLoginFinish(loginChallenge);
        await finish();
      });
      return;
    }
    run(async () => {
      const challenge = await passkeyLoginBegin(instance);
      (document.activeElement as HTMLElement | null)?.blur?.();
      setLoginChallenge(challenge);
      setBusy(false); // re-enable so the user can confirm with a fresh tap
    });
  };

  const doEnroll = () =>
    run(async () => {
      const token = await enrollWithCode(code);
      setEnrolledToken(token); // connected — now offer to add a passkey
      setBusy(false);
    });

  // Same two-phase split as login (see doLogin).
  const doRegister = () => {
    if (!enrolledToken) return;
    if (registerChallenge) {
      run(async () => {
        await passkeyRegisterFinish(enrolledToken, registerChallenge);
        await finish();
      });
      return;
    }
    run(async () => {
      const challenge = await passkeyRegisterBegin(enrolledToken);
      setRegisterChallenge(challenge);
      setBusy(false);
    });
  };

  const doAdvanced = () =>
    run(async () => {
      const ep = endpoint.trim().replace(/\/+$/, "");
      if (!ep || !key.trim()) {
        setBusy(false);
        return;
      }
      const r = await fetch(ep + "/version");
      if (!r.ok) throw new Error(`Couldn't reach ${ep} — HTTP ${r.status}`);
      setConnection(ep, key);
      window.location.reload();
    });

  const switchTo = (m: Mode) => {
    setMode(m);
    setError(null);
  };

  // Desktop handoff complete: the native app is being woken via the deep link.
  if (handedOff) {
    return (
      <div className="fixed inset-0 z-50 flex flex-col items-center justify-center bg-bg px-8 text-center">
        <img src={logo} alt="" className="mb-5 size-16 rounded-[22%] shadow-sm" />
        <h1 className="font-display text-[22px] font-semibold tracking-tight">Back to the app</h1>
        <p className="mt-2 max-w-[40ch] text-[13.5px] leading-relaxed text-muted">
          You're signed in. Return to the Hygur app — if it didn't open automatically, your browser
          will ask for permission to launch it. You can close this tab.
        </p>
      </div>
    );
  }

  return (
    <div className="fixed inset-0 z-50 flex flex-col items-center justify-center bg-bg px-8">
      <div className="w-full max-w-[420px]">
        <div className="mb-7 flex flex-col items-center text-center">
          <img src={logo} alt="" className="mb-4 size-20 rounded-[22%] shadow-sm" />
          <h1 className="font-display text-[26px] font-semibold leading-tight tracking-tight">
            {enrolledToken
              ? "You're in"
              : DESKTOP_STATE
                ? "Sign in for the desktop app"
                : "Sign in to Hygur Cloud"}
          </h1>
          <p className="mt-2 max-w-[40ch] text-[13.5px] leading-relaxed text-muted">
            {enrolledToken
              ? "Add a passkey so next time you just enter your instance name and confirm with Touch ID or your security key."
              : mode === "login"
                ? "Enter your instance name and confirm with your passkey."
                : mode === "enroll"
                  ? "Paste the one-time enrollment code from your subscription page."
                  : "Point this app at a Hygur server and paste its device key."}
          </p>
        </div>

        {error && (
          <div className="mb-4 rounded-lg border border-danger/40 bg-danger/5 px-3.5 py-2.5 text-[12.5px] text-danger">
            {error}
          </div>
        )}

        {/* Post-enrollment: offer passkey registration. */}
        {enrolledToken ? (
          <div className="space-y-3">
            <Button onClick={doRegister} disabled={busy}>
              {busy
                ? registerChallenge
                  ? "Waiting for your passkey…"
                  : "Preparing…"
                : registerChallenge
                  ? "Confirm passkey creation"
                  : "Add a passkey"}
            </Button>
            {registerChallenge && !busy && (
              <p className="text-center text-[12px] text-muted">
                Tap again and confirm with Touch ID / Face ID.
              </p>
            )}
            <button
              type="button"
              onClick={() => void finish()}
              className="block w-full text-center text-[12.5px] text-muted hover:text-text"
            >
              Skip for now
            </button>
          </div>
        ) : mode === "login" ? (
          <>
            <label className="mb-5 block">
              <span className="mb-1.5 block text-[13px] font-medium">Instance name</span>
              <TextInput
                value={instance}
                spellCheck={false}
                autoCapitalize="off"
                placeholder="e.g. brave-otter-green"
                onChange={(e) => {
                  setInstance(e.target.value);
                  setLoginChallenge(null); // editing the slug invalidates a pending challenge
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && instance.trim()) doLogin();
                }}
              />
            </label>
            <Button onClick={doLogin} disabled={busy || !instance.trim()}>
              {busy
                ? loginChallenge
                  ? "Waiting for your passkey…"
                  : "Preparing…"
                : loginChallenge
                  ? "Confirm with your passkey"
                  : "Continue with passkey"}
            </Button>
            {loginChallenge && !busy && (
              <p className="mt-2 text-center text-[12px] text-muted">
                Tap again and confirm with Touch ID / Face ID.
              </p>
            )}
            <div className="mt-5 flex flex-col gap-1.5 text-center text-[12.5px] text-muted">
              <button type="button" onClick={() => switchTo("enroll")} className="hover:text-text">
                Have an enrollment code?
              </button>
              <button type="button" onClick={() => switchTo("advanced")} className="hover:text-text">
                Advanced — connect with a device key
              </button>
            </div>
          </>
        ) : mode === "enroll" ? (
          <>
            <label className="mb-5 block">
              <span className="mb-1.5 block text-[13px] font-medium">Enrollment code</span>
              <TextInput
                value={code}
                spellCheck={false}
                autoCapitalize="off"
                placeholder="one-time code"
                onChange={(e) => setCode(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && code.trim()) doEnroll();
                }}
              />
            </label>
            <Button onClick={doEnroll} disabled={busy || !code.trim()}>
              {busy ? "Enrolling…" : "Enroll this device"}
            </Button>
            <div className="mt-5 text-center text-[12.5px] text-muted">
              <button type="button" onClick={() => switchTo("login")} className="hover:text-text">
                ← Back to sign in
              </button>
            </div>
          </>
        ) : (
          <>
            <label className="mb-3 block">
              <span className="mb-1.5 block text-[13px] font-medium">Server endpoint</span>
              <TextInput
                value={endpoint}
                spellCheck={false}
                autoCapitalize="off"
                placeholder="https://app.hygur.eu"
                onChange={(e) => setEndpoint(e.target.value)}
              />
            </label>
            <label className="mb-5 block">
              <span className="mb-1.5 block text-[13px] font-medium">Device key</span>
              <TextInput
                type="password"
                value={key}
                spellCheck={false}
                autoCapitalize="off"
                placeholder="X-Hygur-Token / device JWT"
                onChange={(e) => setKey(e.target.value)}
              />
            </label>
            <Button onClick={doAdvanced} disabled={busy || !endpoint.trim() || !key.trim()}>
              {busy ? "Connecting…" : "Connect"}
            </Button>
            {passkeysSupported() && (
              <div className="mt-5 text-center text-[12.5px] text-muted">
                <button type="button" onClick={() => switchTo("login")} className="hover:text-text">
                  ← Back to passkey sign in
                </button>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}
