import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { native } from "../lib/native";
import type { SidecarConfig } from "../lib/types";
import {
  Button,
  ErrorBanner,
  Page,
  PageHeader,
  Skeleton,
  TextInput,
} from "../components/ui";

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="mb-9">
      <h2 className="mb-3 text-[11.5px] font-medium uppercase tracking-[0.09em] text-faint">
        {title}
      </h2>
      <div className="flex flex-col divide-y divide-border rounded-xl border border-border bg-surface">
        {children}
      </div>
    </section>
  );
}

function Row({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center justify-between gap-4 px-4 py-3">
      <div className="min-w-0">
        <p className="text-[14px]">{label}</p>
        {hint && <p className="mt-0.5 text-[12.5px] text-muted">{hint}</p>}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  );
}

function Toggle({
  checked,
  onChange,
  disabled,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <button
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={`inline-flex h-6 w-11 shrink-0 items-center rounded-full px-0.5 transition-colors disabled:opacity-40 ${
        checked ? "bg-accent" : "bg-border"
      }`}
    >
      <span
        className={`size-5 rounded-full bg-white shadow-sm transition-transform ${
          checked ? "translate-x-5" : "translate-x-0"
        }`}
      />
    </button>
  );
}

export function Settings() {
  const qc = useQueryClient();
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["config"],
    queryFn: () => api.config(),
  });

  const [draft, setDraft] = useState<SidecarConfig | null>(null);
  useEffect(() => {
    if (data) setDraft(data);
  }, [data]);

  const save = useMutation({
    mutationFn: (cfg: SidecarConfig) =>
      api.patchConfig({
        lm_studio: {
          url: cfg.lm_studio.url,
          embedding_url: cfg.lm_studio.embedding_url,
          indexing_url: cfg.lm_studio.indexing_url,
          model_default: cfg.lm_studio.model_default,
          model_indexing: cfg.lm_studio.model_indexing,
          embedding_model: cfg.lm_studio.embedding_model,
          embedding_max_tokens: cfg.lm_studio.embedding_max_tokens,
          embedding_batch_size: cfg.lm_studio.embedding_batch_size,
        },
        logging: { level: cfg.logging.level },
        daily_brief: {
          enabled: cfg.daily_brief.enabled,
          hour_local: cfg.daily_brief.hour_local,
          lookback_hours: cfg.daily_brief.lookback_hours,
        },
        retrieval: {
          use_llm_intent: cfg.retrieval.use_llm_intent,
          use_judge: cfg.retrieval.use_judge,
          temporal_scoring_mode: cfg.retrieval.temporal_scoring_mode,
        },
        mail: { reconcile_deletions: cfg.mail.reconcile_deletions },
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["config"] }),
  });

  if (isLoading || !draft) {
    return (
      <Page>
        <PageHeader title="Settings" />
        {error ? (
          <ErrorBanner
            message={`Couldn't load settings: ${(error as Error).message}`}
            onRetry={() => refetch()}
          />
        ) : (
          <Skeleton rows={6} />
        )}
      </Page>
    );
  }

  // Typed nested setters keep the JSX terse.
  const set = <K extends keyof SidecarConfig>(
    section: K,
    patch: Partial<SidecarConfig[K]>,
  ) => setDraft((d) => (d ? { ...d, [section]: { ...d[section], ...patch } } : d));

  const dirty = JSON.stringify(draft) !== JSON.stringify(data);

  return (
    <Page>
      <PageHeader
        title="Settings"
        subtitle="Everything runs locally. Changes are saved to the sidecar."
        actions={
          <Button onClick={() => save.mutate(draft)} disabled={!dirty || save.isPending}>
            {save.isPending ? "Saving…" : "Save"}
          </Button>
        }
      />

      {save.error && (
        <ErrorBanner message={`Couldn't save: ${(save.error as Error).message}`} />
      )}

      <Section title="AI runtime">
        <Row label="Inference URL" hint="OpenAI-compatible chat endpoint (LM Studio, vLLM…)">
          <TextInput
            value={draft.lm_studio.url}
            onChange={(e) => set("lm_studio", { url: e.target.value })}
            className="w-64"
          />
        </Row>
        <Row label="Embedding URL" hint="Leave empty to reuse the inference URL">
          <TextInput
            value={draft.lm_studio.embedding_url}
            onChange={(e) => set("lm_studio", { embedding_url: e.target.value })}
            className="w-64"
          />
        </Row>
        <Row label="Indexing URL" hint="Fast small model for ingestion-time extraction (empty = inference URL)">
          <TextInput
            value={draft.lm_studio.indexing_url}
            onChange={(e) => set("lm_studio", { indexing_url: e.target.value })}
            className="w-64"
          />
        </Row>
        <Row label="Chat model">
          <TextInput
            value={draft.lm_studio.model_default}
            onChange={(e) => set("lm_studio", { model_default: e.target.value })}
            className="w-64"
          />
        </Row>
        <Row label="Indexing model" hint="Small model used during ingestion (empty = chat model)">
          <TextInput
            value={draft.lm_studio.model_indexing}
            onChange={(e) => set("lm_studio", { model_indexing: e.target.value })}
            className="w-64"
          />
        </Row>
        <Row label="Embedding model">
          <TextInput
            value={draft.lm_studio.embedding_model}
            onChange={(e) => set("lm_studio", { embedding_model: e.target.value })}
            className="w-64"
          />
        </Row>
        <Row label="Embedding max tokens" hint="Per-input cap before the embedding call">
          <TextInput
            type="number"
            value={String(draft.lm_studio.embedding_max_tokens)}
            onChange={(e) =>
              set("lm_studio", { embedding_max_tokens: Number(e.target.value) || 0 })
            }
            className="w-28"
          />
        </Row>
        <Row
          label="Embedding batch size"
          hint="Chunks per request during indexing — higher = faster (lower if your server rejects big batches)"
        >
          <TextInput
            type="number"
            value={String(draft.lm_studio.embedding_batch_size)}
            onChange={(e) =>
              set("lm_studio", { embedding_batch_size: Number(e.target.value) || 0 })
            }
            className="w-28"
          />
        </Row>
      </Section>

      <Section title="Briefings">
        <Row label="Daily brief" hint="Generate a morning digest of recent activity">
          <Toggle
            checked={draft.daily_brief.enabled}
            onChange={(v) => set("daily_brief", { enabled: v })}
          />
        </Row>
        <Row label="Hour" hint="Local time, HH:MM">
          <TextInput
            value={draft.daily_brief.hour_local}
            onChange={(e) => set("daily_brief", { hour_local: e.target.value })}
            className="w-24"
          />
        </Row>
        <Row label="Lookback hours">
          <TextInput
            type="number"
            value={String(draft.daily_brief.lookback_hours)}
            onChange={(e) =>
              set("daily_brief", { lookback_hours: Number(e.target.value) || 0 })
            }
            className="w-28"
          />
        </Row>
      </Section>

      <Section title="Retrieval">
        <Row label="LLM intent classifier" hint="Slower, sometimes sharper routing">
          <Toggle
            checked={draft.retrieval.use_llm_intent}
            onChange={(v) => set("retrieval", { use_llm_intent: v })}
          />
        </Row>
        <Row label="Relevance judge" hint="Post-filter weak results (adds latency)">
          <Toggle
            checked={draft.retrieval.use_judge}
            onChange={(v) => set("retrieval", { use_judge: v })}
          />
        </Row>
        <Row label="Temporal scoring">
          <select
            value={draft.retrieval.temporal_scoring_mode || "additive"}
            onChange={(e) =>
              set("retrieval", { temporal_scoring_mode: e.target.value })
            }
            className="rounded-lg border border-border bg-surface px-3 py-2 text-sm outline-none focus:border-accent"
          >
            <option value="additive">additive</option>
            <option value="multiplicative">multiplicative</option>
          </select>
        </Row>
      </Section>

      <Section title="Mail">
        <Row
          label="Prune deleted mail"
          hint="On a full sync, remove from the knowledge base mail deleted on the server"
        >
          <Toggle
            checked={draft.mail.reconcile_deletions}
            onChange={(v) => set("mail", { reconcile_deletions: v })}
          />
        </Row>
      </Section>

      <Section title="Logging">
        <Row label="Log level">
          <select
            value={draft.logging.level || "info"}
            onChange={(e) => set("logging", { level: e.target.value })}
            className="rounded-lg border border-border bg-surface px-3 py-2 text-sm outline-none focus:border-accent"
          >
            {["debug", "info", "warn", "error"].map((l) => (
              <option key={l} value={l}>
                {l}
              </option>
            ))}
          </select>
        </Row>
      </Section>

      <NotificationsSection />
      <PermissionsSection />
    </Page>
  );
}

// MARK: - Native notification toggles (macOS UserDefaults via the prefs bridge)

const NOTIF_TOGGLES: { key: string; label: string; hint: string }[] = [
  { key: "notify.dailyBrief", label: "Daily brief", hint: "Notify when the morning brief is ready" },
  { key: "notify.priorityMail", label: "Important mail", hint: "Notify on actionable incoming mail" },
  { key: "notify.agendaAlerts", label: "Deadline alerts", hint: "Notify ahead of upcoming deadlines" },
];

function NotificationsSection() {
  const [state, setState] = useState<Record<string, boolean>>({});
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    if (!native.available) return;
    let cancelled = false;
    Promise.all(
      NOTIF_TOGGLES.map((t) => native.prefs.getBool(t.key).then((v) => [t.key, v] as const)),
    ).then((pairs) => {
      if (!cancelled) {
        setState(Object.fromEntries(pairs));
        setLoaded(true);
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  if (!native.available) {
    return (
      <Section title="Notifications">
        <Row label="Notifications" hint="Available in the Hygur desktop app.">
          <span className="text-[12.5px] text-faint">desktop only</span>
        </Row>
      </Section>
    );
  }

  const toggle = (key: string, v: boolean) => {
    setState((s) => ({ ...s, [key]: v }));
    void native.prefs.setBool(key, v);
  };

  return (
    <Section title="Notifications">
      {NOTIF_TOGGLES.map((t) => (
        <Row key={t.key} label={t.label} hint={t.hint}>
          <Toggle
            checked={Boolean(state[t.key])}
            disabled={!loaded}
            onChange={(v) => toggle(t.key, v)}
          />
        </Row>
      ))}
    </Section>
  );
}

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

function PermissionsSection() {
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
