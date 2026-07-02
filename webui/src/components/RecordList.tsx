import type { ReactNode } from "react";
import { FolderKanban } from "lucide-react";
import { DEFAULT_TAG_COLOR } from "../lib/format";
import { Badge, StatusBadge, type StatusVariant } from "./ui";

export interface RecordRow {
  id: string;
  title?: ReactNode;
  badge?: string;
  /** When set, the badge renders through `StatusBadge` with this state colour
   *  instead of the neutral descriptive `Badge`. */
  badgeVariant?: StatusVariant;
  meta?: string;
  excerpt?: string;
  accent?: string; // optional left color dot (tags, calendars)
  icon?: ReactNode; // optional leading source glyph
  /** Optional attached project — rendered as a pill with the project glyph. */
  projectName?: string;
  /** Optional tags — rendered as colored pills (first 3, then a "+N" count). */
  tags?: { name: string; color?: string }[];
  onClick?: () => void;

  // --- Custom-row mode (rich lists with leading controls / trailing actions).
  // When `content` is set the row renders `leading · content · trailing` in a
  // flex row and the structured fields above are ignored; each caller supplies
  // the exact inner markup so behaviour stays identical. ---
  content?: ReactNode;
  leading?: ReactNode; // left-hand control (checkbox, health dot, …)
  trailing?: ReactNode; // right-hand actions (buttons, delete confirm, …)
  selected?: boolean; // highlighted row background
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

/** The row badge — a coloured `StatusBadge` when a state variant is given,
 *  otherwise the neutral descriptive `Badge`. */
function RowBadge({ row }: { row: RecordRow }) {
  if (!row.badge) return null;
  return row.badgeVariant ? (
    <StatusBadge variant={row.badgeVariant}>{row.badge}</StatusBadge>
  ) : (
    <Badge>{row.badge}</Badge>
  );
}

/** Divider-separated rows — the minimalism alternative to card spam. Title +
 *  quiet meta on the baseline, a clamped two-line excerpt below. The whole row
 *  is the hit target when `onClick` is provided. In `compact` mode each record
 *  collapses to a single line (title · project · tags · date, no excerpt).
 *
 *  Rows carrying a `content` node switch to a flex layout (leading control ·
 *  content · trailing actions) — the one primitive behind the app's richer,
 *  stateful lists. `variant="card"` wraps the whole list in a rounded, filled
 *  card (with an optional `accent` frame); `align` sets flex-row alignment. */
export function RecordList({
  rows,
  compact = false,
  variant = "divider",
  accent = false,
  align = "center",
}: {
  rows: RecordRow[];
  compact?: boolean;
  variant?: "divider" | "card";
  accent?: boolean;
  align?: "center" | "start";
}) {
  const card = variant === "card";
  const listClass = card
    ? `divide-y divide-border rounded-xl border ${
        accent ? "border-accent/40 bg-accent-weak/20" : "border-border bg-surface"
      }`
    : "border-t border-border";
  const liClass = card ? "" : "border-b border-border";
  const alignClass = align === "start" ? "items-start" : "items-center";
  const customPad = card ? "px-3.5 py-3" : "px-1 py-3.5";

  return (
    <ul className={listClass}>
      {rows.map((r) => {
        // --- Custom-row mode: caller-supplied inner markup, no whole-row click. ---
        if (r.content !== undefined) {
          return (
            <li key={r.id} className={liClass}>
              <div
                className={`group flex gap-3 ${alignClass} ${customPad} ${
                  r.selected ? "bg-accent-weak/40" : ""
                }`}
              >
                {r.leading}
                <div className="min-w-0 flex-1">{r.content}</div>
                {r.trailing}
              </div>
            </li>
          );
        }

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
            <li key={r.id} className={liClass}>
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
          <li key={r.id} className={liClass}>
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
                <RowBadge row={r} />
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
