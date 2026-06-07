import { ArrowRight } from "lucide-react";
import { EDITIONS, GITHUB_URL, type Edition } from "../lib/content";
import { Badge, Eyebrow, Reveal, cx } from "./ui";

/** Asymmetric 7/5 · 5/7 pinwheel so the four editions never read as four equal
 *  cards. App + Server are the free foundation; Cloud carries the warm "core"
 *  highlight; Marketplace closes the ecosystem. */
const SPAN: Record<string, string> = {
  app: "lg:col-span-7",
  server: "lg:col-span-5",
  cloud: "lg:col-span-5",
  marketplace: "lg:col-span-7",
};

function badgeTone(edition: Edition, badge: string): "neutral" | "accent" | "core" {
  if (edition.featured) return "core";
  if (/agpl|open source/i.test(badge)) return "accent";
  return "neutral";
}

function EditionCard({ edition, delay }: { edition: Edition; delay: number }) {
  const Icon = edition.icon;
  const featured = edition.featured;
  return (
    <Reveal as="article" delay={delay} className={cx("group/card", SPAN[edition.id])}>
      <a
        href={edition.href ?? GITHUB_URL}
        target={(edition.href ?? GITHUB_URL).startsWith("http") ? "_blank" : undefined}
        rel="noreferrer"
        className={cx(
          "flex h-full flex-col rounded-2xl border p-7 transition-[transform,border-color,box-shadow] duration-300 sm:p-8",
          "hover:-translate-y-1 hover:shadow-[var(--shadow-lift)]",
          featured
            ? "border-[color:var(--core)]/35 bg-[color:var(--core-glow)]/[0.07] hover:border-[color:var(--core)]/60"
            : "border-border bg-surface hover:border-accent/45",
        )}
      >
        <div className="flex items-center justify-between gap-4">
          <span
            className={cx(
              "grid h-12 w-12 place-items-center rounded-xl transition-colors",
              featured
                ? "bg-[color:var(--core-glow)]/20 text-[color:var(--core)]"
                : "bg-accent-weak text-accent",
            )}
          >
            <Icon size={22} strokeWidth={1.7} />
          </span>
          <span className="text-xs font-semibold uppercase tracking-[0.14em] text-faint">
            {edition.kicker}
          </span>
        </div>

        <h3 className="mt-6 text-xl font-semibold tracking-tight text-text">
          {edition.name}
        </h3>
        <p className="mt-0.5 text-sm font-medium text-accent">{edition.tagline}</p>

        <p className="mt-3 max-w-prose flex-1 text-pretty text-[0.95rem] leading-relaxed text-muted">
          {edition.body}
        </p>

        <div className="mt-6 flex flex-wrap items-center gap-1.5">
          {edition.badges.map((b) => (
            <Badge key={b} tone={badgeTone(edition, b)}>
              {b}
            </Badge>
          ))}
        </div>

        <span
          className={cx(
            "mt-6 inline-flex items-center gap-1.5 text-sm font-medium",
            featured ? "text-[color:var(--core)]" : "text-accent",
          )}
        >
          {edition.cta}
          <ArrowRight
            size={16}
            strokeWidth={2}
            className="transition-transform duration-200 group-hover/card:translate-x-1"
          />
        </span>
      </a>
    </Reveal>
  );
}

export function Editions() {
  return (
    <section id="editions" className="scroll-mt-20">
      <div className="mx-auto max-w-6xl px-5 py-20 sm:px-8 lg:py-28">
        <Reveal as="header" className="max-w-2xl">
          <Eyebrow>The Hygur line</Eyebrow>
          <h2 className="font-display mt-5 text-[clamp(2rem,4vw,3rem)] font-semibold leading-[1.04] text-balance text-text">
            One core. Four ways to run it.
          </h2>
          <p className="mt-4 text-pretty text-lg leading-relaxed text-muted">
            Start free on your own machine. Self-host the server, let us host it
            for you, or extend it with connectors — the data and the model stay
            under your control the whole way.
          </p>
        </Reveal>

        <div className="mt-12 grid gap-5 lg:grid-cols-12">
          {EDITIONS.map((e, i) => (
            <EditionCard key={e.id} edition={e} delay={i * 80} />
          ))}
        </div>
      </div>
    </section>
  );
}
