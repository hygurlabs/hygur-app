// First-run onboarding completion flag. Persisted in two places so it survives
// both a WKWebView data-store reset (native UserDefaults via the bridge) and a
// plain-browser session (localStorage). markOnboardingComplete writes both; the
// check reads the cheap synchronous store first, then falls back to native.

import { native } from "./native";

const KEY = "onboarding.completed";

/** Resolves `value` but gives up after `ms`, yielding `fallback` instead — the
 *  bridge call should never wedge the first paint behind a hung promise. */
function withTimeout<T>(p: Promise<T>, ms: number, fallback: T): Promise<T> {
  return new Promise((resolve) => {
    let settled = false;
    const t = window.setTimeout(() => {
      if (!settled) {
        settled = true;
        resolve(fallback);
      }
    }, ms);
    void p.then((v) => {
      if (!settled) {
        settled = true;
        window.clearTimeout(t);
        resolve(v);
      }
    }).catch(() => {
      if (!settled) {
        settled = true;
        window.clearTimeout(t);
        resolve(fallback);
      }
    });
  });
}

/** True once the user has finished (or explicitly skipped through) onboarding. */
export async function isOnboardingComplete(): Promise<boolean> {
  try {
    if (localStorage.getItem(KEY) === "1") return true;
  } catch {
    /* localStorage may be unavailable in some embeddings — fall through */
  }
  if (native.available) {
    const v = await withTimeout(native.prefs.getBool(KEY), 1500, false);
    if (v) {
      try {
        localStorage.setItem(KEY, "1");
      } catch {
        /* ignore */
      }
    }
    return v;
  }
  return false;
}

/** Records that onboarding is done in both stores. */
export function markOnboardingComplete(): void {
  try {
    localStorage.setItem(KEY, "1");
  } catch {
    /* ignore */
  }
  if (native.available) {
    void native.prefs.setBool(KEY, true);
  }
}
