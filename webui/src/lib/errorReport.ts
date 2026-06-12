import { CONSOLE_URL, isRemote } from "./connection";

// First-party, cookieless crash reporting → the operator console (/errors). No
// third-party SDK, no cookies. Gated on isRemote(): only CLOUD sessions report;
// a local/self-hosted instance keeps its errors on the user's machine.

const seen = new Set<string>();
let sent = 0;
const MAX_PER_SESSION = 25; // never flood the console from one wedged client

/** The running bundle filename — enough to correlate a report with a build. */
function appVersion(): string {
  const src =
    document.querySelector('script[src*="/assets/index-"]')?.getAttribute("src") ?? "";
  const m = src.match(/index-[\w-]+\.js/);
  return m ? m[0] : "";
}

/** Best-effort report of one client error. Deduped + capped; never throws. */
export function reportClientError(message: string, stack?: string): void {
  if (!isRemote() || !CONSOLE_URL) return; // local-first: nothing leaves the machine
  const msg = (message || "").trim().slice(0, 2000);
  if (!msg) return;
  const key = msg + "|" + (stack ?? "").slice(0, 200);
  if (seen.has(key) || sent >= MAX_PER_SESSION) return;
  seen.add(key);
  sent++;
  try {
    void fetch(`${CONSOLE_URL.replace(/\/$/, "")}/errors`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        message: msg,
        stack: (stack ?? "").slice(0, 8000),
        url: location.href,
        app_version: appVersion(),
      }),
      keepalive: true, // survive a navigation/unload
    }).catch(() => {});
  } catch {
    /* reporting must never itself throw */
  }
}

/** Install global handlers once (uncaught errors + unhandled promise rejections). */
export function installErrorReporting(): void {
  window.addEventListener("error", (e) => {
    reportClientError(e.message || String(e.error ?? "error"), e.error?.stack);
  });
  window.addEventListener("unhandledrejection", (e) => {
    const r = e.reason as { message?: string; stack?: string } | undefined;
    reportClientError(
      r?.message ? `Unhandled rejection: ${r.message}` : `Unhandled rejection: ${String(e.reason)}`,
      r?.stack,
    );
  });
}
