import { GITHUB_URL, EDITIONS } from "../lib/content";
import logo from "../assets/logo.jpg";

const RESOURCES = [
  { label: "GitHub", href: GITHUB_URL },
  { label: "Source code", href: GITHUB_URL },
  { label: "License (AGPL-3.0)", href: GITHUB_URL },
];

// Internal hash routes — handled client-side, no new tab.
const LEGAL = [
  { label: "Legal Notice", href: "#/legal" },
  { label: "Privacy Policy", href: "#/privacy" },
  { label: "Terms of Service", href: "#/terms" },
];

export function Footer() {
  return (
    <footer className="border-t border-hairline bg-surface/60">
      <div className="mx-auto max-w-6xl px-5 py-14 sm:px-8">
        <div className="grid gap-10 sm:grid-cols-2 lg:grid-cols-12">
          <div className="lg:col-span-4">
            <a href="#top" className="flex items-center gap-2.5" aria-label="Hygur — home">
              <img
                src={logo}
                alt=""
                width={28}
                height={28}
                className="h-7 w-7 rounded-lg shadow-[var(--shadow-soft)]"
              />
              <span className="font-display text-xl text-text">Hygur</span>
            </a>
            <p className="mt-4 max-w-xs text-pretty text-sm leading-relaxed text-muted">
              Your local digital twin. A private memory of your documents, mail
              and notes, powered by your own LLM.
            </p>
          </div>

          <nav className="lg:col-span-3" aria-label="Editions">
            <h2 className="text-xs font-semibold uppercase tracking-[0.14em] text-faint">
              Editions
            </h2>
            <ul className="mt-4 space-y-2.5">
              {EDITIONS.map((e) => {
                const href = e.href ?? GITHUB_URL;
                const external = href.startsWith("http");
                return (
                  <li key={e.id}>
                    <a
                      href={href}
                      target={external ? "_blank" : undefined}
                      rel={external ? "noreferrer" : undefined}
                      className="text-sm text-muted transition-colors hover:text-text"
                    >
                      {e.name}
                    </a>
                  </li>
                );
              })}
            </ul>
          </nav>

          <nav className="lg:col-span-2" aria-label="Resources">
            <h2 className="text-xs font-semibold uppercase tracking-[0.14em] text-faint">
              Resources
            </h2>
            <ul className="mt-4 space-y-2.5">
              {RESOURCES.map((r) => (
                <li key={r.label}>
                  <a
                    href={r.href}
                    target="_blank"
                    rel="noreferrer"
                    className="text-sm text-muted transition-colors hover:text-text"
                  >
                    {r.label}
                  </a>
                </li>
              ))}
            </ul>
          </nav>

          <nav className="lg:col-span-3" aria-label="Legal">
            <h2 className="text-xs font-semibold uppercase tracking-[0.14em] text-faint">
              Legal
            </h2>
            <ul className="mt-4 space-y-2.5">
              {LEGAL.map((l) => (
                <li key={l.label}>
                  <a
                    href={l.href}
                    className="text-sm text-muted transition-colors hover:text-text"
                  >
                    {l.label}
                  </a>
                </li>
              ))}
            </ul>
          </nav>
        </div>

        <div className="mt-12 flex flex-col gap-3 border-t border-hairline pt-6 text-sm text-faint sm:flex-row sm:items-center sm:justify-between">
          <p>© 2026 0x0800 SRL · Hygur is open source under AGPL-3.0.</p>
          <p className="flex items-center gap-2">
            <span className="core-dot inline-block h-1.5 w-1.5 rounded-full bg-[color:var(--core)]" />
            Built in the EU · No cloud, no account
          </p>
        </div>
      </div>
    </footer>
  );
}
