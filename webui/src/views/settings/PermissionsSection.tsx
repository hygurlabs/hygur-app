import { useEffect, useState } from "react";
import { native } from "../../lib/native";
import { Row, Section } from "./common";

// MARK: - Permission status (read-only)

const PERMS: { key: string; label: string }[] = [
  { key: "microphone", label: "Microphone" },
  { key: "speech", label: "Speech recognition" },
  { key: "calendar", label: "Calendar" },
  { key: "notifications", label: "Notifications" },
];

function statusColor(s?: string): string {
  if (s === "authorized") return "var(--accent)";
  if (s === "denied" || s === "restricted") return "var(--danger)";
  return "var(--faint)";
}

export function PermissionsSection() {
  const [status, setStatus] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!native.available) return;
    let cancelled = false;
    native.perms.status().then((s) => {
      if (!cancelled) setStatus(s);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  if (!native.available) return null;

  return (
    <Section title="Permissions">
      {PERMS.map((p) => (
        <Row key={p.key} label={p.label}>
          <span className="inline-flex items-center gap-2 text-[12.5px] text-muted">
            <span
              aria-hidden
              className="size-2 rounded-full"
              style={{ background: statusColor(status[p.key]) }}
            />
            {status[p.key] ?? "unknown"}
          </span>
        </Row>
      ))}
    </Section>
  );
}
