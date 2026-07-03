import { fetchEventSource } from "@microsoft/fetch-event-source";
import { apiBase, apiKey, refreshAccessToken } from "./connection";

/** One event off the sidecar's `/events` SSE stream (sync/ingest/mail/…). */
export interface HygurEvent {
  type: string;
  source?: string;
  status?: string;
  message?: string;
  data?: Record<string, unknown>;
  created_at?: string;
}

/** Sentinel thrown from onopen when a 401 was transparently refreshed: the
 *  stream must reconnect from scratch so the rotated access key is picked up
 *  (fetch-event-source reuses its static headers across its own retries). */
const REFRESH_RECONNECT = new Error("hygur:events-refresh-reconnect");

/** Subscribes to the sidecar activity stream. Long-lived: transient errors keep
 *  the stream alive with a 5s backoff, and an expired cloud access token is
 *  renewed via the refresh token then reconnected with the rotated key — instead
 *  of hammering `/events` every 5s with a dead token (the old 401 loop). A
 *  definitive auth failure signs out (via refreshAccessToken → SIGNED_OUT_EVENT,
 *  which routes to Connect and aborts this signal), so no manual stop is needed. */
export function subscribeEvents(
  onEvent: (e: HygurEvent) => void,
  signal: AbortSignal,
): Promise<void> {
  const connect = async (): Promise<void> => {
    try {
      await fetchEventSource(apiBase() + "/events", {
        // Rebuilt on every connect() call, so a post-refresh reconnect sends the
        // rotated key rather than the expired one.
        headers: { "X-Hygur-Token": apiKey() },
        signal,
        openWhenHidden: true,
        async onopen(response) {
          const ct = response.headers.get("content-type") || "";
          if (response.ok && ct.includes("text/event-stream")) return;
          // Expired access token → one transparent refresh, then reconnect with
          // the rotated key. If the refresh fails (transient or a real sign-out),
          // fall through to the backoff below; a real sign-out cleared the tokens
          // and emitted SIGNED_OUT_EVENT, so the signal will abort us shortly.
          if (response.status === 401 && (await refreshAccessToken())) {
            throw REFRESH_RECONNECT;
          }
          throw new Error(`events stream: HTTP ${response.status}`);
        },
        onmessage(msg) {
          if (!msg.data) return;
          try {
            const e = JSON.parse(msg.data) as HygurEvent;
            if (e.type === "connection") return; // hello frame
            onEvent(e);
          } catch {
            /* ignore malformed frames */
          }
        },
        onerror(err) {
          // A refreshed 401 must reconnect from scratch (rethrow → connect() below);
          // everything else is transient — reconnect after 5s with backoff.
          if (err === REFRESH_RECONNECT) throw err;
          return 5000;
        },
      });
    } catch (err) {
      if (err === REFRESH_RECONNECT && !signal.aborted) return connect();
      // Aborted (unmount / sign-out routing) or a fatal error — stop quietly.
    }
  };
  return connect();
}
