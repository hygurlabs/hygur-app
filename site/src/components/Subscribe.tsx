import { useEffect } from "react";
import { ArrowLeft, ArrowUpRight, ArrowRight, Check, ShieldCheck } from "lucide-react";
import { CLOUD_OPEN, CLOUD_SUBSCRIBE_URL, RELEASES_URL } from "../lib/content";
import { Badge, Button, Eyebrow } from "./ui";
import { Footer } from "./Footer";
import logo from "../assets/logo.jpg";

const INCLUDED = [
  "Your own private instance, on EU servers",
  "Your data encrypted and isolated, never used for training",
  "The full psyche: a daily brief, decisions, contradictions and chronicle",
  "Connect your mail, files and calendar",
  "Use it on web, desktop and your phone (installable, no app store)",
  "Cancel anytime. Export or delete your data whenever you want",
];

/** Pre-checkout recap (#/subscribe): the offer, what's included, the legal links
 *  and a single CTA to Stripe. Sits between the marketing site and Stripe Checkout
 *  so the price and terms are clear before payment. Gated by CLOUD_OPEN. */
export function Subscribe() {
  useEffect(() => {
    document.title = "Subscribe — Hygur Cloud";
    return () => {
      document.title = "Hygur — Your local digital twin";
    };
  }, []);

  return (
    <>
      <header className="sticky top-0 z-50 border-b border-hairline bg-bg/85 backdrop-blur-md">
        <div className="mx-auto flex h-16 max-w-3xl items-center justify-between px-5 sm:px-8">
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

      <main className="mx-auto max-w-3xl px-5 py-16 sm:px-8 lg:py-20">
        <a
          href="#top"
          className="inline-flex items-center gap-1.5 text-sm text-muted transition-colors hover:text-text"
        >
          <ArrowLeft size={15} strokeWidth={2} />
          Back to home
        </a>

        <div className="mt-7">
          <Eyebrow>Hygur Cloud</Eyebrow>
        </div>
        <h1 className="font-display mt-5 text-[clamp(2.2rem,5vw,3rem)] font-semibold leading-[1.02] text-balance text-text">
          Hygur Personal
        </h1>

        {/* Price */}
        <div className="mt-7 rounded-2xl border border-[color:var(--core)]/35 bg-[color:var(--core-glow)]/[0.07] p-6 sm:p-8">
          <div className="flex flex-wrap items-end gap-x-3 gap-y-1">
            <span className="font-display text-[clamp(2.4rem,6vw,3.2rem)] font-semibold leading-none text-text">
              29.90 €
            </span>
            <span className="pb-1 text-lg text-muted">/ month</span>
          </div>
          <p className="mt-3 text-pretty text-[0.95rem] leading-relaxed text-muted">
            Launch price, locked for life for the first 2,000 subscribers. Then 35 €/month.
            VAT included. Billed monthly, cancel anytime.
          </p>
        </div>

        {/* What's included */}
        <ul className="mt-8 grid gap-3 sm:grid-cols-2">
          {INCLUDED.map((item) => (
            <li key={item} className="flex items-start gap-2.5">
              <span className="mt-0.5 grid size-5 shrink-0 place-items-center rounded-full bg-accent-weak text-accent">
                <Check size={13} strokeWidth={2.4} />
              </span>
              <span className="text-pretty text-[0.95rem] leading-relaxed text-muted">{item}</span>
            </li>
          ))}
        </ul>

        {/* CTA */}
        <div className="mt-9">
          {CLOUD_OPEN ? (
            <a
              href={CLOUD_SUBSCRIBE_URL}
              target="_blank"
              rel="noreferrer"
              className="group/cta inline-flex h-13 items-center gap-2 rounded-full bg-accent px-8 text-base font-medium text-bg shadow-[var(--shadow-soft)] transition-[transform,box-shadow] duration-200 hover:-translate-y-0.5 hover:shadow-[var(--shadow-lift)] active:translate-y-px"
            >
              Continue to payment
              <ArrowRight
                size={18}
                strokeWidth={2}
                className="transition-transform duration-200 group-hover/cta:translate-x-1"
              />
            </a>
          ) : (
            <span className="inline-flex h-13 items-center gap-2 rounded-full border border-border bg-surface px-8 text-base font-medium text-muted">
              <Badge tone="accent">Soon</Badge>
              Subscriptions open at launch
            </span>
          )}
          <p className="mt-3 flex items-center gap-1.5 text-sm text-faint">
            <ShieldCheck size={15} strokeWidth={1.8} className="text-accent" />
            Secure payment via Stripe. We never see your card details.
          </p>
        </div>

        {/* Legal */}
        <p className="mt-10 border-t border-hairline pt-6 text-pretty text-[0.8rem] leading-relaxed text-faint">
          By subscribing you accept our{" "}
          <a className="text-muted underline decoration-border hover:text-text" href="#/terms">
            Terms of Service
          </a>{" "}
          and{" "}
          <a className="text-muted underline decoration-border hover:text-text" href="#/privacy">
            Privacy Policy
          </a>
          . Hygur Cloud is operated by 0x0800 SRL · VAT BE 1021.845.609. Prices include VAT.
        </p>
      </main>

      <Footer />
    </>
  );
}
