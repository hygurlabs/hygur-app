import { useEffect, useRef } from "react";
import { ArrowRight, Download } from "lucide-react";
import { RELEASES_URL, RUNTIMES } from "../lib/content";
import { Button, Eyebrow } from "./ui";
import { AskTypewriter } from "./AskTypewriter";
import { usePrefersReducedMotion } from "../lib/useMotion";
import appShot from "../assets/app-interface.png";
import welcomeShot from "../assets/welcome.png";

export function Hero() {
  const reduced = usePrefersReducedMotion();
  const backRef = useRef<HTMLImageElement>(null);
  const frontRef = useRef<HTMLDivElement>(null);

  // Gentle scroll parallax on the layered plates — transform only, rAF-throttled.
  useEffect(() => {
    if (reduced) return;
    let raf = 0;
    const apply = () => {
      raf = 0;
      const y = window.scrollY;
      if (backRef.current) backRef.current.style.transform = `translate3d(0, ${y * -0.035}px, 0)`;
      if (frontRef.current) frontRef.current.style.transform = `translate3d(0, ${y * 0.05}px, 0)`;
    };
    const onScroll = () => {
      if (!raf) raf = requestAnimationFrame(apply);
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      window.removeEventListener("scroll", onScroll);
      if (raf) cancelAnimationFrame(raf);
    };
  }, [reduced]);

  return (
    <section id="top" className="relative scroll-mt-20">
      <a id="overview" className="absolute -top-20" aria-hidden />
      <div className="mx-auto grid max-w-6xl items-center gap-14 px-5 pb-20 pt-14 sm:px-8 lg:grid-cols-12 lg:gap-10 lg:pb-28 lg:pt-20">
        {/* Copy side */}
        <div className="reveal is-in lg:col-span-6 xl:col-span-6">
          <Eyebrow>Local-first · No cloud · No account</Eyebrow>

          <h1 className="font-display mt-6 text-[clamp(2.7rem,6.4vw,4.6rem)] font-semibold leading-[0.98] text-balance text-text">
            Your local
            <br />
            digital{" "}
            <em className="italic font-medium text-accent">twin.</em>
          </h1>

          <p className="mt-6 max-w-[34rem] text-pretty text-lg leading-relaxed text-muted">
            <span className="font-medium text-text">It remembers everything, so you don't have to.</span>{" "}
            A private memory of your documents, mail and notes. It runs on your
            machine, powered by your own LLM.
          </p>

          <div className="mt-7 max-w-[27rem]">
            <AskTypewriter />
            <div className="mt-3 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-faint">
              <span className="font-medium uppercase tracking-wider">Bring your runtime</span>
              {RUNTIMES.map((r, i) => (
                <span key={r} className="flex items-center gap-x-2">
                  {i > 0 && <span aria-hidden className="text-faint">·</span>}
                  <span className="font-mono text-[0.72rem] text-muted">{r}</span>
                </span>
              ))}
            </div>
          </div>

          <div className="mt-8 flex flex-wrap items-center gap-3">
            <Button href={RELEASES_URL} target="_blank" rel="noreferrer" size="lg">
              <Download size={18} strokeWidth={2} />
              Download the app
            </Button>
            <Button href="#how" size="lg" variant="ghost">
              See how it works
              <ArrowRight size={17} strokeWidth={2} className="transition-transform duration-200 group-hover/btn:translate-x-0.5" />
            </Button>
          </div>

          <p className="mt-6 flex items-center gap-2 text-sm text-faint">
            <span className="core-dot inline-block h-2 w-2 rounded-full bg-[color:var(--core)]" />
            Free, forever, on your own hardware.
          </p>
        </div>

        {/* Visual side — two real product plates, layered. */}
        <div className="relative lg:col-span-6 xl:col-span-6">
          <div className="relative mx-auto max-w-[34rem] lg:mr-0 lg:ml-auto">
            <img
              ref={backRef}
              src={appShot}
              alt="The Hygur app open on the Ask screen, showing example questions and a private chat input."
              className="w-full rounded-2xl border border-border shadow-[var(--shadow-lift)]"
              width={840}
              height={760}
              loading="eager"
            />
            <div
              ref={frontRef}
              className="absolute -bottom-10 -left-6 w-[46%] min-w-[150px] sm:-left-10"
            >
              <img
                src={welcomeShot}
                alt="Hygur's welcome screen: a private memory of your documents, mail and notes, running on your own machine."
                className="w-full rounded-xl border border-border bg-surface shadow-[var(--shadow-lift)]"
                width={520}
                height={540}
                loading="lazy"
              />
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
