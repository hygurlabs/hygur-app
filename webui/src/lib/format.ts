/** Short, locale-stable date label (en-GB → "5 Jun 2026"). Empty on bad input. */
export function fmtDate(value?: string | null): string {
  if (!value) return "";
  const t = new Date(value);
  if (Number.isNaN(t.getTime())) return "";
  return t.toLocaleDateString("en-GB", {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

/** Date + time, used for calendar events. */
export function fmtDateTime(value?: string | null): string {
  if (!value) return "";
  const t = new Date(value);
  if (Number.isNaN(t.getTime())) return "";
  return t.toLocaleString("en-GB", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** Locale-stable grouped number (en-GB → "1,234"). */
export function fmtNumber(n: number): string {
  return n.toLocaleString("en-GB");
}

/** Fallback dot colour for a tag with no colour of its own. */
export const DEFAULT_TAG_COLOR = "#3B82F6";

const SOURCE_LABELS: Record<string, string> = {
  mail: "mail",
  email: "mail",
  thread: "mail",
  note: "note",
  markdown: "doc",
  pdf: "pdf",
  txt: "text",
  docx: "doc",
  file: "file",
};

/** Collapses the many source_type values into a tidy badge label. */
export function srcLabel(t?: string): string {
  if (!t) return "source";
  return SOURCE_LABELS[t] ?? t;
}
