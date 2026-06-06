import { useState } from "react";
import { setConnection } from "../lib/connection";
import {
  enrollWithCode,
  passkeyLogin,
  passkeysSupported,
  registerPasskey,
} from "../lib/passkey";
import { Button, TextInput } from "../components/ui";
import logo from "../assets/logo.png";

type Mode = "login" | "enroll" | "advanced";

/** Hygur Cloud sign-in (the web shell at cloud.hygur.ai). Primary path: instance
 *  slug + passkey. Secondary: redeem a one-time enrollment code, then add a
 *  passkey. Fallback ("advanced"): point at any endpoint with a device key
 *  (self-host / debug). On success it persists the connection and reloads. */
export function Connect() {
  const [mode, setMode] = useState<Mode>(passkeysSupported() ? "login" : "advanced");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [instance, setInstance] = useState("");
  const [code, setCode] = useState("");
  const [enrolledToken, setEnrolledToken] = useState<string | null>(null);
  const [endpoint, setEndpoint] = useState("https://app.hygur.eu");
  const [key, setKey] = useState("");

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

  const doLogin = () =>
    run(async () => {
      await passkeyLogin(instance);
      window.location.reload();
    });

  const doEnroll = () =>
    run(async () => {
      const token = await enrollWithCode(code);
      setEnrolledToken(token); // connected — now offer to add a passkey
      setBusy(false);
    });

  const doRegister = () =>
    run(async () => {
      if (enrolledToken) await registerPasskey(enrolledToken);
      window.location.reload();
    });

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

  return (
    <div className="fixed inset-0 z-50 flex flex-col items-center justify-center bg-bg px-8">
      <div className="w-full max-w-[420px]">
        <div className="mb-7 flex flex-col items-center text-center">
          <img src={logo} alt="" className="mb-4 size-20 rounded-[22%] shadow-sm" />
          <h1 className="font-display text-[26px] font-semibold leading-tight tracking-tight">
            {enrolledToken ? "You're in" : "Sign in to Hygur Cloud"}
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
              {busy ? "Waiting for your passkey…" : "Add a passkey"}
            </Button>
            <button
              type="button"
              onClick={() => window.location.reload()}
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
                onChange={(e) => setInstance(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && instance.trim()) doLogin();
                }}
              />
            </label>
            <Button onClick={doLogin} disabled={busy || !instance.trim()}>
              {busy ? "Waiting for your passkey…" : "Continue with passkey"}
            </Button>
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
