import { useEffect, useState } from "react";

/** True once an operation has run past `afterMs` WITHOUT making progress, so an
 *  indefinite spinner can shift into a "taking longer than usual" hint and the
 *  user can tell "still working" from "stuck/dead".
 *
 *  This is stall detection, not a wall-clock timer: pass a `resetKey` that
 *  changes on every progress event (a streamed token, a new source, a status
 *  change). Each change restarts the countdown, so a steadily-progressing task
 *  never trips, and the flag fires only after a genuine gap of silence. The flag
 *  clears whenever `active` goes false. */
export function useSlow(active: boolean, afterMs = 9000, resetKey?: unknown): boolean {
  const [slow, setSlow] = useState(false);

  // Clear the flag during render whenever the activity restarts or makes
  // progress — React's sanctioned "adjust state when a prop changes" pattern
  // (previous values held in state, no effect, no ref read).
  const [prev, setPrev] = useState<{ active: boolean; key: unknown }>({ active, key: resetKey });
  if (prev.active !== active || prev.key !== resetKey) {
    setPrev({ active, key: resetKey });
    if (slow) setSlow(false);
  }

  // Arm a fresh countdown after every progress event; only the timer sets the
  // flag (async, so it never cascades a render synchronously).
  useEffect(() => {
    if (!active) return;
    const id = window.setTimeout(() => setSlow(true), afterMs);
    return () => window.clearTimeout(id);
  }, [active, afterMs, resetKey]);

  return slow;
}
