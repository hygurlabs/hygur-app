import { Nav } from "./Nav";
import { Hero } from "./Hero";
import { Principles } from "./Principles";
import { Everyday } from "./Everyday";
import { Grounded } from "./Grounded";
import { Editions } from "./Editions";
import { HowItWorks } from "./HowItWorks";
import { CtaBand } from "./CtaBand";
import { Footer } from "./Footer";

export function Home() {
  return (
    <>
      <a
        href="#editions"
        className="sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-[60] focus:rounded-full focus:bg-accent focus:px-4 focus:py-2 focus:text-sm focus:text-bg"
      >
        Skip to editions
      </a>
      <Nav />
      <main>
        <Hero />
        <Principles />
        <Everyday />
        <Grounded />
        <Editions />
        <HowItWorks />
        <CtaBand />
      </main>
      <Footer />
    </>
  );
}
