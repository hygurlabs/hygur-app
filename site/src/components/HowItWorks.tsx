import { MonitorSmartphone, Cpu, Database } from "lucide-react";
import { Eyebrow } from "./ui";

/** Plain "where it lives" explainer: an app on your machine that talks to a
 *  local LLM you choose, with your data indexed on-device. No architecture
 *  under-the-hood — just the three things that stay yours. */
const POINTS = [
  {
    icon: MonitorSmartphone,
    label: "The app",
    note: "Runs on your Mac, Windows or phone. Open it and it's ready; there's nothing to set up in the cloud.",
  },
  {
    icon: Cpu,
    label: "Your model",
    note: "Connects to a local LLM you choose: LM Studio, Ollama, vLLM, llama.cpp, or any OpenAI-compatible endpoint.",
  },
  {
    icon: Database,
    label: "Your data",
    note: "Your mail, files and notes are indexed on your machine and answered by your model. Nothing leaves your side.",
  },
];

export function HowItWorks() {
  return (
    <section id="how" className="scroll-mt-20 border-t border-hairline bg-sunk/50">
      <div className="mx-auto max-w-6xl px-5 py-20 sm:px-8 lg:py-28">
        <header className="max-w-2xl">
          <Eyebrow>How it works</Eyebrow>
          <h2 className="font-display mt-5 text-[clamp(2rem,4vw,3rem)] font-semibold leading-[1.04] text-balance text-text">
            It lives on your machine.
          </h2>
          <p className="mt-4 text-pretty text-lg leading-relaxed text-muted">
            Download the app, point it at a local LLM you choose, and connect your
            mail, files and calendar. Everything is indexed and answered right
            here, on your own hardware. No account, no cloud.
          </p>
        </header>

        <div className="mt-12 grid gap-5 sm:grid-cols-3">
          {POINTS.map((p) => {
            const Icon = p.icon;
            return (
              <div
                key={p.label}
                className="flex flex-col rounded-2xl border border-border bg-surface px-6 py-7 shadow-[var(--shadow-soft)]"
              >
                <span className="grid h-12 w-12 place-items-center rounded-xl bg-accent-weak text-accent">
                  <Icon size={22} strokeWidth={1.7} />
                </span>
                <h3 className="mt-4 text-base font-semibold tracking-tight text-text">
                  {p.label}
                </h3>
                <p className="mt-2 text-pretty text-sm leading-relaxed text-muted">
                  {p.note}
                </p>
              </div>
            );
          })}
        </div>
      </div>
    </section>
  );
}
