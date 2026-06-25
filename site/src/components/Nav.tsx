import { useEffect, useState } from "react";
import { ArrowUpRight } from "lucide-react";
import { RELEASES_URL } from "../lib/content";
import { Button } from "./ui";
import logo from "../assets/logo.jpg";

const LINKS = [
  { href: "#overview", label: "Overview" },
  { href: "#editions", label: "Editions" },
  { href: "#how", label: "How it works" },
];

export function Nav() {
  const [scrolled, setScrolled] = useState(false);

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 12);
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);

  return (
    <header
      className={
        "sticky top-0 z-50 transition-colors duration-300 " +
        (scrolled
          ? "border-b border-hairline bg-bg/85 backdrop-blur-md"
          : "border-b border-transparent bg-transparent")
      }
    >
      <div className="mx-auto flex h-16 max-w-6xl items-center justify-between px-5 sm:px-8">
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

        <nav className="hidden items-center gap-8 md:flex" aria-label="Primary">
          {LINKS.map((l) => (
            <a
              key={l.href}
              href={l.href}
              className="text-sm text-muted transition-colors hover:text-text"
            >
              {l.label}
            </a>
          ))}
        </nav>

        <div className="flex items-center gap-2">
          <a
            href="#/connectors"
            className="text-sm text-muted transition-colors hover:text-text"
          >
            Connectors
          </a>
          <Button href={RELEASES_URL} target="_blank" rel="noreferrer" size="md">
            Get the app
            <ArrowUpRight size={16} strokeWidth={2} className="transition-transform duration-200 group-hover/btn:translate-x-0.5 group-hover/btn:-translate-y-0.5" />
          </Button>
        </div>
      </div>
    </header>
  );
}
