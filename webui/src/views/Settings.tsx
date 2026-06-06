import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { native } from "../lib/native";
import { clearConnection, getConnection, isRemote, setConnection } from "../lib/connection";
import { isDesktop, getDesktopConfig, type DesktopConfig } from "../lib/desktop";
import { ModePicker } from "../onboarding/ModePicker";
import type {
  SidecarConfig,
  SidecarConfigPatch,
  TokenPeriodUsage,
  TokenPricing,
  TokenUsageResponse,
} from "../lib/types";
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

/** One labelled progress bar for the token-budget gauge. */
function GaugeRow({
  label,
  used,
  budget,
  pct,
  over,
  color,
}: {
  label: string;
  used: number;
  budget: number;
  pct: number;
  over: boolean;
  color: string;
}) {
  const f = (n: number) => n.toLocaleString("fr-FR");
  return (
    <div className="mb-2.5 last:mb-0">
      <div className="mb-1 flex items-baseline justify-between text-[12px]">
        <span className="font-medium">{label}</span>
        <span className={`tabular-nums ${over ? "font-semibold text-danger" : "text-muted"}`}>
          {f(used)} / {f(budget)}
          {over && " — over budget"}
        </span>
      </div>
      <div className="h-2 overflow-hidden rounded-full bg-border">
        <div className={`h-full rounded-full ${color}`} style={{ width: `${pct * 100}%` }} />
      </div>
    </div>
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

// MARK: - Connection (local sidecar vs remote endpoint + key)

function ConnectionSection() {
  const initial = getConnection();
  const [endpoint, setEndpoint] = useState(initial.endpoint);
  const [key, setKey] = useState(initial.key);
  const remote = isRemote();

  const connect = () => {
    setConnection(endpoint, key);
    // Reload so every query refetches against the new base origin.
    window.location.reload();
  };
  const disconnect = () => {
    clearConnection();
    window.location.reload();
  };

  return (
    <Section title="Connection">
      <Row
        label="Mode"
        hint={
          remote
            ? `Remote — ${initial.endpoint}`
            : "Local — served by the sidecar on this machine"
        }
      >
        {remote && (
          <Button variant="ghost" onClick={disconnect}>
            Disconnect
          </Button>
        )}
      </Row>
      <Row label="Server endpoint" hint="Empty = local sidecar. e.g. https://app.hygur.eu">
        <TextInput
          value={endpoint}
          spellCheck={false}
          autoCapitalize="off"
          placeholder="https://app.hygur.eu"
          onChange={(e) => setEndpoint(e.target.value)}
          className="w-64"
        />
      </Row>
      <Row label="API key" hint="Sent as X-Hygur-Token. Stored on this device only.">
        <TextInput
          type="password"
          value={key}
          spellCheck={false}
          autoCapitalize="off"
          onChange={(e) => setKey(e.target.value)}
          className="w-64"
        />
      </Row>
      <Row label="Apply">
        <Button onClick={connect} disabled={!endpoint.trim()}>
          {remote ? "Update & reconnect" : "Connect"}
        </Button>
      </Row>
    </Section>
  );
}

// MARK: - Engine mode (desktop only: local full engine vs cloud thin client)

function EngineModeSection() {
  const [cfg, setCfg] = useState<DesktopConfig | null>(null);
  const [picker, setPicker] = useState(false);

  const reload = () => {
    void getDesktopConfig()
      .then(setCfg)
      .catch(() => {});
  };
  useEffect(() => {
    if (isDesktop()) reload();
  }, []);

  if (!isDesktop()) return null;
  if (picker) {
    // A proxy-mode change reloads the page; otherwise we just close + refresh.
    return (
      <ModePicker
        onDone={() => {
          setPicker(false);
          reload();
        }}
        onCancel={() => setPicker(false)}
      />
    );
  }

  const cloud = cfg?.mode === "cloud";
  return (
    <Section title="Engine mode">
      <Row
        label="Mode"
        hint={
          cloud
            ? `Hygur Cloud — ${cfg?.server || "not set"}`
            : "Local — full engine on this Mac"
        }
      >
        <Button variant="ghost" onClick={() => setPicker(true)}>
          {cloud ? "Reconfigure" : "Switch…"}
        </Button>
      </Row>
    </Section>
  );
}

/** Billing panel (cloud customers). Reads the subscription status from the control
 *  plane and links to the Stripe customer portal. Self-hides when there's no
 *  billing account (the operator's hand-provisioned instance, self-host, or a
 *  browser with no device token) — its query just errors and the section vanishes. */
function BillingSection() {
  const q = useQuery({
    queryKey: ["billing-status"],
    queryFn: () => api.billingStatus(),
    retry: false,
  });
  if (q.isError || !q.data) return null;
  const b = q.data;
  const label =
    b.status === "active"
      ? "Active"
      : b.status === "trialing"
        ? "Trial"
        : b.status === "past_due"
          ? "Payment due"
          : b.status === "canceled"
            ? "Canceled"
            : b.status;
  const until = b.valid_until
    ? ` · ${b.active ? "renews" : "ends"} ${new Date(b.valid_until).toLocaleDateString()}`
    : "";
  return (
    <Section title="Billing">
      <Row label="Plan" hint={`Hygur Cloud — Personal${until}`}>
        <span className={`text-[13px] font-medium ${b.active ? "text-green-600" : "text-danger"}`}>
          {label}
        </span>
      </Row>
      {b.portal_url && (
        <Row label="Subscription" hint="Update payment method, download invoices, or cancel.">
          <a
            href={b.portal_url}
            target="_blank"
            rel="noopener noreferrer"
            className="rounded-md bg-accent px-3 py-1.5 text-[13px] font-medium text-white transition-opacity hover:opacity-90"
          >
            Manage
          </a>
        </Row>
      )}
    </Section>
  );
}

export function Settings() {
  const qc = useQueryClient();
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["config"],
    queryFn: () => api.config(),
  });

  const [draft, setDraft] = useState<SidecarConfig | null>(null);
  // The API key is never returned by GET (only api_key_set), so it has its own
  // write-only draft: empty = leave the stored key untouched.
  const [apiKey, setApiKey] = useState("");
  // Keep the editable draft in sync with the latest server config (re-syncs on
  // refetch, e.g. after a save). Done during render — React Query hands a fresh
  // `data` object per fetch, so this converges and avoids setState-in-effect.
  const [syncedFrom, setSyncedFrom] = useState<SidecarConfig | null>(null);
  if (data && data !== syncedFrom) {
    setSyncedFrom(data);
    setDraft(data);
  }

  const save = useMutation({
    mutationFn: (cfg: SidecarConfig) => {
      const patch: SidecarConfigPatch = {
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
      };
      // Managed cloud tenant: the AI runtime is operator-controlled — never send
      // it (the sidecar would reject it with 403 and break the whole save).
      if (!cfg.managed) {
        patch.lm_studio = {
          url: cfg.lm_studio.url,
          embedding_url: cfg.lm_studio.embedding_url,
          indexing_url: cfg.lm_studio.indexing_url,
          model_default: cfg.lm_studio.model_default,
          model_indexing: cfg.lm_studio.model_indexing,
          embedding_model: cfg.lm_studio.embedding_model,
          embedding_max_tokens: cfg.lm_studio.embedding_max_tokens,
          embedding_batch_size: cfg.lm_studio.embedding_batch_size,
          // Only send the key when the user typed one; empty leaves it untouched.
          ...(apiKey.trim() !== "" ? { api_key: apiKey.trim() } : {}),
        };
      }
      return api.patchConfig(patch);
    },
    onSuccess: () => {
      setApiKey("");
      qc.invalidateQueries({ queryKey: ["config"] });
    },
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

  // Typed nested setters keep the JSX terse. `section` is always one of the
  // object-valued config groups (never the `managed` flag), so the spread is safe.
  const set = <K extends keyof SidecarConfig>(
    section: K,
    patch: Partial<SidecarConfig[K]>,
  ) =>
    setDraft((d) =>
      d ? { ...d, [section]: { ...(d[section] as object), ...patch } } : d,
    );

  const dirty = JSON.stringify(draft) !== JSON.stringify(data) || apiKey.trim() !== "";

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

      {/* Connection (endpoint + device key) is an advanced/self-host control; in a
          managed cloud tenant the connection is handled for the user — hide it. */}
      {!draft.managed && <ConnectionSection />}
      <EngineModeSection />
      <BillingSection />

      {/* In a managed cloud tenant the AI runtime is operator-controlled and
          redacted server-side — hide the editor entirely. */}
      {!draft.managed && (
      <Section title="AI runtime">
        <Row label="Inference URL" hint="OpenAI-compatible chat endpoint (LM Studio, vLLM…)">
          <TextInput
            value={draft.lm_studio.url}
            onChange={(e) => set("lm_studio", { url: e.target.value })}
            className="w-64"
          />
        </Row>
        <Row
          label="API key"
          hint={
            draft.lm_studio.api_key_set
              ? "A key is saved. Type a new one to replace it, or leave empty to keep it."
              : "Only for hosted providers (Mistral, OpenAI…). Local runtimes need none."
          }
        >
          <TextInput
            type="password"
            value={apiKey}
            autoComplete="off"
            spellCheck={false}
            placeholder={draft.lm_studio.api_key_set ? "•••••••• (saved)" : "sk-…"}
            onChange={(e) => setApiKey(e.target.value)}
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
      )}

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

      {/* Retrieval tuning is a power-user/debug knob — hide on a managed tenant. */}
      {!draft.managed && (
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
      )}

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

      {/* Log level is a debug control — hide on a managed tenant. */}
      {!draft.managed && (
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
      )}

      <TokenUsageSection />
      {/* Local at-rest encryption + DB backup/restore are admin operations; on a
          managed cloud tenant the server owns them — hide for standard users. */}
      {!draft.managed && <EncryptionSection />}
      {!draft.managed && <BackupSection />}
      <NotificationsSection />
      <PermissionsSection />
    </Page>
  );
}

// MARK: - Local encryption

function EncryptionSection() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ["encryption-status"],
    queryFn: () => api.getEncryptionStatus(),
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [staged, setStaged] = useState(false);

  const enabled = data?.enabled ?? false;
  const envManaged = data?.env_managed ?? false;

  async function onEnable() {
    const ok = window.confirm(
      "Chiffrer la base locale ? La clé est stockée dans le keychain de l'OS. " +
        "La base est migrée au prochain démarrage de Hygur (l'originale est conservée). " +
        "Si tu perds la clé, les données sont irrécupérables. Continuer ?",
    );
    if (!ok) return;
    setError(null);
    setBusy(true);
    try {
      const r = await api.enableEncryption();
      if (r.restart_required) setStaged(true);
      qc.invalidateQueries({ queryKey: ["encryption-status"] });
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Section title="Local encryption">
      {error && (
        <div className="px-4 pt-3">
          <ErrorBanner message={error} />
        </div>
      )}
      <Row
        label={enabled ? "Database encrypted" : "Encrypt the local database"}
        hint={
          enabled
            ? envManaged
              ? "Encrypted at rest; the key is managed by the server."
              : "Encrypted at rest (SQLCipher); the key is in your OS keychain."
            : "Encrypt the knowledge base at rest. The key is stored in your OS keychain; migration runs on the next restart and keeps a backup."
        }
      >
        {isLoading ? (
          <span className="text-[12.5px] text-muted">…</span>
        ) : enabled ? (
          <span className="text-[12.5px] font-medium text-accent">Encrypted ✓</span>
        ) : (
          <Button onClick={() => void onEnable()} disabled={busy}>
            {busy ? "Enabling…" : "Encrypt…"}
          </Button>
        )}
      </Row>
      {staged && (
        <div className="px-4 py-3 text-[12.5px] text-accent">
          Encryption enabled — restart Hygur to migrate your database.
        </div>
      )}
    </Section>
  );
}

// MARK: - Database backup / restore

function BackupSection() {
  const [busy, setBusy] = useState<"backup" | "restore" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [staged, setStaged] = useState(false);
  const [savedPath, setSavedPath] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  async function onBackup() {
    setError(null);
    setSavedPath(null);
    setBusy("backup");
    try {
      // The desktop webview can't trigger a browser download, so locally the
      // sidecar (same machine) writes the file and reports where. A remote
      // server has no access to your disk → stream + save in the browser.
      if (isRemote()) {
        await api.downloadBackup();
      } else {
        const { path } = await api.saveBackupLocal();
        setSavedPath(path);
      }
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  }

  async function onRestoreFile(f: File | undefined) {
    if (!f) return;
    const ok = window.confirm(
      "Restaurer cette sauvegarde ? La base actuelle sera remplacée au prochain démarrage de Hygur (l'actuelle est conservée en .pre-restore.bak). Continuer ?",
    );
    if (!ok) {
      if (fileRef.current) fileRef.current.value = "";
      return;
    }
    setError(null);
    setStaged(false);
    setBusy("restore");
    try {
      await api.restoreBackup(f);
      setStaged(true);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
      if (fileRef.current) fileRef.current.value = "";
    }
  }

  return (
    <Section title="Database backup">
      {error && (
        <div className="px-4 pt-3">
          <ErrorBanner message={error} />
        </div>
      )}
      <Row
        label="Save a backup"
        hint={
          isRemote()
            ? "Downloads a consistent snapshot of your database (encryption preserved)."
            : "Writes a consistent snapshot (encryption preserved) to your Downloads folder."
        }
      >
        <Button onClick={() => void onBackup()} disabled={busy !== null}>
          {busy === "backup" ? "Preparing…" : isRemote() ? "Download" : "Save backup"}
        </Button>
      </Row>
      <Row
        label="Restore from a backup"
        hint="Replaces the database on the next restart; the current one is kept as a backup."
      >
        <input
          ref={fileRef}
          type="file"
          accept=".db"
          className="hidden"
          onChange={(e) => void onRestoreFile(e.target.files?.[0])}
        />
        <Button
          variant="ghost"
          onClick={() => fileRef.current?.click()}
          disabled={busy !== null}
        >
          {busy === "restore" ? "Uploading…" : "Restore…"}
        </Button>
      </Row>
      {savedPath && (
        <div className="px-4 py-3 text-[12.5px] text-accent">
          Backup saved to <span className="font-mono">{savedPath}</span>
        </div>
      )}
      {staged && (
        <div className="px-4 py-3 text-[12.5px] text-accent">
          Backup staged — restart Hygur to apply it.
        </div>
      )}
    </Section>
  );
}

// MARK: - Token usage & cost

// A locale-tolerant decimal input. `type="number"` rejects comma decimals
// (French keyboards) and a controlled numeric value clobbers half-typed states
// like "0,"; so this is an uncontrolled text field that accepts both "," and
// "." and reports the parsed number upward. Seeded once at mount.
function PriceField({
  value,
  onChange,
}: {
  value: number;
  onChange: (n: number) => void;
}) {
  return (
    <input
      type="text"
      inputMode="decimal"
      defaultValue={value ? String(value).replace(".", ",") : ""}
      placeholder="0"
      onChange={(e) => {
        const n = parseFloat(e.target.value.replace(",", "."));
        onChange(Number.isFinite(n) ? n : 0);
      }}
      className="w-28 rounded-lg border border-border bg-surface px-2 py-1 text-right text-sm tabular-nums outline-none focus:border-accent"
    />
  );
}

function TokenUsageSection() {
  const qc = useQueryClient();
  const { data } = useQuery({
    queryKey: ["usage"],
    queryFn: api.getTokenUsage,
  });
  // Local draft of the price fields, seeded once from the server values.
  // Seeding during render (not in an effect) is React's sanctioned pattern for
  // deriving initial state from async data — it converges once price is set.
  const [price, setPrice] = useState<TokenPricing | null>(null);
  if (price === null && data?.pricing) {
    setPrice(data.pricing);
  }

  const save = useMutation({
    mutationFn: (p: TokenPricing) => api.setTokenPricing(p),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["usage"] }),
  });

  if (!data || !price) {
    return (
      <Section title="Token usage & cost">
        <Row label="Usage">
          <span className="text-[12.5px] text-faint">loading…</span>
        </Row>
      </Section>
    );
  }

  const cur = price.currency || data.currency || "€";
  const fmtTok = (n: number) => n.toLocaleString("fr-FR");
  const money = (n: number) => `${n.toFixed(n > 0 && n < 1 ? 4 : 2)} ${cur}`;
  const chatCost = (p: TokenPeriodUsage) =>
    (p.chat_in / 1e6) * price.chat_in_per_1m +
    (p.chat_out / 1e6) * price.chat_out_per_1m;
  const ingestCost = (p: TokenPeriodUsage) =>
    ((p.embedding + p.indexing) / 1e6) * price.ingest_per_1m;

  const periods: { key: keyof TokenUsageResponse["periods"]; label: string }[] = [
    { key: "today", label: "Today" },
    { key: "this_week", label: "This week" },
    { key: "this_month", label: "This month" },
  ];
  const dirty = JSON.stringify(price) !== JSON.stringify(data.pricing);

  // Monthly inference caps (hardcoded) and the weekly slice we watch against.
  // Both directions sit in one weekly gauge so we can judge whether 8M IN / 2M
  // OUT per month leaves enough gross margin at the current price.
  const MONTHLY_IN = 8_000_000;
  const MONTHLY_OUT = 2_000_000;
  const weekBudget = (monthly: number) => Math.round((monthly * 7) / 30);
  const wk = data.periods.this_week;
  const gauge = (used: number, budget: number) => {
    const pct = budget > 0 ? Math.min(used / budget, 1) : 0;
    const over = budget > 0 && used > budget;
    const color = over ? "bg-danger" : pct >= 0.75 ? "bg-amber-500" : "bg-green-500";
    return { pct, over, color };
  };
  const inG = gauge(wk.total_in, weekBudget(MONTHLY_IN));
  const outG = gauge(wk.total_out, weekBudget(MONTHLY_OUT));

  return (
    <Section title="Token usage & cost">
      <div className="px-4 pb-1 pt-3">
        <div className="mb-1 flex items-baseline justify-between">
          <span className="text-[12px] font-medium">This week's budget</span>
          <span className="text-[11px] text-faint">
            caps: {fmtTok(MONTHLY_IN)} IN · {fmtTok(MONTHLY_OUT)} OUT / month
          </span>
        </div>
        <GaugeRow label="Input" used={wk.total_in} budget={weekBudget(MONTHLY_IN)} {...inG} />
        <GaugeRow label="Output" used={wk.total_out} budget={weekBudget(MONTHLY_OUT)} {...outG} />
      </div>
      <div className="overflow-x-auto px-4 py-3">
        <table className="w-full text-[13px]">
          <thead>
            <tr className="text-[11px] uppercase tracking-wide text-faint">
              <th className="pb-2 text-left font-medium">Period</th>
              <th className="pb-2 text-right font-medium">Chat IN</th>
              <th className="pb-2 text-right font-medium">Chat OUT</th>
              <th className="pb-2 text-right font-medium">Embeddings</th>
              <th className="pb-2 text-right font-medium">Indexing</th>
              <th className="pb-2 text-right font-medium">Cost</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {periods.map(({ key, label }) => {
              const p = data.periods[key];
              return (
                <tr key={key}>
                  <td className="py-1.5">{label}</td>
                  <td className="py-1.5 text-right tabular-nums">{fmtTok(p.chat_in)}</td>
                  <td className="py-1.5 text-right tabular-nums">{fmtTok(p.chat_out)}</td>
                  <td className="py-1.5 text-right tabular-nums">{fmtTok(p.embedding)}</td>
                  <td className="py-1.5 text-right tabular-nums">{fmtTok(p.indexing)}</td>
                  <td className="py-1.5 text-right font-medium tabular-nums">
                    {money(chatCost(p) + ingestCost(p))}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      <Row label="Chat IN price" hint={`Per 1M tokens (${cur})`}>
        <PriceField
          value={price.chat_in_per_1m}
          onChange={(n) => setPrice({ ...price, chat_in_per_1m: n })}
        />
      </Row>
      <Row label="Chat OUT price" hint={`Per 1M tokens (${cur})`}>
        <PriceField
          value={price.chat_out_per_1m}
          onChange={(n) => setPrice({ ...price, chat_out_per_1m: n })}
        />
      </Row>
      <Row
        label="Embeddings & indexing price"
        hint={`Per 1M tokens (${cur}) — applied to both`}
      >
        <PriceField
          value={price.ingest_per_1m}
          onChange={(n) => setPrice({ ...price, ingest_per_1m: n })}
        />
      </Row>
      <div className="flex items-center justify-end gap-3 px-4 py-3">
        {save.error && (
          <span className="text-[12.5px] text-danger">
            {(save.error as Error).message}
          </span>
        )}
        <Button
          onClick={() => save.mutate(price)}
          disabled={!dirty || save.isPending}
        >
          {save.isPending ? "Saving…" : "Save prices"}
        </Button>
      </div>
    </Section>
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
