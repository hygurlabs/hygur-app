import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { listen } from "@tauri-apps/api/event";
import { Button, TextInput } from "../components/ui";
import {
  getDesktopConfig,
  openExternal,
  randomState,
  setDesktopConfig,
  waitForSidecarThenReload,
  type DesktopConfigInput,
} from "../lib/desktop";
import { desktopClaim } from "../lib/passkey";
import logo from "../assets/logo.png";

/** Web shell that runs the passkey ceremony for the desktop handoff. */
const SHELL_URL = "https://cloud.hygur.ai";

/** Desktop engine-mode chooser. Shown full-screen at first run (no onCancel), or
 *  as a modal from Settings to switch/edit later (onCancel closes it). Writes the
 *  choice to ~/.hygur-edge/config.json via the Tauri core; a proxy-mode change
 *  restarts the sidecar and reloads, otherwise it just proceeds. */
export function ModePicker({
  onDone,
  onCancel,
}: {
  onDone: () => void;
  onCancel?: () => void;
}) {
  const [view, setView] = useState<"cards" | "cloud">("cards");
  const [server, setServer] = useState("");
  const [token, setToken] = useState("");
  const [tokenSet, setTokenSet] = useState(false);
  const [folder, setFolder] = useState("");
  const [protonUser, setProtonUser] = useState("");
  const [protonPassword, setProtonPassword] = useState("");
  const [protonPwSet, setProtonPwSet] = useState(false);
  const [protonMailbox, setProtonMailbox] = useState("All Mail");
  const [intervalMin, setIntervalMin] = useState(15);
  const [busy, setBusy] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Desktop passkey handoff: waiting for the browser, then the signed-in instance.
  const [waiting, setWaiting] = useState(false);
  const [signedInAs, setSignedInAs] = useState<string | null>(null);
  const unlistenRef = useRef<(() => void) | null>(null);

  // Tear down a dangling deep-link listener if the user navigates away mid-handoff.
  useEffect(() => {
    return () => {
      unlistenRef.current?.();
      unlistenRef.current = null;
    };
  }, []);

  // Prefill from the stored config so the Settings re-config path can edit values
  // (secrets come back only as *_set flags — blank fields keep them).
  useEffect(() => {
    let cancelled = false;
    void getDesktopConfig()
      .then((c) => {
        if (cancelled) return;
        setServer(c.server);
        setTokenSet(c.token_set);
        setFolder(c.folder);
        setProtonUser(c.proton_user);
        setProtonPwSet(c.proton_password_set);
        setProtonMailbox(c.proton_mailbox || "All Mail");
        setIntervalMin(c.interval_secs > 0 ? Math.round(c.interval_secs / 60) : 15);
        if (c.mode === "cloud") setView("cloud");
      })
      .catch((e) => {
        // Surface it rather than render an empty form silently — if the Tauri
        // IPC is unreachable, the user (and we) need to see why.
        if (!cancelled) setError(`Couldn't read desktop config — ${(e as Error).message}`);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const apply = async (input: DesktopConfigInput) => {
    setBusy(true);
    setError(null);
    try {
      const restarted = await setDesktopConfig(input);
      if (restarted) {
        setRestarting(true);
        await waitForSidecarThenReload();
      } else {
        onDone();
      }
    } catch (e) {
      setError((e as Error).message ?? String(e));
      setBusy(false);
    }
  };

  const chooseLocal = () => void apply({ mode: "local" });

  const chooseCloud = async () => {
    const ep = server.trim().replace(/\/+$/, "");
    if (!ep) return setError("Enter your Hygur Cloud server URL.");
    if (!tokenSet && !token.trim()) return setError("A device token is required.");
    setError(null);
    // No client-side reachability probe here: the desktop webview can't reach the
    // tenant cross-origin (CORS → "Load failed"), while the sidecar proxies to it
    // server-side just fine. apply() restarts the sidecar in cloud mode — that
    // restart IS the real connection test.
    await apply({
      mode: "cloud",
      server: ep,
      token: token.trim(),
      folder: folder.trim(),
      proton_user: protonUser.trim(),
      proton_password: protonPassword,
      proton_mailbox: protonMailbox.trim() || "All Mail",
      interval_secs: Math.max(0, Math.round(intervalMin)) * 60,
    });
  };

  // Passkey sign-in: WebAuthn can't run in the loopback webview, so the ceremony
  // happens in the system browser (cloud.hygur.ai). It stashes a long-lived token
  // keyed by a one-time `state`, then wakes us via the hygur:// deep link carrying
  // only that `state`; we claim the bundle and pre-fill the server + token. The
  // user reviews local sources, then hits Connect.
  const signInWithPasskey = async () => {
    setError(null);
    setSignedInAs(null);
    const state = randomState();
    let settled = false;
    try {
      unlistenRef.current?.();
      unlistenRef.current = await listen<string>("deeplink-auth", (e) => {
        if (settled) return;
        let parsed: URL;
        try {
          parsed = new URL(e.payload);
        } catch {
          return; // not a URL we recognize
        }
        if (parsed.searchParams.get("state") !== state) return; // stale / unrelated deep link
        // The console issues its own one-time claim handle; redeem THAT, not our
        // correlation nonce (which only proves this deep link is ours).
        const claim = parsed.searchParams.get("claim");
        if (!claim) return;
        settled = true;
        unlistenRef.current?.();
        unlistenRef.current = null;
        void desktopClaim(claim)
          .then((b) => {
            setServer(b.endpoint);
            setToken(b.access_token);
            setTokenSet(true);
            setSignedInAs(b.tenant_id);
            setWaiting(false);
          })
          .catch((err) => {
            setError((err as Error).message);
            setWaiting(false);
          });
      });
      setWaiting(true);
      await openExternal(`${SHELL_URL}/?desktop=${state}`);
    } catch (err) {
      unlistenRef.current?.();
      unlistenRef.current = null;
      setError(`Couldn't open the browser — ${(err as Error).message}`);
      setWaiting(false);
    }
  };

  if (restarting) {
    return createPortal(
      <div className="fixed inset-0 z-50 flex flex-col items-center justify-center bg-bg px-8 text-center">
        <img src={logo} alt="" className="mb-5 size-16 rounded-[22%] shadow-sm" />
        <p className="text-[14px] font-medium">Applying your choice…</p>
        <p className="mt-1 text-[13px] text-muted">Restarting Hygur — one moment.</p>
      </div>,
      document.body,
    );
  }

  return createPortal(
    // Scrollable overlay: centers when the form fits, scrolls when the window is
    // short (the cloud form is tall — otherwise the Connect button is unreachable).
    <div className="fixed inset-0 z-50 overflow-y-auto bg-bg">
      <div className="flex min-h-full flex-col items-center justify-center px-8 py-10">
        <div className="w-full max-w-[460px]">
        <div className="mb-7 flex flex-col items-center text-center">
          <img src={logo} alt="" className="mb-4 size-20 rounded-[22%] shadow-sm" />
          <h1 className="font-display text-[26px] font-semibold leading-tight tracking-tight">
            {view === "cards" ? "How do you want to run Hygur?" : "Connect to Hygur Cloud"}
          </h1>
          <p className="mt-2 max-w-[44ch] text-[13.5px] leading-relaxed text-muted">
            {view === "cards"
              ? "Run everything locally on this Mac, or use Hygur Cloud and let this app push your local files & mail to it."
              : "Your local files & mail stay on this device; only extracted text is pushed. The token is stored locally, never in the browser."}
          </p>
        </div>

        {error && (
          <div className="mb-4 rounded-lg border border-danger/40 bg-danger/5 px-3.5 py-2.5 text-[12.5px] text-danger">
            {error}
          </div>
        )}

        {view === "cards" ? (
          <div className="flex flex-col gap-3">
            <button
              onClick={() => setView("cloud")}
              disabled={busy}
              className="rounded-xl border border-border px-4 py-3.5 text-left transition-colors hover:border-accent hover:bg-accent/5 disabled:opacity-50"
            >
              <div className="text-[14px] font-semibold">Hygur Cloud</div>
              <div className="mt-0.5 text-[12.5px] text-muted">
                Thin client — your knowledge base, AI and search run in the cloud; this app pushes local sources.
              </div>
            </button>
            <button
              onClick={chooseLocal}
              disabled={busy}
              className="rounded-xl border border-border px-4 py-3.5 text-left transition-colors hover:border-accent hover:bg-accent/5 disabled:opacity-50"
            >
              <div className="text-[14px] font-semibold">Run locally</div>
              <div className="mt-0.5 text-[12.5px] text-muted">
                Full local engine — everything stays on this Mac (you configure the LLM endpoints).
              </div>
            </button>
            {onCancel && (
              <button
                onClick={onCancel}
                className="mt-1 self-center rounded-lg px-2 py-1.5 text-[13px] text-muted transition-colors hover:text-text"
              >
                Cancel
              </button>
            )}
          </div>
        ) : (
          <div className="flex flex-col gap-3">
            {signedInAs ? (
              <div className="rounded-lg border border-accent/40 bg-accent/5 px-3.5 py-2.5 text-[12.5px]">
                <span className="font-medium text-accent">✓ Signed in as {signedInAs}</span>
                <span className="text-muted"> — review your local sources, then Connect.</span>
              </div>
            ) : (
              <>
                <Button onClick={signInWithPasskey} disabled={busy || waiting}>
                  {waiting ? "Waiting for your browser…" : "Sign in with a passkey"}
                </Button>
                {waiting && (
                  <button
                    onClick={() => {
                      unlistenRef.current?.();
                      unlistenRef.current = null;
                      setWaiting(false);
                    }}
                    className="self-center text-[12.5px] text-muted transition-colors hover:text-text"
                  >
                    Cancel
                  </button>
                )}
                <div className="flex items-center gap-3 text-[11.5px] uppercase tracking-wide text-muted">
                  <span className="h-px flex-1 bg-border" />
                  or enter a device token
                  <span className="h-px flex-1 bg-border" />
                </div>
              </>
            )}
            <label className="block">
              <span className="mb-1.5 block text-[13px] font-medium">Server URL</span>
              <TextInput
                value={server}
                spellCheck={false}
                autoCapitalize="off"
                placeholder="https://cloud.hygur.ai"
                onChange={(e) => setServer(e.target.value)}
              />
            </label>
            <label className="block">
              <span className="mb-1.5 block text-[13px] font-medium">
                Device token{tokenSet && " (leave blank to keep current)"}
              </span>
              <TextInput
                type="password"
                value={token}
                spellCheck={false}
                autoCapitalize="off"
                placeholder={tokenSet ? "••••••••" : "device JWT"}
                onChange={(e) => setToken(e.target.value)}
              />
            </label>

            <div className="mt-1 text-[12px] font-medium uppercase tracking-wide text-muted">
              Local sources (optional)
            </div>
            <label className="block">
              <span className="mb-1.5 block text-[13px] font-medium">Files folder</span>
              <TextInput
                value={folder}
                spellCheck={false}
                autoCapitalize="off"
                placeholder="/Users/you/Documents/work"
                onChange={(e) => setFolder(e.target.value)}
              />
            </label>
            <label className="block">
              <span className="mb-1.5 block text-[13px] font-medium">Proton Bridge email</span>
              <TextInput
                value={protonUser}
                spellCheck={false}
                autoCapitalize="off"
                placeholder="you@proton.me (Proton Bridge running)"
                onChange={(e) => setProtonUser(e.target.value)}
              />
            </label>
            {protonUser.trim() && (
              <label className="block">
                <span className="mb-1.5 block text-[13px] font-medium">
                  Proton Bridge password{protonPwSet && " (leave blank to keep current)"}
                </span>
                <TextInput
                  type="password"
                  value={protonPassword}
                  spellCheck={false}
                  autoCapitalize="off"
                  placeholder={protonPwSet ? "••••••••" : "Bridge app password"}
                  onChange={(e) => setProtonPassword(e.target.value)}
                />
              </label>
            )}
            <label className="block">
              <span className="mb-1.5 block text-[13px] font-medium">Sync every (minutes, 0 = manual)</span>
              <TextInput
                type="number"
                value={String(intervalMin)}
                onChange={(e) => setIntervalMin(Number(e.target.value) || 0)}
              />
            </label>

            <div className="mt-2 flex items-center gap-3">
              <Button onClick={chooseCloud} disabled={busy}>
                {busy ? "Connecting…" : "Connect"}
              </Button>
              <button
                onClick={() => {
                  setError(null);
                  setView("cards");
                }}
                disabled={busy}
                className="rounded-lg px-2 py-1.5 text-[13px] text-muted transition-colors hover:text-text disabled:opacity-50"
              >
                Back
              </button>
            </div>
          </div>
        )}
        </div>
      </div>
    </div>,
    document.body,
  );
}
