// Pre-render the landing page into dist/index.html so hygur.ai ships real HTML
// (crawlable + fetchable), not an empty JS shell. The client still boots via
// createRoot, which replaces #root on mount — so no hydration-matching
// constraints. Run after `vite build` + `vite build --ssr` (see build:ssg).
import { readFileSync, writeFileSync, rmSync } from "node:fs";
import { render } from "./.ssr-dist/entry-server.js";

const indexPath = "dist/index.html";
const marker = '<div id="root"></div>';

let index = readFileSync(indexPath, "utf8");
if (!index.includes(marker)) {
  throw new Error(`prerender: '${marker}' not found in ${indexPath}`);
}
const appHtml = render();
index = index.replace(marker, `<div id="root">${appHtml}</div>`);
writeFileSync(indexPath, index);
rmSync(".ssr-dist", { recursive: true, force: true });
console.log(`prerender: injected ${appHtml.length} bytes of HTML into ${indexPath}`);
