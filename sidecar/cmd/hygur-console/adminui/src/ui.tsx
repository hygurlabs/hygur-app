import { useEffect, useRef, useState, type ReactNode } from "react";

export function fmtInt(v: number): string {
  return v.toLocaleString();
}
export function fmtMoney(v: number, currency: string): string {
  return currency + v.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

function reducedMotion(): boolean {
  return typeof window !== "undefined" && !!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
}

/** Animate a number toward `value` (utility motion). Respects reduced-motion. */
export function CountUp({ value, decimals = 2, prefix = "" }: { value: number; decimals?: number; prefix?: string }) {
  const [n, setN] = useState(value);
  const prev = useRef(0);
  useEffect(() => {
    if (reducedMotion()) {
      setN(value);
      prev.current = value;
      return;
    }
    const from = prev.current;
    const to = value;
    const start = performance.now();
    const dur = 600;
    let raf = 0;
    const tick = (t: number) => {
      const p = Math.min(1, (t - start) / dur);
      const e = 1 - Math.pow(1 - p, 3);
      setN(from + (to - from) * e);
      if (p < 1) raf = requestAnimationFrame(tick);
      else prev.current = to;
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [value]);
  return <>{prefix}{n.toLocaleString(undefined, { minimumFractionDigits: decimals, maximumFractionDigits: decimals })}</>;
}

export function MetricTile({ label, children, sub, alert }: { label: string; children: ReactNode; sub?: string; alert?: boolean }) {
  return (
    <div className={"tile" + (alert ? " alert" : "")}>
      <span className="label">{label}</span>
      <span className="display tnum">{children}</span>
      {sub ? <div className="sub">{sub}</div> : null}
    </div>
  );
}

export function Skeleton({ w = "100%", h = 14 }: { w?: string | number; h?: number }) {
  return <div className="skel" style={{ width: w, height: h }} />;
}
