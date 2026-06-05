import type { ComponentPropsWithoutRef, ReactNode } from "react";
import { useInView } from "../lib/useMotion";

function cx(...parts: Array<string | false | undefined>): string {
  return parts.filter(Boolean).join(" ");
}

/* ── Button ───────────────────────────────────────────────────────────────
   One radius/depth system shared by every call to action. Primary is the solid
   green lifted from the app; ghost is a quiet hairline. Both compress slightly
   on press for tactile warm-modern feedback. */
type ButtonProps = ComponentPropsWithoutRef<"a"> & {
  variant?: "primary" | "ghost";
  size?: "md" | "lg";
};

export function Button({
  variant = "primary",
  size = "md",
  className,
  children,
  ...rest
}: ButtonProps) {
  const base =
    "group/btn inline-flex items-center justify-center gap-2 rounded-full font-medium " +
    "transition-[transform,background-color,border-color,box-shadow] duration-200 ease-out " +
    "active:translate-y-px focus-visible:outline-none";
  const sizes = { md: "h-11 px-5 text-[0.95rem]", lg: "h-13 px-7 text-base" };
  const variants = {
    primary:
      "bg-accent text-bg shadow-[var(--shadow-soft)] hover:bg-accent-strong " +
      "hover:shadow-[var(--shadow-lift)] hover:-translate-y-0.5",
    ghost:
      "bg-transparent text-text border border-border hover:border-accent/60 " +
      "hover:bg-accent-weak/60 hover:-translate-y-0.5",
  };
  return (
    <a className={cx(base, sizes[size], variants[variant], className)} {...rest}>
      {children}
    </a>
  );
}

/* ── Badge ───────────────────────────────────────────────────────────────── */
export function Badge({
  children,
  tone = "neutral",
}: {
  children: ReactNode;
  tone?: "neutral" | "accent" | "core";
}) {
  const tones = {
    neutral: "border-border bg-surface text-muted",
    accent: "border-accent/25 bg-accent-weak text-accent-strong",
    core: "border-[color:var(--core)]/30 bg-[color:var(--core-glow)]/12 text-[color:var(--core)]",
  };
  return (
    <span
      className={cx(
        "inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-medium tracking-tight",
        tones[tone],
      )}
    >
      {children}
    </span>
  );
}

/* ── Eyebrow ─────────────────────────────────────────────────────────────── */
export function Eyebrow({ children }: { children: ReactNode }) {
  return (
    <span className="inline-flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.16em] text-accent">
      <span aria-hidden className="h-px w-6 bg-accent/50" />
      {children}
    </span>
  );
}

/* ── Reveal ──────────────────────────────────────────────────────────────
   Wraps children in the CSS `.reveal` fade and flips it on once the block
   enters the viewport. `delay` staggers siblings; reduced-motion is handled in
   CSS so this stays a thin, dependency-free wrapper. */
export function Reveal({
  children,
  delay = 0,
  as: Tag = "div",
  className,
}: {
  children: ReactNode;
  delay?: number;
  as?: "div" | "li" | "section" | "header" | "article";
  className?: string;
}) {
  const { ref, inView } = useInView<HTMLDivElement>();
  return (
    <Tag
      ref={ref as never}
      className={cx("reveal", inView && "is-in", className)}
      style={{ ["--d" as string]: `${delay}ms` }}
    >
      {children}
    </Tag>
  );
}

export { cx };
