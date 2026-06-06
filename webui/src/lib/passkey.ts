// Passkey (WebAuthn) ceremonies against the Hygur Cloud control plane. The web
// shell (cloud.hygur.ai) calls these cross-origin to the console (console.hygur.ai,
// CORS-allowed there); on success the tenant token bundle is persisted and the app
// boots against the instance. @simplewebauthn/browser handles the base64url ↔
// ArrayBuffer glue + navigator.credentials.
import { startAuthentication, startRegistration } from "@simplewebauthn/browser";
import { CONSOLE_URL, setTokens } from "./connection";

interface TokenBundle {
  access_token: string;
  refresh_token: string;
  endpoint: string;
  tenant_id: string;
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

/** Log in to a cloud instance by its slug, authenticated by a passkey. Persists
 *  the resulting token bundle so the app boots against the tenant. */
export async function passkeyLogin(instance: string): Promise<void> {
  const begin = await consolePost("/passkey/login/begin", {
    instance: instance.trim().toLowerCase(),
  });
  if (!begin.ok) throw new Error("Unknown instance, or no passkey is registered for it.");
  const opts = (await begin.json()) as { publicKey: unknown; session_id: string };
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
