import { DAILY } from "../lib/content";
import { Reveal } from "./ui";

/** The daily payoff: Hygur does the remembering and chasing so the user spends
 *  their time deciding, not digging. Outcome-first, same plain voice. */
export function Everyday() {
  return (
    <section className="border-b border-hairline">
      <div className="mx-auto max-w-6xl px-5 py-16 sm:px-8 lg:py-20">
        <Reveal>
          <p className="max-w-2xl text-pretty text-xl leading-snug text-text sm:text-2xl">
            Hygur does the remembering, so you move faster.{" "}
            <span className="text-muted">
              It reads your own world, ranks what matters, and brings it to you —
              before you go looking.
            </span>
          </p>
        </Reveal>

        <ul className="mt-12 grid gap-px overflow-hidden rounded-2xl border border-hairline bg-hairline sm:grid-cols-2 lg:grid-cols-4">
          {DAILY.map((p, i) => {
            const Icon = p.icon;
            return (
              <Reveal as="li" key={p.title} delay={i * 90} className="bg-bg">
                <div className="h-full px-6 py-8 sm:px-7 sm:py-9">
                  <span className="grid h-11 w-11 place-items-center rounded-xl bg-accent-weak text-accent">
                    <Icon size={20} strokeWidth={1.75} />
                  </span>
                  <h3 className="mt-5 text-lg font-semibold tracking-tight text-text">
                    {p.title}
                  </h3>
                  <p className="mt-2 text-pretty text-[0.95rem] leading-relaxed text-muted">
                    {p.body}
                  </p>
                </div>
              </Reveal>
            );
          })}
        </ul>
      </div>
    </section>
  );
}
