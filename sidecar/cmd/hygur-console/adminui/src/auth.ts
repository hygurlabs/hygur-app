import { startAuthentication, startRegistration } from "@simplewebauthn/browser";

// Operator session: a short-lived access token kept in memory (mirrored to
// sessionStorage so a reload survives) + an HttpOnly refresh cookie set by the
// console, so the 15-min token renews silently for the session. The operator
// account is pinned to the "operator" instance (its tenant_id sentinel).
const KEY = "hygur.admin.token";
const OPERATOR_INSTANCE = "operator";

export function getToken(): string {
  try {
    return sessionStorage.getItem(KEY) || "";
  } catch {
    return "";
  }
}
function setToken(t: string): string {
  try {
    sessionStorage.setItem(KEY, t);
  } catch {
    /* ignore */
  }
  return t;
}
function clearToken(): void {
  try {
    sessionStorage.removeItem(KEY);
  } catch {
    /* ignore */
  }
}

async function post(path: string, body?: unknown, token?: string): Promise<Response> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (token) headers.Authorization = `Bearer ${token}`;
  return fetch(path, {
    method: "POST",
    credentials: "include", // store/send the HttpOnly refresh cookie
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

interface Challenge {
  publicKey: unknown;
  session_id: string;
}

/** Renew the access token from the refresh cookie (returning operator). */
export async function refresh(): Promise<string> {
  const r = await post("/token/refresh", {});
  if (!r.ok) throw new Error("refresh failed");
  return setToken(((await r.json()) as { access_token: string }).access_token);
}

/** Sign in with a registered passkey. */
export async function passkeyLogin(): Promise<string> {
  const begin = await post("/passkey/login/begin", { instance: OPERATOR_INSTANCE });
  if (!begin.ok) throw new Error("No passkey is registered yet — use your one-time code below first.");
  const ch = (await begin.json()) as Challenge;
  const assertion = await startAuthentication({ optionsJSON: ch.publicKey as never });
  const fin = await post(`/passkey/login/finish?s=${encodeURIComponent(ch.session_id)}`, assertion);
  if (!fin.ok) throw new Error("Passkey authentication failed.");
  return setToken(((await fin.json()) as { access_token: string }).access_token);
}

/** First run: redeem the one-time code (the temporary password), then register a
 *  passkey on this device. Afterwards passkeyLogin() is all that's needed. */
export async function enrollAndRegister(code: string): Promise<string> {
  const er = await post("/enroll", { code: code.trim() });
  if (!er.ok) throw new Error("Invalid or expired code.");
  const tok = ((await er.json()) as { access_token: string }).access_token;
  const begin = await post("/passkey/register/begin", undefined, tok);
  if (!begin.ok) throw new Error("Could not start passkey registration.");
  const ch = (await begin.json()) as Challenge;
  const attestation = await startRegistration({ optionsJSON: ch.publicKey as never });
  const fin = await post(`/passkey/register/finish?s=${encodeURIComponent(ch.session_id)}`, attestation, tok);
  if (!fin.ok) throw new Error("Passkey registration failed.");
  return setToken(tok);
}

export function signOut(): void {
  clearToken();
  void post("/token/logout").catch(() => {});
}
