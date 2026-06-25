import { ArrowRight } from "lucide-react";
import { GITHUB_URL } from "../lib/content";
import { Reveal } from "./ui";

export function CtaBand() {
  return (
    <section className="mx-auto max-w-6xl px-5 py-20 sm:px-8 lg:py-24">
      <Reveal className="relative overflow-hidden rounded-3xl border border-border bg-surface px-7 py-14 shadow-[var(--shadow-soft)] sm:px-12 lg:px-16 lg:py-20">
        {/* Warm core glow, pinned to the corner — the only spotlight on the page. */}
        <span
          aria-hidden
          className="pointer-events-none absolute -right-24 -top-24 h-72 w-72 rounded-full opacity-70"
          style={{
            background:
              "radial-gradient(circle, rgba(244,184,94,0.22), rgba(244,184,94,0) 70%)",
          }}
        />
        <div className="relative max-w-2xl">
          <h2 className="font-display text-[clamp(2.1rem,4.4vw,3.3rem)] font-semibold leading-[1.02] text-balance text-text">
            Start local.{" "}
            <span className="text-accent">Scale when you want.</span>
          </h2>
          <p className="mt-5 max-w-xl text-pretty text-lg leading-relaxed text-muted">
            Free on your own machine today. When you want it hosted, move to a
            managed Hygur Cloud instance. Your data and your model come with you.
          </p>
          <p className="mt-4 max-w-xl text-pretty text-sm leading-relaxed text-faint">
            Hygur Cloud runs <strong className="font-medium text-muted">exclusively on EU
            servers</strong>, with GPU inference at an EU provider that
            <strong className="font-medium text-muted"> never trains on your data</strong>.
          </p>

          <a
            href={GITHUB_URL}
            target="_blank"
            rel="noreferrer"
            className="group/cta relative mt-8 inline-flex h-13 items-center gap-2 overflow-hidden rounded-full bg-accent px-8 text-base font-medium text-bg shadow-[var(--shadow-soft)] transition-[transform,box-shadow] duration-200 hover:-translate-y-0.5 hover:shadow-[var(--shadow-lift)] active:translate-y-px"
          >
            <span
              aria-hidden
              className="absolute inset-y-0 -left-1/2 w-1/2 -skew-x-12 bg-white/15 transition-transform duration-700 ease-out group-hover/cta:translate-x-[260%]"
            />
            <span className="relative">Get Hygur</span>
            <ArrowRight
              size={18}
              strokeWidth={2}
              className="relative transition-transform duration-200 group-hover/cta:translate-x-1"
            />
          </a>
        </div>
      </Reveal>
    </section>
  );
}
