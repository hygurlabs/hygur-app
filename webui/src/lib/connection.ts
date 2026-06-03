// How the web client reaches the Hygur API.
//
// Local (default): the app is served BY the local sidecar on the same origin,
// which injects the API token into the page. base = "" (same-origin), key = the
// injected meta token. This is the embedded/desktop-local experience.
//
// Remote: the user points the client at a Hygur server endpoint (Hygur Cloud /
// self-host, e.g. https://app.hygur.eu) and supplies a device key. Both are
// persisted in localStorage. This is what the desktop/mobile thin clients use —
// the API token is NOT served in the page (the public endpoint blocks the shell),
// so the client holds its own key. This module is the single seam the Tauri
// shell (P2.6) plugs into.

const META_TOKEN: string = (() => {
  const m = document.querySelector('meta[name="hygur-token"]')?.getAttribute("content") ?? "";
  return m === "__HYGUR_TOKEN__" ? "" : m;
})();

const ENDPOINT_KEY = "hygur.endpoint";
const API_KEY_KEY = "hygur.key";

function ls(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

/** Base URL prepended to every API path. "" = same-origin (local sidecar). */
export function apiBase(): string {
  const ep = ls(ENDPOINT_KEY);
  return ep ? ep.replace(/\/+$/, "") : "";
}

/** Key sent as X-Hygur-Token. A configured remote key wins; otherwise the
 *  same-origin token injected by the local sidecar. */
export function apiKey(): string {
  return ls(API_KEY_KEY) || META_TOKEN;
}

/** True when the client is configured to talk to a remote endpoint. */
export function isRemote(): boolean {
  return !!ls(ENDPOINT_KEY);
}

/** True when the client can authenticate: local same-origin, or a remote key set. */
export function isConfigured(): boolean {
  return !isRemote() || !!ls(API_KEY_KEY);
}

/** Current remote connection (empty strings in local mode). */
export function getConnection(): { endpoint: string; key: string } {
  return { endpoint: ls(ENDPOINT_KEY) ?? "", key: ls(API_KEY_KEY) ?? "" };
}

/** Persists a remote endpoint + key. Empty endpoint reverts to local mode. */
export function setConnection(endpoint: string, key: string): void {
  const ep = endpoint.trim().replace(/\/+$/, "");
  try {
    if (ep) {
      localStorage.setItem(ENDPOINT_KEY, ep);
      localStorage.setItem(API_KEY_KEY, key.trim());
    } else {
      clearConnection();
    }
  } catch {
    /* localStorage unavailable — stays in local mode */
  }
}

/** Clears the remote connection (back to same-origin/local). */
export function clearConnection(): void {
  try {
    localStorage.removeItem(ENDPOINT_KEY);
    localStorage.removeItem(API_KEY_KEY);
  } catch {
    /* ignore */
  }
}
