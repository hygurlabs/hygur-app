// Desktop engine-mode (local↔cloud) plumbing. The switch is a desktop-only
// setting persisted by the Tauri core to ~/.hygur-edge/config.json (no sidecar
// HTTP endpoint — smaller attack surface). In a plain browser these are no-ops /
// throw, so callers gate on isDesktop().

import { invoke, isTauri } from "@tauri-apps/api/core";

/** True when running inside the Tauri desktop shell (vs a plain browser). */
export function isDesktop(): boolean {
  try {
    return isTauri();
  } catch {
    return false;
  }
}

/** Redacted desktop config (secrets reported as *_set, never echoed). */
export interface DesktopConfig {
  mode: string; // "cloud" | "local" | ""
  server: string;
  folder: string;
  proton_user: string;
  proton_mailbox: string;
  interval_secs: number;
  backfill_count: number; // mails fetched per folder on its first sync
  token_set: boolean;
  proton_password_set: boolean;
}

/** Outgoing desktop config. Blank token / proton_password = keep the stored one. */
export interface DesktopConfigInput {
  mode: string;
  server?: string;
  token?: string;
  folder?: string;
  proton_user?: string;
  proton_password?: string;
  proton_mailbox?: string;
  interval_secs?: number;
  backfill_count?: number; // 0 / omitted = keep stored
}

/** Rejects after `ms` so a hung IPC never wedges the UI behind a pending promise. */
function withTimeout<T>(p: Promise<T>, ms: number): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const t = setTimeout(() => reject(new Error(`Tauri command timed out after ${ms}ms`)), ms);
    p.then(
      (v) => {
        clearTimeout(t);
        resolve(v);
      },
      (e) => {
        clearTimeout(t);
        reject(e instanceof Error ? e : new Error(String(e)));
      },
    );
  });
}

export async function getDesktopConfig(): Promise<DesktopConfig> {
  return withTimeout(invoke<DesktopConfig>("get_desktop_config"), 5000);
}

/** Opens a URL in the user's default browser via the Tauri shell. Used to run the
 *  passkey ceremony where WebAuthn works (cloud.hygur.ai), since the loopback
 *  webview can't satisfy the hygur.ai relying party. */
export async function openExternal(url: string): Promise<void> {
  await withTimeout(invoke<void>("open_external", { url }), 5000);
}

/** A 128-bit hex nonce for the desktop passkey handoff. Identifies the one-time
 *  token bundle the browser stashes; the only value that travels in the deep link. */
export function randomState(): string {
  const a = new Uint8Array(16);
  crypto.getRandomValues(a);
  return Array.from(a, (b) => b.toString(16).padStart(2, "0")).join("");
}

/** Persists the config. Returns true if the sidecar was restarted (a proxy-mode
 *  change), false if only source fields changed (the running edge loop re-reads
 *  those, no restart). The caller reloads only when restarted. */
export async function setDesktopConfig(cfg: DesktopConfigInput): Promise<boolean> {
  return withTimeout(invoke<boolean>("set_desktop_config", { cfg }), 8000);
}

/** After a mode change the sidecar is killed + respawned (~1s). Poll /health
 *  until it's back, then reload so the SPA picks up the new mode (managed flag,
 *  proxied data routes). Reloads anyway after the timeout as a last resort. */
export async function waitForSidecarThenReload(timeoutMs = 15000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  // Small initial delay: the supervisor sleeps ~500ms before respawning.
  await new Promise((r) => setTimeout(r, 700));
  while (Date.now() < deadline) {
    try {
      const res = await fetch("/health", { cache: "no-store" });
      if (res.ok) break;
    } catch {
      /* sidecar still down — keep polling */
    }
    await new Promise((r) => setTimeout(r, 300));
  }
  window.location.reload();
}
