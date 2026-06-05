import { useEffect, useRef, useState } from "react";

/** True when the OS asks for reduced motion. Kept reactive so the page can
 *  honour a mid-session change without a reload. */
export function usePrefersReducedMotion(): boolean {
  const [reduced, setReduced] = useState(false);

  useEffect(() => {
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    const update = () => setReduced(mq.matches);
    update();
    mq.addEventListener("change", update);
    return () => mq.removeEventListener("change", update);
  }, []);

  return reduced;
}

/** Adds `is-in` once an element scrolls into view, then disconnects — every
 *  reveal fires exactly once and costs nothing afterwards. Drives both the
 *  `.reveal` fade and the `.draw` line-draw via CSS. */
export function useInView<T extends HTMLElement>(
  options: IntersectionObserverInit = { threshold: 0.18, rootMargin: "0px 0px -10% 0px" },
) {
  const ref = useRef<T | null>(null);
  const [inView, setInView] = useState(false);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (typeof IntersectionObserver === "undefined") {
      setInView(true);
      return;
    }
    const obs = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (entry.isIntersecting) {
          setInView(true);
          obs.disconnect();
          break;
        }
      }
    }, options);
    obs.observe(el);
    return () => obs.disconnect();
    // options is intentionally read once on mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return { ref, inView };
}
