import { useState } from "react";
import { MonitorSmartphone, Server, Database } from "lucide-react";
import { Eyebrow, cx } from "./ui";
import { useInView } from "../lib/useMotion";

type Mode = "local" | "remote";

const MODE_COPY: Record<Mode, { badge: string; note: string }> = {
  local: {
    badge: "Bundled & spawned",
    note: "The desktop app starts the server for you on loopback, secured with a local token. Zero config, fully offline.",
  },
  remote: {
    badge: "Self-hosted or Cloud",
    note: "Point any client at a server you run — over HTTPS with a device token (JWT), behind your own reverse proxy.",
  },
};

function Station({
  icon: Icon,
  label,
  sub,
  note,
  tone = "accent",
}: {
  icon: typeof Server;
  label: string;
  sub: string;
  note: string;
  tone?: "accent" | "core";
}) {
  return (
    <div className="flex flex-col items-center rounded-2xl border border-border bg-surface px-6 py-7 text-center shadow-[var(--shadow-soft)] md:flex-1">
      <span
        className={cx(
          "grid h-12 w-12 place-items-center rounded-xl",
          tone === "core"
            ? "bg-[color:var(--core-glow)]/20 text-[color:var(--core)]"
            : "bg-accent-weak text-accent",
        )}
      >
        <Icon size={22} strokeWidth={1.7} />
      </span>
      <h3 className="mt-4 text-base font-semibold tracking-tight text-text">{label}</h3>
      <p
        className={cx(
          "mt-1 text-xs font-semibold uppercase tracking-[0.12em]",
          tone === "core" ? "text-[color:var(--core)]" : "text-accent",
        )}
      >
        {sub}
      </p>
      <p className="mt-3 max-w-[15rem] text-pretty text-sm leading-relaxed text-muted">
        {note}
      </p>
    </div>
  );
}

/** Thin connector that grows from the previous station. Horizontal on md+,
 *  vertical on mobile — transform-scale only, so it stays cheap and respects
 *  reduced motion (CSS zeroes the transition there via `.reveal`-style rules). */
function Connector({ on }: { on: boolean }) {
  return (
    <div className="flex items-center justify-center py-1 md:w-16 md:py-0">
      <span
        className="h-7 w-px origin-top bg-accent/40 transition-transform duration-700 ease-out md:h-px md:w-full md:origin-left"
        style={{ transform: on ? "scale(1)" : "scale(0)" }}
      />
    </div>
  );
}

export function HowItWorks() {
  const [mode, setMode] = useState<Mode>("local");
  const { ref, inView } = useInView<HTMLDivElement>();

  return (
    <section id="how" className="scroll-mt-20 border-t border-hairline bg-sunk/50">
      <div className="mx-auto max-w-6xl px-5 py-20 sm:px-8 lg:py-28">
        <div className="grid gap-6 lg:grid-cols-12 lg:items-end">
          <header className="lg:col-span-7">
            <Eyebrow>How it works</Eyebrow>
            <h2 className="font-display mt-5 text-[clamp(2rem,4vw,3rem)] font-semibold leading-[1.04] text-balance text-text">
              One server. Thin clients. Your hardware.
            </h2>
            <p className="mt-4 max-w-xl text-pretty text-lg leading-relaxed text-muted">
              The server holds the data and the brain. Clients are just windows
              onto it. Run it locally for zero-config solo use, or point your
              clients at a server you host.
            </p>
          </header>

          {/* Mode toggle */}
          <div className="lg:col-span-5 lg:justify-self-end">
            <div
              role="group"
              aria-label="Server mode"
              className="inline-flex rounded-full border border-border bg-surface p-1 shadow-[var(--shadow-soft)]"
            >
              {(["local", "remote"] as Mode[]).map((m) => (
                <button
                  key={m}
                  type="button"
                  onClick={() => setMode(m)}
                  aria-pressed={mode === m}
                  className={cx(
                    "h-9 rounded-full px-5 text-sm font-medium capitalize transition-colors duration-200",
                    mode === m
                      ? "bg-accent text-bg shadow-[var(--shadow-soft)]"
                      : "text-muted hover:text-text",
                  )}
                >
                  {m}
                </button>
              ))}
            </div>
          </div>
        </div>

        {/* The scene */}
        <div
          ref={ref}
          className="mt-12 flex flex-col items-stretch md:flex-row md:items-center"
        >
          <Station
            icon={MonitorSmartphone}
            label="Clients"
            sub="Desktop · Mobile · Web"
            note="Thin by design — no data and no model live here, just a window onto the server."
          />
          <Connector on={inView} />
          <Station
            icon={Server}
            label="Hygur Server"
            sub={MODE_COPY[mode].badge}
            note={MODE_COPY[mode].note}
          />
          <Connector on={inView} />
          <Station
            icon={Database}
            label="Your data + your LLM"
            sub="Indexed locally"
            note="Documents, mail and notes vectorised on the server, answered by the model you choose."
            tone="core"
          />
        </div>
      </div>
    </section>
  );
}
