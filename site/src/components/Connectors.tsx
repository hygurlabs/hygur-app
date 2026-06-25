import { ArrowLeft, ArrowUpRight } from "lucide-react";
import { CONNECTORS, RELEASES_URL, type Connector } from "../lib/content";
import { Badge, Button, Eyebrow, Reveal, cx } from "./ui";
import { Footer } from "./Footer";
import logo from "../assets/logo.jpg";

function ConnectorCard({ c, delay }: { c: Connector; delay: number }) {
  const Icon = c.icon;
  return (
    <Reveal as="article" delay={delay}>
      <div
        className={cx(
          "flex h-full flex-col rounded-2xl border border-border bg-surface p-6",
          c.soon && "opacity-90",
        )}
      >
        <div className="flex items-center justify-between gap-3">
          <span className="grid h-11 w-11 place-items-center rounded-xl bg-accent-weak text-accent">
            <Icon size={20} strokeWidth={1.7} />
          </span>
          {c.soon ? (
            <Badge tone="accent">Soon</Badge>
          ) : (
            <span className="text-xs font-semibold uppercase tracking-[0.12em] text-faint">
              {c.category}
            </span>
          )}
        </div>
        <h3 className="mt-5 text-lg font-semibold tracking-tight text-text">{c.name}</h3>
        <p className="mt-2 text-pretty text-sm leading-relaxed text-muted">{c.body}</p>
      </div>
    </Reveal>
  );
}

/** Marketplace connectors page (#/connectors). Mirrors the legal-page chrome —
 *  slim header, readable column, shared footer — with a card grid for the
 *  catalogue and a "Coming soon" group for connectors still in the works. */
export function Connectors() {
  const live = CONNECTORS.filter((c) => !c.soon);
  const soon = CONNECTORS.filter((c) => c.soon);
  return (
    <>
      <header className="sticky top-0 z-50 border-b border-hairline bg-bg/85 backdrop-blur-md">
        <div className="mx-auto flex h-16 max-w-5xl items-center justify-between px-5 sm:px-8">
          <a href="#top" className="flex items-center gap-2.5" aria-label="Hygur — home">
            <img
              src={logo}
              alt=""
              width={30}
              height={30}
              className="h-[30px] w-[30px] rounded-[9px] shadow-[var(--shadow-soft)]"
            />
            <span className="font-display text-[1.35rem] leading-none text-text">Hygur</span>
          </a>
          <Button href={RELEASES_URL} target="_blank" rel="noreferrer" variant="ghost">
            Get the app
            <ArrowUpRight size={16} strokeWidth={2} />
          </Button>
        </div>
      </header>

      <main className="mx-auto max-w-5xl px-5 py-16 sm:px-8 lg:py-20">
        <a
          href="#top"
          className="inline-flex items-center gap-1.5 text-sm text-muted transition-colors hover:text-text"
        >
          <ArrowLeft size={15} strokeWidth={2} />
          Back to home
        </a>

        <div className="mt-7">
          <Eyebrow>Hygur Marketplace</Eyebrow>
        </div>
        <h1 className="font-display mt-5 text-[clamp(2.2rem,5vw,3rem)] font-semibold leading-[1.02] text-balance text-text">
          Connectors
        </h1>
        <p className="mt-4 max-w-2xl text-pretty text-lg leading-relaxed text-muted">
          Bring your world into Hygur: mail, files and calendars, indexed into your
          private memory. Connectors run on your machine or your own instance; your
          data stays yours.
        </p>

        <h2 className="mt-12 text-sm font-semibold uppercase tracking-[0.12em] text-faint">
          Available now
        </h2>
        <div className="mt-5 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {live.map((c, i) => (
            <ConnectorCard key={c.name} c={c} delay={i * 60} />
          ))}
        </div>

        <h2 className="mt-16 text-sm font-semibold uppercase tracking-[0.12em] text-faint">
          Coming soon
        </h2>
        <div className="mt-5 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {soon.map((c, i) => (
            <ConnectorCard key={c.name} c={c} delay={i * 60} />
          ))}
        </div>
      </main>

      <Footer />
    </>
  );
}
