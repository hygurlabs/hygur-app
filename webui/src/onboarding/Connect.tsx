import { useState } from "react";
import { setConnection } from "../lib/connection";
import { Button, TextInput } from "../components/ui";
import logo from "../assets/logo.png";

/** First-run screen for a packaged thin client (Tauri) or a bare browser: point
 *  the app at a Hygur server endpoint and paste its device key. Persists the
 *  connection (localStorage) and reloads so the app boots against it. */
export function Connect() {
  const [endpoint, setEndpoint] = useState("https://app.hygur.eu");
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const connect = async () => {
    const ep = endpoint.trim().replace(/\/+$/, "");
    if (!ep || !key.trim()) return;
    setBusy(true);
    setError(null);
    try {
      // Reachability + CORS sanity check (public route, no auth, simple GET).
      const r = await fetch(ep + "/version");
      if (!r.ok) throw new Error(`server responded ${r.status}`);
    } catch (e) {
      setError(`Couldn't reach ${ep} — ${(e as Error).message}`);
      setBusy(false);
      return;
    }
    setConnection(ep, key);
    window.location.reload();
  };

  return (
    <div className="fixed inset-0 z-50 flex flex-col items-center justify-center bg-bg px-8">
      <div className="w-full max-w-[420px]">
        <div className="mb-7 flex flex-col items-center text-center">
          <img src={logo} alt="" className="mb-4 size-20 rounded-[22%] shadow-sm" />
          <h1 className="font-display text-[26px] font-semibold leading-tight tracking-tight">
            Connect to Hygur
          </h1>
          <p className="mt-2 max-w-[40ch] text-[13.5px] leading-relaxed text-muted">
            Point this app at your Hygur server and paste its device key. Traffic
            stays between this device and your server.
          </p>
        </div>

        {error && (
          <div className="mb-4 rounded-lg border border-danger/40 bg-danger/5 px-3.5 py-2.5 text-[12.5px] text-danger">
            {error}
          </div>
        )}

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
        <Button onClick={connect} disabled={busy || !endpoint.trim() || !key.trim()}>
          {busy ? "Connecting…" : "Connect"}
        </Button>
      </div>
    </div>
  );
}
