import { useEffect, useState } from "react";
import { native } from "./native";

const POLL_MS = 5 * 60 * 1000;
const BUNDLE_RE = /assets\/index-[\w-]+\.js/;

/** The hashed main-bundle filename currently running (from the loaded script tag). */
function loadedBundle(): string | null {
  const src =
    document.querySelector('script[src*="/assets/index-"]')?.getAttribute("src") ?? "";
  const m = src.match(BUNDLE_RE);
  return m ? m[0] : null;
}

/** True once the deployed web build differs from the one running — a cue to
 *  reload. Web-only: the desktop app ships updates via its own bundle (and a
 *  fresh index.html isn't reachable to poll). Polls the no-store index.html and
 *  compares its hashed bundle to ours. */
export function useUpdateAvailable(): boolean {
  const [stale, setStale] = useState(false);
  useEffect(() => {
    if (native.available) return; // desktop — updated via the app, not a reload
    const current = loadedBundle();
    if (!current) return;
    let cancelled = false;
    const id = window.setInterval(() => {
      void (async () => {
        try {
          const res = await fetch("/", { cache: "no-store" });
          if (!res.ok) return;
          const m = (await res.text()).match(BUNDLE_RE);
          if (!cancelled && m && m[0] !== current) {
            setStale(true);
            window.clearInterval(id); // stop polling once we know
          }
        } catch {
          /* offline / transient — retry next interval */
        }
      })();
    }, POLL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(id);
    };
  }, []);
  return stale;
}
