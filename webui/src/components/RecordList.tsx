import type { ReactNode } from "react";
import { FolderKanban } from "lucide-react";
import { DEFAULT_TAG_COLOR } from "../lib/format";
import { Badge } from "./ui";

export interface RecordRow {
  id: string;
  title: string;
  badge?: string;
  meta?: string;
  excerpt?: string;
  accent?: string; // optional left color dot (tags, calendars)
  icon?: ReactNode; // optional leading source glyph
  /** Optional attached project — rendered as a pill with the project glyph. */
  projectName?: string;
  /** Optional tags — rendered as colored pills (first 3, then a "+N" count). */
  tags?: { name: string; color?: string }[];
  onClick?: () => void;
}

/** Project + tag pills (project glyph, then up to 3 colored tags, then "+N").
 *  Shared by the normal (wrapped) and compact (inline) row layouts. */
function RowPills({ row }: { row: RecordRow }) {
  if (!row.projectName && !(row.tags && row.tags.length > 0)) return null;
  return (
    <>
      {row.projectName && (
        <span className="inline-flex max-w-[200px] items-center gap-1 rounded-full bg-surface2 px-2 py-0.5 text-[11.5px] text-muted">
          <FolderKanban size={11} strokeWidth={2} className="shrink-0 text-accent" />
          <span className="truncate">{row.projectName}</span>
        </span>
      )}
      {row.tags?.slice(0, 3).map((t) => (
        <span
          key={t.name}
          className="inline-flex max-w-[160px] items-center gap-1.5 rounded-full bg-surface2 px-2 py-0.5 text-[11.5px] text-muted"
        >
          <span
            aria-hidden
            className="size-2 shrink-0 rounded-full"
            style={{ background: t.color || DEFAULT_TAG_COLOR }}
          />
          <span className="truncate">{t.name}</span>
        </span>
      ))}
      {row.tags && row.tags.length > 3 && (
        <span className="text-[11px] text-faint">+{row.tags.length - 3}</span>
      )}
    </>
  );
}

/** Divider-separated rows — the minimalism alternative to card spam. Title +
 *  quiet meta on the baseline, a clamped two-line excerpt below. The whole row
 *  is the hit target when `onClick` is provided. In `compact` mode each record
 *  collapses to a single line (title · project · tags · date, no excerpt). */
export function RecordList({
  rows,
  compact = false,
}: {
  rows: RecordRow[];
  compact?: boolean;
}) {
  return (
    <ul className="border-t border-border">
      {rows.map((r) => {
        const interactive = Boolean(r.onClick);
        const rowKeyDown = interactive
          ? (e: React.KeyboardEvent) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                r.onClick?.();
              }
            }
          : undefined;
        const hover = interactive
          ? "cursor-pointer transition-colors hover:bg-accent-weak/50 focus:bg-accent-weak/50 focus:outline-none"
          : "";

        if (compact) {
          return (
            <li key={r.id} className="border-b border-border">
              <div
                role={interactive ? "button" : undefined}
                tabIndex={interactive ? 0 : undefined}
                onClick={r.onClick}
                onKeyDown={rowKeyDown}
                className={`group flex items-center gap-2.5 px-1 py-2 ${hover}`}
              >
                {r.accent && (
                  <span
                    aria-hidden
                    className="size-2.5 shrink-0 rounded-full"
                    style={{ background: r.accent }}
                  />
                )}
                {r.icon}
                <span className="min-w-0 flex-1 truncate font-medium">
                  {r.title || "(untitled)"}
                </span>
                <span className="flex shrink-0 items-center gap-1.5">
                  <RowPills row={r} />
                </span>
                {r.meta && (
                  <span className="tnum shrink-0 whitespace-nowrap text-[12.5px] text-muted">
                    {r.meta}
                  </span>
                )}
              </div>
            </li>
          );
        }

        return (
          <li key={r.id} className="border-b border-border">
            <div
              role={interactive ? "button" : undefined}
              tabIndex={interactive ? 0 : undefined}
              onClick={r.onClick}
              onKeyDown={rowKeyDown}
              className={`group grid grid-cols-[1fr_auto] items-baseline gap-x-4 gap-y-1 px-1 py-3.5 ${hover}`}
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
              {(r.projectName || (r.tags && r.tags.length > 0)) && (
                <div className="col-span-2 flex flex-wrap items-center gap-1.5">
                  <RowPills row={r} />
                </div>
              )}
            </div>
          </li>
        );
      })}
    </ul>
  );
}
