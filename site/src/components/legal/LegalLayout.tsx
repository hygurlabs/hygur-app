import type { ReactNode } from "react";
import { ArrowLeft, ArrowUpRight } from "lucide-react";
import { GITHUB_URL } from "../../lib/content";
import { Button } from "../ui";
import { Footer } from "../Footer";
import logo from "../../assets/logo.jpg";

/** Shared shell for the legal pages — a slim header (no in-page section nav,
 *  since those anchors only exist on the home route), a readable column, and
 *  the same footer as the rest of the site. */
export function LegalLayout({
  title,
  updated,
  intro,
  children,
}: {
  title: string;
  updated: string;
  intro?: ReactNode;
  children: ReactNode;
}) {
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
          <Button href={GITHUB_URL} target="_blank" rel="noreferrer" variant="ghost">
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

        <h1 className="font-display mt-7 text-[clamp(2.2rem,5vw,3rem)] font-semibold leading-[1.02] text-balance text-text">
          {title}
        </h1>
        <p className="mt-3 text-sm text-faint">Last updated: {updated}</p>

        {intro && (
          <div className="mt-6 max-w-2xl text-pretty text-lg leading-relaxed text-muted">
            {intro}
          </div>
        )}

        <div className="legal-prose mt-10">{children}</div>
      </main>

      <Footer />
    </>
  );
}
