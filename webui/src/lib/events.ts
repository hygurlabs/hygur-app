import { fetchEventSource } from "@microsoft/fetch-event-source";
import { TOKEN } from "./api";

/** One event off the sidecar's `/events` SSE stream (sync/ingest/mail/…). */
export interface HygurEvent {
  type: string;
  source?: string;
  status?: string;
  message?: string;
  data?: Record<string, unknown>;
  created_at?: string;
}

/** Subscribes to the sidecar activity stream. Unlike the chat stream this is a
 *  long-lived subscription — onerror returns a retry delay so fetch-event-source
 *  reconnects with backoff instead of giving up. */
export function subscribeEvents(
  onEvent: (e: HygurEvent) => void,
  signal: AbortSignal,
): Promise<void> {
  return fetchEventSource("/events", {
    headers: { "X-Hygur-Token": TOKEN },
    signal,
    openWhenHidden: true,
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
    onerror() {
      // Keep the stream alive: reconnect after 5s rather than aborting.
      return 5000;
    },
  });
}
