// Passkey (WebAuthn) ceremonies against the Hygur Cloud control plane. The web
// shell (cloud.hygur.ai) calls these cross-origin to the console (console.hygur.ai,
// CORS-allowed there); on success the tenant token bundle is persisted and the app
// boots against the instance. @simplewebauthn/browser handles the base64url ↔
// ArrayBuffer glue + navigator.credentials.
import { startAuthentication, startRegistration } from "@simplewebauthn/browser";
import { apiKey, CONSOLE_URL, setTokens } from "./connection";

export interface TokenBundle {
  access_token: string;
  refresh_token: string;
  endpoint: string;
  tenant_id: string;
}

/** True when this browser supports WebAuthn at all. */
export function passkeysSupported(): boolean {
  return typeof window !== "undefined" && typeof window.PublicKeyCredential !== "undefined";
}

/** WebKit (Safari, and every iOS browser including Chrome — they're all WKWebView)
 *  checks `document.hasFocus()` inside navigator.credentials.get()/create() and
 *  throws "The document is not focused" when it's false. On mobile that happens
 *  right after the tap starting sign-in dismisses the soft keyboard, while the
 *  options fetch is in flight. Reclaim window focus and wait a beat for the
 *  webview to settle before the ceremony. No-op when already focused, so desktop
 *  and Android take the fast path unchanged. */
async function ensureDocumentFocused(): Promise<void> {
  if (typeof document === "undefined" || document.hasFocus()) return;
  // Dismissing the keyboard is what drops focus; blur the field, then reclaim it.
  (document.activeElement as HTMLElement | null)?.blur?.();
  for (let i = 0; i < 10; i++) {
    window.focus();
    if (document.hasFocus()) return;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
}

async function consolePost(path: string, body?: unknown, token?: string): Promise<Response> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (token) headers.Authorization = `Bearer ${token}`;
  return fetch(`${CONSOLE_URL}${path}`, {
    method: "POST",
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

/** Log in to a cloud instance by its slug, authenticated by a passkey. Persists
 *  the resulting token bundle so the app boots against the tenant. */
export async function passkeyLogin(instance: string): Promise<void> {
  const begin = await consolePost("/passkey/login/begin", {
    instance: instance.trim().toLowerCase(),
  });
  if (!begin.ok) throw new Error("Unknown instance, or no passkey is registered for it.");
  const opts = (await begin.json()) as { publicKey: unknown; session_id: string };
  await ensureDocumentFocused();
  const assertion = await startAuthentication({ optionsJSON: opts.publicKey as never });
  const finish = await fetch(
    `${CONSOLE_URL}/passkey/login/finish?s=${encodeURIComponent(opts.session_id)}`,
    { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(assertion) },
  );
  if (!finish.ok) throw new Error("Passkey authentication failed.");
  const b = (await finish.json()) as TokenBundle;
  setTokens(b.endpoint, b.access_token, b.refresh_token);
}

/** Redeem a one-time enrollment code → device token bundle (connects the app).
 *  Returns the access token so the caller can immediately register a passkey. */
export async function enrollWithCode(code: string): Promise<string> {
  const r = await consolePost("/enroll", { code: code.trim() });
  if (!r.ok) throw new Error("Invalid or expired enrollment code.");
  const b = (await r.json()) as TokenBundle;
  setTokens(b.endpoint, b.access_token, b.refresh_token);
  return b.access_token;
}

/** Register a passkey for the just-enrolled device (authorized by its access
 *  token). After this the instance can be reached by slug + passkey. */
export async function registerPasskey(accessToken: string): Promise<void> {
  const begin = await consolePost("/passkey/register/begin", undefined, accessToken);
  if (!begin.ok) throw new Error("Could not start passkey registration.");
  const opts = (await begin.json()) as { publicKey: unknown; session_id: string };
  await ensureDocumentFocused();
  const attestation = await startRegistration({ optionsJSON: opts.publicKey as never });
  const finish = await fetch(
    `${CONSOLE_URL}/passkey/register/finish?s=${encodeURIComponent(opts.session_id)}`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${accessToken}` },
      body: JSON.stringify(attestation),
    },
  );
  if (!finish.ok) throw new Error("Passkey registration failed.");
}

// --- Desktop handback ----------------------------------------------------
// The desktop webview (loopback origin) can't run WebAuthn, so passkey sign-in
// happens in the system browser (cloud.hygur.ai). Once the browser is logged in
// it stashes a short-lived bundle under a random `state`; the desktop is woken
// via the hygur:// deep link and claims it. The state is the only thing on the
// wire — the token bundle never travels through the deep-link URL.

/** Browser side: stash the current session as a one-time bundle keyed by `state`,
 *  authorized by the just-obtained access token. */
export async function desktopHandoff(state: string): Promise<void> {
  const token = apiKey();
  if (!token) throw new Error("Not signed in.");
  const r = await consolePost("/desktop/handoff", { state }, token);
  if (!r.ok) throw new Error("Could not prepare the desktop sign-in.");
}

/** Desktop side: redeem the one-time `state` for the token bundle. Returns it for
 *  the caller to apply — on the native app this goes into the desktop config
 *  (cloud engine mode), NOT the browser's localStorage. */
export async function desktopClaim(state: string): Promise<TokenBundle> {
  const r = await consolePost("/desktop/claim", { state });
  if (!r.ok) throw new Error("Desktop sign-in expired — try again.");
  return (await r.json()) as TokenBundle;
}
