import { useEffect, useState } from "react";

export type Route = "home" | "legal" | "privacy" | "terms";

/** Hash routing keeps the whole site a single static artifact: it deploys to
 *  plain nginx with no SPA-fallback rewrite, and deep links like
 *  `/#/legal` resolve on first load. Routes use a `#/` prefix so in-page
 *  anchors (`#editions`, `#how`) are left untouched. */
export function parseHash(): { route: Route; anchor?: string } {
  const h = window.location.hash.replace(/^#/, "");
  if (h === "/legal") return { route: "legal" };
  if (h === "/privacy") return { route: "privacy" };
  if (h === "/terms") return { route: "terms" };
  return { route: "home", anchor: h && !h.startsWith("/") ? h : undefined };
}

export function useRoute() {
  const [state, setState] = useState(parseHash);
  useEffect(() => {
    const onChange = () => setState(parseHash());
    window.addEventListener("hashchange", onChange);
    return () => window.removeEventListener("hashchange", onChange);
  }, []);
  return state;
}
