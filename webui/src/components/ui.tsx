import type { ReactNode } from "react";

/** Scrollable, max-width column shared by the list/detail views. The Ask view
 *  manages its own full-height layout and does not use this. */
export function Page({ children }: { children: ReactNode }) {
  return (
    <div className="h-full overflow-y-auto">
      <div className="view-enter mx-auto max-w-[760px] px-4 pb-24 pt-9 sm:px-7">
        {children}
      </div>
    </div>
  );
}

/** A view's title block: serif display headline + one quiet line of context.
 *  Typography carries the hierarchy — no chrome, per the minimalism direction. */
export function PageHeader({
  title,
  subtitle,
  actions,
}: {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
}) {
  return (
    <header className="mb-7 flex items-start justify-between gap-4">
      <div>
        <h1 className="font-display text-[26px] font-semibold leading-tight tracking-tight">
          {title}
        </h1>
        {subtitle && (
          <p className="mt-1 max-w-[64ch] text-[13.5px] text-muted">
            {subtitle}
          </p>
        )}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </header>
  );
}

/** Small uppercase tag used for source kind / status. */
export function Badge({ children }: { children: ReactNode }) {
  return (
    <span className="rounded-full bg-accent-weak px-2 py-0.5 text-[11px] font-medium uppercase tracking-wide text-accent">
      {children}
    </span>
  );
}

/** Skeleton lines that preserve list rhythm while loading. */
export function Skeleton({ rows = 3 }: { rows?: number }) {
  return (
    <div className="space-y-3 py-2" aria-hidden>
      {Array.from({ length: rows }).map((_, i) => (
        <div
          key={i}
          className="h-3.5 animate-pulse rounded bg-surface2"
          style={{ width: `${72 - i * 9}%` }}
        />
      ))}
    </div>
  );
}

/** Explains what's missing and what to do next — never a bare blank. */
export function EmptyState({
  title,
  hint,
  action,
}: {
  title: string;
  hint?: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center gap-3 px-6 py-16 text-center">
      <p className="font-display text-[17px] text-text">{title}</p>
      {hint && <p className="max-w-[42ch] text-[13.5px] text-muted">{hint}</p>}
      {action}
    </div>
  );
}

/** Says what failed and (where known) how to recover, preserving the layout. */
export function ErrorBanner({
  message,
  onRetry,
}: {
  message: string;
  onRetry?: () => void;
}) {
  return (
    <div className="mb-4 flex items-center justify-between gap-3 rounded-lg border border-danger/40 bg-danger/5 px-4 py-2.5 text-[13.5px] text-danger">
      <span>{message}</span>
      {onRetry && (
        <button
          onClick={onRetry}
          className="shrink-0 rounded-md border border-danger/40 px-2.5 py-1 text-[12.5px] font-medium transition-colors hover:bg-danger/10"
        >
          Retry
        </button>
      )}
    </div>
  );
}

/** The one button system: solid accent (primary) or quiet outline (ghost). */
export function Button({
  children,
  onClick,
  variant = "primary",
  disabled,
  type = "button",
}: {
  children: ReactNode;
  onClick?: () => void;
  variant?: "primary" | "ghost";
  disabled?: boolean;
  type?: "button" | "submit";
}) {
  const base =
    "inline-flex items-center gap-2 rounded-lg px-3.5 py-2 text-sm font-medium transition-colors disabled:opacity-40 disabled:cursor-default";
  const styles =
    variant === "primary"
      ? "bg-accent text-white hover:opacity-90"
      : "border border-border bg-surface text-text hover:border-accent hover:text-accent";
  return (
    <button type={type} onClick={onClick} disabled={disabled} className={`${base} ${styles}`}>
      {children}
    </button>
  );
}

/** Shared text/search input — one radius, one focus treatment. */
export function TextInput(props: React.InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      {...props}
      className={`w-full rounded-lg border border-border bg-surface px-3.5 py-2 text-sm text-text outline-none transition-colors placeholder:text-faint focus:border-accent ${props.className ?? ""}`}
    />
  );
}
