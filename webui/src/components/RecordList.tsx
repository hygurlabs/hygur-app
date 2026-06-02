import type { ReactNode } from "react";
import { Badge } from "./ui";

export interface RecordRow {
  id: string;
  title: string;
  badge?: string;
  meta?: string;
  excerpt?: string;
  accent?: string; // optional left color dot (tags, calendars)
  icon?: ReactNode; // optional leading source glyph
  onClick?: () => void;
}

/** Divider-separated rows — the minimalism alternative to card spam. Title +
 *  quiet meta on the baseline, a clamped two-line excerpt below. The whole row
 *  is the hit target when `onClick` is provided. */
export function RecordList({ rows }: { rows: RecordRow[] }) {
  return (
    <ul className="border-t border-border">
      {rows.map((r) => {
        const interactive = Boolean(r.onClick);
        return (
          <li key={r.id} className="border-b border-border">
            <div
              role={interactive ? "button" : undefined}
              tabIndex={interactive ? 0 : undefined}
              onClick={r.onClick}
              onKeyDown={
                interactive
                  ? (e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        r.onClick?.();
                      }
                    }
                  : undefined
              }
              className={`group grid grid-cols-[1fr_auto] items-baseline gap-x-4 gap-y-1 px-1 py-3.5 ${
                interactive
                  ? "cursor-pointer transition-colors hover:bg-accent-weak/50 focus:bg-accent-weak/50 focus:outline-none"
                  : ""
              }`}
            >
              <span className="flex min-w-0 items-center gap-2 font-medium">
                {r.accent && (
                  <span
                    aria-hidden
                    className="size-2.5 shrink-0 rounded-full"
                    style={{ background: r.accent }}
                  />
                )}
                {r.icon}
                <span className="truncate">{r.title || "(untitled)"}</span>
              </span>
              <span className="flex items-center gap-2.5 text-[12.5px] text-muted">
                {r.badge && <Badge>{r.badge}</Badge>}
                {r.meta && <span className="tnum whitespace-nowrap">{r.meta}</span>}
              </span>
              {r.excerpt && (
                <p className="col-span-2 line-clamp-2 text-[13.5px] text-muted">
                  {r.excerpt}
                </p>
              )}
            </div>
          </li>
        );
      })}
    </ul>
  );
}
