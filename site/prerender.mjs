// Pre-render the static pages into dist/ so hygur.ai ships real HTML (crawlable
// + fetchable), not an empty JS shell. Emits:
//   dist/index.html              — the landing page (home route)
//   dist/engram-ai/index.html    — the Engram AI whitepaper, EN (default/canonical)
//   dist/fr/engram-ai/index.html — the whitepaper, FR
// The client boots via createRoot and replaces #root on mount, so there are no
// hydration-matching constraints. Run after `vite build` + `vite build --ssr`
// (see build:ssg).
import { readFileSync, writeFileSync, mkdirSync, rmSync } from "node:fs";
import { dirname } from "node:path";
import { render, ENGRAM_PAGES, ENGRAM_HREFLANG } from "./.ssr-dist/entry-server.js";

const indexPath = "dist/index.html";
const marker = '<div id="root"></div>';

// The raw Vite build output (empty #root) — the template for every page.
const template = readFileSync(indexPath, "utf8");
if (!template.includes(marker)) {
  throw new Error(`prerender: '${marker}' not found in ${indexPath}`);
}

function inject(html, appHtml) {
  return html.replace(marker, `<div id="root">${appHtml}</div>`);
}

// ── Home ──────────────────────────────────────────────────────────────────
const homeHtml = render();
writeFileSync(indexPath, inject(template, homeHtml));
console.log(`prerender: injected ${homeHtml.length} bytes into ${indexPath}`);

// ── Whitepaper pages (path-based, crawlable) ────────────────────────────────
const escAttr = (s) =>
  String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");

/** Replace a self-closing <meta> tag (matched by an attribute, across
 *  newlines) with a fresh single-line one. Throws if the tag is missing so a
 *  template change can never silently ship a page with stale SEO. */
function setMeta(html, matchAttr, matchValue, content) {
  const re = new RegExp(`<meta[^>]*${matchAttr}="${matchValue}"[^>]*>`);
  if (!re.test(html)) throw new Error(`prerender: <meta ${matchAttr}="${matchValue}"> not found`);
  return html.replace(re, `<meta ${matchAttr}="${matchValue}" content="${escAttr(content)}" />`);
}

const hreflangLinks = [
  `<link rel="alternate" hreflang="en" href="${ENGRAM_HREFLANG.en}" />`,
  `<link rel="alternate" hreflang="fr" href="${ENGRAM_HREFLANG.fr}" />`,
  `<link rel="alternate" hreflang="x-default" href="${ENGRAM_HREFLANG.x_default}" />`,
];

for (const page of ENGRAM_PAGES) {
  let html = template;

  // Served from a sub-path, so the head's relative asset refs must be
  // root-absolute (the home page keeps its own `./` refs untouched).
  html = html.replace(/(src|href)="\.\/assets\//g, '$1="/assets/');
  html = html.replace(/href="\.\/favicon\.jpg"/g, 'href="/favicon.jpg"');

  // Per-language <html lang>.
  html = html.replace('<html lang="en">', `<html lang="${page.lang}">`);

  // Title + description + OpenGraph + Twitter.
  html = html.replace(/<title>[\s\S]*?<\/title>/, `<title>${escAttr(page.title)}</title>`);
  html = setMeta(html, "name", "description", page.description);
  html = setMeta(html, "property", "og:title", page.title);
  html = setMeta(html, "property", "og:description", page.description);
  html = setMeta(html, "property", "og:url", page.url);
  html = setMeta(html, "property", "og:type", "article");
  html = setMeta(html, "name", "twitter:title", page.title);
  html = setMeta(html, "name", "twitter:description", page.description);

  // Drop the product (SoftwareApplication) JSON-LD; it describes the app, not
  // this document.
  html = html.replace(/\s*<script type="application\/ld\+json">[\s\S]*?<\/script>/, "");

  // Canonical (self) + hreflang alternates (EN = x-default) + og:locale.
  const ogLocale = page.lang === "fr" ? "fr_FR" : "en_US";
  const headExtra =
    `    <link rel="canonical" href="${page.url}" />\n` +
    hreflangLinks.map((l) => `    ${l}`).join("\n") +
    `\n    <meta property="og:locale" content="${ogLocale}" />\n  `;
  html = html.replace("</head>", `${headExtra}</head>`);

  // Body: the pre-rendered whitepaper for this language.
  html = inject(html, render(page.route));

  const out = `dist/${page.dir}/index.html`;
  mkdirSync(dirname(out), { recursive: true });
  writeFileSync(out, html);
  console.log(`prerender: wrote ${out} (${html.length} bytes, lang=${page.lang})`);
}

rmSync(".ssr-dist", { recursive: true, force: true });
