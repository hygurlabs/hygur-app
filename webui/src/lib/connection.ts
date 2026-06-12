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
const REFRESH_KEY = "hygur.refresh";

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

// The cloud-shell access token is held IN MEMORY (not localStorage) so an XSS
// can't read it; it's restored from the refresh cookie on page load. Fallbacks:
// a *persisted* key (manual self-host connect, refresh-less) and the loopback
// META token (desktop / local sidecar). Lost on reload by design for the cloud
// shell — Root refreshes it at startup.
let memAccess = "";

/** Key sent as X-Hygur-Token: the in-memory cloud access token, else a persisted
 *  manual key, else the same-origin token injected by the local sidecar. */
export function apiKey(): string {
  return memAccess || ls(API_KEY_KEY) || META_TOKEN;
}

/** The token injected by the LOCAL sidecar that served this page (empty on a
 *  static host like the cloud web shell). Used for same-origin, sidecar-local
 *  routes (/edge/*) that must bypass any configured remote endpoint/key. */
export function localToken(): string {
  return META_TOKEN;
}

/** True when the client is configured to talk to a remote endpoint. */
export function isRemote(): boolean {
  return !!ls(ENDPOINT_KEY);
}

/** True when the client can authenticate: local same-origin, or a key available
 *  (in memory, persisted, or injected). */
export function isConfigured(): boolean {
  return !isRemote() || !!apiKey();
}

/** True when there is no way to reach the API yet: no remote endpoint configured
 *  AND no same-origin token injected by a local sidecar. This is the case in a
 *  packaged thin client (Tauri) or a bare browser, where the user must point at
 *  a server first. */
export function needsConnection(): boolean {
  return !isRemote() && META_TOKEN === "";
}

/** Current remote connection (empty strings in local mode). */
export function getConnection(): { endpoint: string; key: string } {
  return { endpoint: ls(ENDPOINT_KEY) ?? "", key: memAccess || ls(API_KEY_KEY) || "" };
}

/** Sets a remote endpoint + key. By default the key is held IN MEMORY (the cloud
 *  shell, whose access token is restored from the refresh cookie on load).
 *  `persist=true` stores it in localStorage — for a manual, refresh-less self-host
 *  key that must survive a reload. Empty endpoint reverts to local mode. */
export function setConnection(endpoint: string, key: string, persist = false): void {
  const ep = endpoint.trim().replace(/\/+$/, "");
  if (!ep) {
    clearConnection();
    return;
  }
  try {
    localStorage.setItem(ENDPOINT_KEY, ep);
    if (persist) {
      localStorage.setItem(API_KEY_KEY, key.trim());
      memAccess = "";
    } else {
      memAccess = key.trim();
      localStorage.removeItem(API_KEY_KEY); // don't let a stale persisted key shadow
    }
  } catch {
    // localStorage unavailable — keep the in-memory access at least usable.
    if (!persist) memAccess = key.trim();
  }
}

/** Clears the remote connection (back to same-origin/local). */
export function clearConnection(): void {
  memAccess = "";
  try {
    localStorage.removeItem(ENDPOINT_KEY);
    localStorage.removeItem(API_KEY_KEY);
  } catch {
    /* ignore */
  }
}

/** Base URL of the control plane (enroll + passkey ceremonies + token refresh).
 *  Cross-origin from the cloud web shell; overridable for dev via the
 *  "hygur.console" localStorage key. */
export const CONSOLE_URL: string = (ls("hygur.console") || "https://console.hygur.ai").replace(
  /\/+$/,
  "",
);

/** Persists the token bundle from a passkey login / enrollment / refresh: the
 *  tenant endpoint + access key (sent to the tenant) + the refresh token (held
 *  for renewing the short-lived access token against the control plane). */
export function setTokens(endpoint: string, accessToken: string): void {
  setConnection(endpoint, accessToken);
  // The refresh token now lives in an HttpOnly cookie set by the console — never
  // store it in JS-readable localStorage. Clear any legacy value (migration).
  try {
    localStorage.removeItem(REFRESH_KEY);
  } catch {
    /* ignore */
  }
}

/** Event fired on full sign-out so the app can reactively route back to Connect
 *  (instead of stranding the user on a 401-ing app shell until a manual reload). */
export const SIGNED_OUT_EVENT = "hygur:signed-out";

/** Drops the connection AND the refresh token (full sign-out). */
export function clearTokens(): void {
  clearConnection();
  try {
    localStorage.removeItem(REFRESH_KEY);
  } catch {
    /* ignore */
  }
  // Clear the HttpOnly refresh cookie server-side (fire-and-forget).
  try {
    void fetch(`${CONSOLE_URL}/token/logout`, { method: "POST", credentials: "include" });
  } catch {
    /* ignore */
  }
  try {
    window.dispatchEvent(new Event(SIGNED_OUT_EVENT));
  } catch {
    /* non-browser / SSR — ignore */
  }
}

let refreshing: Promise<boolean> | null = null;

/** Exchanges the stored refresh token for a fresh access token (rotating both)
 *  against the control plane. De-duped so several concurrent 401s trigger a
 *  single refresh. Returns false (and signs out) when there is no/invalid refresh
 *  token. Talks to the console directly to avoid an api.ts import cycle. */
export function refreshAccessToken(): Promise<boolean> {
  if (refreshing) return refreshing;
  refreshing = (async (): Promise<boolean> => {
    // The refresh token rides in an HttpOnly cookie (credentials:"include" sends
    // it); the web shell never holds it in JS. The legacy localStorage→body
    // bootstrap was removed once all sessions were on the cookie.
    try {
      const r = await fetch(`${CONSOLE_URL}/token/refresh`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: "{}",
      });
      if (!r.ok) {
        // Only a definitive auth rejection means the session is dead → sign out.
        // Transient failures (5xx, network) keep state for the next try, so a
        // console hiccup doesn't log the user out.
        if (r.status === 401 || r.status === 403) clearTokens();
        return false;
      }
      const b = (await r.json()) as { access_token: string; endpoint: string };
      setTokens(b.endpoint, b.access_token); // refresh stays in the cookie
      return true;
    } catch {
      return false;
    } finally {
      refreshing = null;
    }
  })();
  return refreshing;
}
