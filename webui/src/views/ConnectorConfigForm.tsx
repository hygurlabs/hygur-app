import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, ExternalLink } from "lucide-react";
import { api } from "../lib/api";
import { native } from "../lib/native";
import type { ConfigField } from "../lib/types";
import { Button, ErrorBanner, Page, Skeleton, TextInput } from "../components/ui";

// Quick-fill presets for cron sync schedules (short / medium / long).
const CRON_PRESETS = [
  { label: "5 min", expr: "*/5 * * * *" },
  { label: "1 h", expr: "0 * * * *" },
  { label: "6 h", expr: "0 */6 * * *" },
];

/** Schema-driven configuration form for one connector. Renders whatever
 *  config_schema the sidecar returns (same contract as the native form), so it
 *  works for every connector type — present and future (CalDav included). */
export function ConnectorConfigForm({
  id,
  onClose,
}: {
  id: string;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const detailQ = useQuery({
    queryKey: ["connector", id],
    queryFn: () => api.connectorDetail(id),
  });
  const detail = detailQ.data;

  const [values, setValues] = useState<Record<string, string>>({});
  const [oauthCode, setOauthCode] = useState("");
  const [oauthStarted, setOauthStarted] = useState(false);

  // Seed field values from the current config + defaults once loaded.
  useEffect(() => {
    if (!detail) return;
    const next: Record<string, string> = {};
    for (const g of detail.config_schema.groups ?? []) {
      for (const f of g.fields) {
        if (f.type === "cron") {
          next[f.key] = detail.config.schedule ?? f.default ?? "";
        } else if (f.type !== "secret") {
          next[f.key] = detail.config.settings?.[f.key] ?? f.default ?? "";
        } else {
          next[f.key] = ""; // secrets are write-only
        }
      }
    }
    setValues(next);
  }, [detail]);

  const fields = useMemo(
    () => (detail?.config_schema.groups ?? []).flatMap((g) => g.fields),
    [detail],
  );

  const save = useMutation({
    mutationFn: async () => {
      if (!detail) return;
      const settings: Record<string, string> = {};
      const secrets: Record<string, string> = {};
      let schedule = detail.config.schedule ?? "";
      for (const f of fields) {
        const v = values[f.key] ?? "";
        if (f.type === "secret") {
          if (v) secrets[f.key] = v;
        } else if (f.type === "oauth" || f.type === "permission_check") {
          // handled out of band
        } else if (f.type === "cron") {
          schedule = v;
        } else {
          settings[f.key] = v;
        }
      }
      await api.configureConnector(id, {
        enabled: detail.config.enabled,
        settings,
        schedule,
      });
      if (Object.keys(secrets).length > 0) {
        await api.saveConnectorCredentials(id, secrets);
      }
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["connector", id] });
      qc.invalidateQueries({ queryKey: ["connectors"] });
    },
  });

  const oauth = useMutation({
    mutationFn: async () => {
      await save.mutateAsync(); // persist provider/client fields first
      const { url } = await api.connectorAuthUrl(id);
      await native.openExternal(url);
      setOauthStarted(true);
    },
  });
  const oauthSubmit = useMutation({
    mutationFn: () => api.connectorAuthCallback(id, oauthCode.trim()),
    onSuccess: () => {
      setOauthStarted(false);
      setOauthCode("");
      qc.invalidateQueries({ queryKey: ["connector", id] });
      qc.invalidateQueries({ queryKey: ["connectors"] });
    },
  });

  if (detailQ.isLoading || !detail) {
    return (
      <Page>
        <BackBar onClose={onClose} />
        {detailQ.error ? (
          <ErrorBanner message={`Couldn't load connector: ${(detailQ.error as Error).message}`} />
        ) : (
          <Skeleton rows={6} />
        )}
      </Page>
    );
  }

  const visible = (f: ConfigField) =>
    !f.condition || values[f.condition.field] === f.condition.value;

  const set = (key: string, v: string) => setValues((s) => ({ ...s, [key]: v }));

  // When the connector is already connected, dynamic folder pickers can load
  // their list automatically (no manual "Load folders" click).
  const connectorHealthy = ["ok", "healthy", "connected", "up"].includes(
    (detail.health.status ?? "").toLowerCase(),
  );

  return (
    <Page>
      <BackBar onClose={onClose}>
        <Button onClick={() => save.mutate()} disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save"}
        </Button>
      </BackBar>

      <h1 className="mb-1 font-display text-[24px] font-semibold tracking-tight">
        {detail.info.name}
      </h1>
      {detail.info.description && (
        <p className="mb-6 text-[13.5px] text-muted">{detail.info.description}</p>
      )}

      {(save.error || oauth.error || oauthSubmit.error) && (
        <ErrorBanner
          message={`Action failed: ${
            ((save.error || oauth.error || oauthSubmit.error) as Error).message
          }`}
        />
      )}

      {(detail.config_schema.groups ?? []).map((group) => (
        <section key={group.title} className="mb-7">
          <h2 className="mb-2 text-[11.5px] font-medium uppercase tracking-[0.09em] text-faint">
            {group.title}
          </h2>
          <div className="flex flex-col divide-y divide-border rounded-xl border border-border bg-surface">
            {group.fields.filter(visible).map((f) => (
              <div key={f.key} className="flex items-center justify-between gap-4 px-4 py-3">
                <div className="min-w-0">
                  <p className="text-[14px]">{f.label}</p>
                  {f.description && (
                    <p className="mt-0.5 text-[12.5px] text-muted">{f.description}</p>
                  )}
                </div>
                <div className="shrink-0">
                  <FieldControl
                    field={f}
                    value={values[f.key] ?? ""}
                    connectorId={id}
                    connectorHealthy={connectorHealthy}
                    onChange={(v) => set(f.key, v)}
                    onOAuth={() => oauth.mutate()}
                    oauthBusy={oauth.isPending}
                  />
                </div>
              </div>
            ))}
          </div>
        </section>
      ))}

      {oauthStarted && (
        <div className="mb-7 rounded-xl border border-accent/40 bg-accent-weak/40 p-4">
          <p className="mb-2 text-[13px] text-muted">
            A browser window opened — authorize Hygur, then paste the code here.
          </p>
          <div className="flex gap-2">
            <TextInput
              value={oauthCode}
              onChange={(e) => setOauthCode(e.target.value)}
              placeholder="Paste authorization code…"
            />
            <Button
              onClick={() => oauthSubmit.mutate()}
              disabled={!oauthCode.trim() || oauthSubmit.isPending}
            >
              {oauthSubmit.isPending ? "Validating…" : "Validate"}
            </Button>
          </div>
        </div>
      )}
    </Page>
  );
}

function BackBar({ onClose, children }: { onClose: () => void; children?: React.ReactNode }) {
  return (
    <div className="mb-5 flex items-center justify-between gap-3">
      <button
        onClick={onClose}
        className="inline-flex items-center gap-1.5 text-[13.5px] text-muted transition-colors hover:text-text"
      >
        <ArrowLeft size={16} strokeWidth={1.75} /> Connectors
      </button>
      <div className="flex items-center gap-2">{children}</div>
    </div>
  );
}

function FieldControl({
  field,
  value,
  connectorId,
  connectorHealthy,
  onChange,
  onOAuth,
  oauthBusy,
}: {
  field: ConfigField;
  value: string;
  connectorId: string;
  connectorHealthy: boolean;
  onChange: (v: string) => void;
  onOAuth: () => void;
  oauthBusy: boolean;
}) {
  switch (field.type) {
    case "multi_enum":
      return (
        <MultiEnumField
          field={field}
          value={value}
          connectorId={connectorId}
          connectorHealthy={connectorHealthy}
          onChange={onChange}
        />
      );
    case "bool":
      return (
        <button
          role="switch"
          aria-checked={value === "true"}
          onClick={() => onChange(value === "true" ? "false" : "true")}
          className={`inline-flex h-6 w-11 items-center rounded-full px-0.5 transition-colors ${
            value === "true" ? "bg-accent" : "bg-border"
          }`}
        >
          <span
            className={`size-5 rounded-full bg-white shadow-sm transition-transform ${
              value === "true" ? "translate-x-5" : "translate-x-0"
            }`}
          />
        </button>
      );
    case "enum":
      return (
        <select
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className="rounded-lg border border-border bg-surface px-3 py-2 text-sm outline-none focus:border-accent"
        >
          {(field.options ?? []).map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      );
    case "int":
      return (
        <TextInput
          type="number"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className="w-28"
        />
      );
    case "secret":
      return (
        <TextInput
          type="password"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder="•••• (unchanged)"
          className="w-56"
        />
      );
    case "oauth":
      return (
        <Button variant="ghost" onClick={onOAuth} disabled={oauthBusy}>
          <ExternalLink size={14} strokeWidth={1.75} />
          {oauthBusy ? "Opening…" : "Connect"}
        </Button>
      );
    case "permission_check":
      return (
        <Button
          variant="ghost"
          onClick={() => {
            if (field.description?.includes("://")) void native.openExternal(field.description);
          }}
        >
          {field.default || "Open settings"}
        </Button>
      );
    case "cron":
      return (
        <div className="flex flex-col items-end gap-1.5">
          <TextInput
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder="*/5 * * * *"
            className="w-44"
          />
          <div className="flex gap-1">
            {CRON_PRESETS.map((p) => (
              <button
                key={p.expr}
                type="button"
                onClick={() => onChange(p.expr)}
                className={`rounded-md border px-2 py-0.5 text-[11.5px] transition-colors ${
                  value === p.expr
                    ? "border-accent/40 bg-accent-weak text-accent"
                    : "border-border text-muted hover:border-accent/40 hover:text-accent"
                }`}
              >
                {p.label}
              </button>
            ))}
          </div>
        </div>
      );
    default:
      // string, path — text input.
      return (
        <TextInput
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className="w-56"
        />
      );
  }
}

/** A checkbox folder/label selector. When the field has static options it shows
 *  them; otherwise it loads the real list from the connected account via
 *  GET /connectors/{id}/mailboxes ("Load folders" after connecting). The value
 *  is a comma-separated list, matching what the sync engine expects. */
function MultiEnumField({
  field,
  value,
  connectorId,
  connectorHealthy,
  onChange,
}: {
  field: ConfigField;
  value: string;
  connectorId: string;
  connectorHealthy: boolean;
  onChange: (v: string) => void;
}) {
  const selected = new Set(
    value
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean),
  );
  const staticOptions = field.options ?? [];
  const hasStatic = staticOptions.length > 0;
  const [loaded, setLoaded] = useState<string[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const load = async () => {
    setLoading(true);
    setErr(null);
    try {
      setLoaded(await api.connectorMailboxes(connectorId));
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setLoading(false);
    }
  };

  // Auto-load the folder list once when the connector is already connected, so
  // the user doesn't have to click "Load folders". Stays manual (button only)
  // when not yet connected, to avoid a guaranteed error on open.
  useEffect(() => {
    if (!hasStatic && connectorHealthy && loaded === null && !loading) {
      void load();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasStatic, connectorHealthy]);

  const toggle = (v: string) => {
    const next = new Set(selected);
    if (next.has(v)) next.delete(v);
    else next.add(v);
    onChange(Array.from(next).join(","));
  };

  // Static options if declared; else the loaded folders; else fall back to the
  // already-selected values so a saved selection stays visible before loading.
  const options = hasStatic
    ? staticOptions.map((o) => ({ value: o.value, label: o.label }))
    : (loaded ?? Array.from(selected)).map((v) => ({ value: v, label: v }));

  return (
    <div className="flex w-60 flex-col items-end gap-1.5">
      {!hasStatic && (
        <button
          type="button"
          onClick={load}
          disabled={loading}
          className="rounded-md border border-border px-2 py-0.5 text-[11.5px] text-muted transition-colors hover:border-accent/40 hover:text-accent disabled:opacity-40"
        >
          {loading ? "Loading…" : loaded ? "Refresh" : "Load folders"}
        </button>
      )}
      {err && (
        <p className="text-right text-[11.5px] text-danger">
          Connect first (save, then retry).
        </p>
      )}
      {options.length > 0 ? (
        <div className="max-h-44 w-full overflow-y-auto rounded-lg border border-border bg-surface p-1">
          {options.map((o) => (
            <label
              key={o.value}
              className="flex cursor-pointer items-center gap-2 rounded px-2 py-1 text-[13px] hover:bg-surface2"
            >
              <input
                type="checkbox"
                checked={selected.has(o.value)}
                onChange={() => toggle(o.value)}
                className="accent-[var(--accent)]"
              />
              <span className="truncate">{o.label}</span>
            </label>
          ))}
        </div>
      ) : (
        !loading &&
        !hasStatic && (
          <p className="text-right text-[11.5px] text-faint">
            No folders — load the list after connecting.
          </p>
        )
      )}
    </div>
  );
}
