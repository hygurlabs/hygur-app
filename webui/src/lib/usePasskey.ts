import { useCallback, useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { apiKey, isRemote } from "./connection";
import {
  passkeyCount,
  passkeyRegisterBegin,
  passkeyRegisterFinish,
  passkeysSupported,
  type PasskeyChallenge,
} from "./passkey";

// Shared cache key so the global banner, the Settings banner and the Settings
// security section read the same count and clear together once one is added.
export const PASSKEY_COUNT_KEY = ["passkey-count"];

/** Passkeys registered for the signed-in cloud account. Cloud web shell only —
 *  self-host / local have no console passkeys, so the query is disabled there. */
export function usePasskeyCount() {
  return useQuery({
    queryKey: PASSKEY_COUNT_KEY,
    queryFn: passkeyCount,
    enabled: isRemote(),
    retry: false,
    staleTime: 60_000,
  });
}

/** Pre-arms a registration challenge so a single tap can run the WebAuthn ceremony:
 *  iOS requires create() to be the first thing in the user gesture (no await before
 *  it), so the options are fetched ahead of time and re-armed after each attempt
 *  (challenges are single-use). The fetch sets state only from the async resolution,
 *  never synchronously inside the effect. */
export function useAddPasskey() {
  const qc = useQueryClient();
  const [challenge, setChallenge] = useState<PasskeyChallenge | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [nonce, setNonce] = useState(0); // bump to re-arm a fresh challenge

  useEffect(() => {
    if (!isRemote() || !passkeysSupported()) return;
    let cancelled = false;
    void passkeyRegisterBegin(apiKey())
      .then((c) => {
        if (!cancelled) setChallenge(c);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [nonce]);

  const add = useCallback(async () => {
    if (!challenge) return;
    setBusy(true); // synchronous — keeps the user gesture intact for iOS
    setError(null);
    try {
      await passkeyRegisterFinish(apiKey(), challenge); // ceremony first, no prior await
      await qc.invalidateQueries({ queryKey: PASSKEY_COUNT_KEY });
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't add the passkey.");
    } finally {
      setBusy(false);
      setChallenge(null);
      setNonce((n) => n + 1);
    }
  }, [challenge, qc]);

  return { add, busy, error, ready: !!challenge };
}
