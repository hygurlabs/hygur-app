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

/** Key sent as X-Hygur-Token. A configured remote key wins; otherwise the
 *  same-origin token injected by the local sidecar. */
export function apiKey(): string {
  return ls(API_KEY_KEY) || META_TOKEN;
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

/** True when the client can authenticate: local same-origin, or a remote key set. */
export function isConfigured(): boolean {
  return !isRemote() || !!ls(API_KEY_KEY);
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
export function setTokens(endpoint: string, accessToken: string, refreshToken?: string): void {
  setConnection(endpoint, accessToken);
  try {
    if (refreshToken) localStorage.setItem(REFRESH_KEY, refreshToken);
  } catch {
    /* ignore */
  }
}

/** Drops the connection AND the refresh token (full sign-out). */
export function clearTokens(): void {
  clearConnection();
  try {
    localStorage.removeItem(REFRESH_KEY);
  } catch {
    /* ignore */
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
    const rt = ls(REFRESH_KEY);
    if (!rt) return false;
    try {
      const r = await fetch(`${CONSOLE_URL}/token/refresh`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: rt }),
      });
      if (!r.ok) {
        clearTokens();
        return false;
      }
      const b = (await r.json()) as { access_token: string; refresh_token: string; endpoint: string };
      setTokens(b.endpoint, b.access_token, b.refresh_token);
      return true;
    } catch {
      return false;
    } finally {
      refreshing = null;
    }
  })();
  return refreshing;
}
