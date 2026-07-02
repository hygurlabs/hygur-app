import { renderToString } from "react-dom/server";
import App from "./App";
import type { Route } from "./lib/router";

// SSR entry for the static pre-render (build:ssg). It imports NO CSS or fonts —
// those stay client-only in main.tsx — so it produces just the markup (Tailwind
// class names), which the client build's linked stylesheet styles. The route is
// passed explicitly (default: the landing route), so prerender.mjs can also
// emit the crawlable whitepaper pages.
export function render(route?: Route): string {
  return renderToString(<App ssrRoute={route} />);
}

// Re-exported for prerender.mjs (bundled into the SSR build): per-language SEO
// metadata + hreflang alternates for the whitepaper pages.
export { ENGRAM_PAGES, ENGRAM_HREFLANG } from "./components/Engram";
