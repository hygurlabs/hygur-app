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
