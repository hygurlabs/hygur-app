import { useEffect, useState } from "react";

export type Route =
  | "home"
  | "legal"
  | "privacy"
  | "terms"
  | "connectors"
  | "subscribe"
  | "engram"
  | "engram-fr";

/** Resolve a route from a location. Hash routes keep the bulk of the site a
 *  single static artifact (`/#/legal` etc.), so they take precedence — that way
 *  the shared Nav/Footer links keep working from every page, including the
 *  path-based whitepaper. When no hash route matches, the pathname is consulted
 *  for the pre-rendered, crawlable whitepaper routes (`/engram-ai`,
 *  `/fr/engram-ai`), which ship as real static HTML for SEO. */
export function resolveLocation(
  pathname: string,
  hash: string,
): { route: Route; anchor?: string } {
  const h = hash.replace(/^#/, "");
  if (h === "/legal") return { route: "legal" };
  if (h === "/privacy") return { route: "privacy" };
  if (h === "/terms") return { route: "terms" };
  if (h === "/connectors") return { route: "connectors" };
  if (h === "/subscribe") return { route: "subscribe" };
  if (!h.startsWith("/")) {
    const p = pathname.replace(/\/+$/, "");
    if (p.endsWith("/fr/engram-ai")) return { route: "engram-fr" };
    if (p.endsWith("/engram-ai")) return { route: "engram" };
  }
  return { route: "home", anchor: h && !h.startsWith("/") ? h : undefined };
}

export function parseHash(): { route: Route; anchor?: string } {
  // SSR / pre-render (no window): the landing route. The pre-render passes the
  // real route explicitly (see entry-server), and the client re-reads the
  // location on mount, so deep links and whitepaper paths still resolve.
  if (typeof window === "undefined") return { route: "home" };
  return resolveLocation(window.location.pathname, window.location.hash);
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
