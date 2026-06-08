// Passkey (WebAuthn) ceremonies against the Hygur Cloud control plane. The web
// shell (cloud.hygur.ai) calls these cross-origin to the console (console.hygur.ai,
// CORS-allowed there); on success the tenant token bundle is persisted and the app
// boots against the instance. @simplewebauthn/browser handles the base64url ↔
// ArrayBuffer glue + navigator.credentials.
//
// iOS focus rule: every iOS browser (incl. Chrome) is WebKit, and WebKit throws
// "The document is not focused" when navigator.credentials.get()/create() runs
// after an intervening await — the options fetch breaks the user-activation/focus
// chain that the call requires. So each ceremony is split into begin() (does the
// fetch) and finish() (does the WebAuthn call): the caller runs begin() on the
// first tap, then finish() on a fresh tap with NOTHING awaited before the
// startAuthentication/startRegistration call inside it.
import { startAuthentication, startRegistration } from "@simplewebauthn/browser";
import { apiKey, CONSOLE_URL, setTokens } from "./connection";

export interface TokenBundle {
  access_token: string;
  refresh_token: string;
  endpoint: string;
  tenant_id: string;
}

/** Server-issued WebAuthn options + the session that ties begin → finish. */
export interface PasskeyChallenge {
  publicKey: unknown;
  session_id: string;
}

/** True when this browser supports WebAuthn at all. */
export function passkeysSupported(): boolean {
  return typeof window !== "undefined" && typeof window.PublicKeyCredential !== "undefined";
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

// --- Login (instance slug + passkey) -------------------------------------

/** Step 1: fetch the assertion options for an instance. Safe to await — no
 *  WebAuthn call happens here. */
export async function passkeyLoginBegin(instance: string): Promise<PasskeyChallenge> {
  const begin = await consolePost("/passkey/login/begin", {
    instance: instance.trim().toLowerCase(),
  });
  if (!begin.ok) throw new Error("Unknown instance, or no passkey is registered for it.");
  return (await begin.json()) as PasskeyChallenge;
}

/** Step 2: run the WebAuthn ceremony and persist the tenant token bundle. MUST be
 *  called as the first thing in a user gesture — do not await anything before it
 *  (see the iOS focus rule above). */
export async function passkeyLoginFinish(challenge: PasskeyChallenge): Promise<void> {
  const assertion = await startAuthentication({ optionsJSON: challenge.publicKey as never });
  const finish = await fetch(
    `${CONSOLE_URL}/passkey/login/finish?s=${encodeURIComponent(challenge.session_id)}`,
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

// --- Registration (add a passkey to the just-enrolled device) -------------

/** Step 1: fetch the attestation options (authorized by the device token). Safe
 *  to await — no WebAuthn call happens here. */
export async function passkeyRegisterBegin(accessToken: string): Promise<PasskeyChallenge> {
  const begin = await consolePost("/passkey/register/begin", undefined, accessToken);
  if (!begin.ok) throw new Error("Could not start passkey registration.");
  return (await begin.json()) as PasskeyChallenge;
}

/** Step 2: run the WebAuthn creation ceremony. MUST be the first thing in a user
 *  gesture — do not await anything before it (see the iOS focus rule above). */
export async function passkeyRegisterFinish(accessToken: string, challenge: PasskeyChallenge): Promise<void> {
  const attestation = await startRegistration({ optionsJSON: challenge.publicKey as never });
  const finish = await fetch(
    `${CONSOLE_URL}/passkey/register/finish?s=${encodeURIComponent(challenge.session_id)}`,
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
