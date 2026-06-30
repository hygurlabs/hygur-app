import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, linkDeviceCode } from "../lib/api";
import { QRCodeSVG } from "qrcode.react";
import { native } from "../lib/native";
import { clearConnection, getConnection, isRemote, setConnection } from "../lib/connection";
import { isDesktop, getDesktopConfig, type DesktopConfig } from "../lib/desktop";
import { enablePush, disablePush, pushSupported } from "../lib/push";
import { ModePicker } from "../onboarding/ModePicker";
import { PasskeyBanner } from "../components/PasskeyNudge";
import { usePasskeyCount, useAddPasskey } from "../lib/usePasskey";
import { passkeysSupported } from "../lib/passkey";
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
    <div className="flex flex-col items-start gap-2 px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
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

// MARK: - Security (passkey) — managed cloud only

// Lets a user who enrolled by code and skipped passkey setup add one later, so
// they're no longer locked to the enrolling browser. The red banner at the top of
// Settings (and the global one) covers the "none yet" case; this is the durable
// home for it + adding more.
function PasskeySecuritySection() {
  const { data: count } = usePasskeyCount();
  const { add, busy, error, ready } = useAddPasskey();
  const supported = passkeysSupported();
  const has = (count ?? 0) > 0;
  return (
    <Section title="Security">
      <Row
        label={has ? "Passkey active" : "Passkey"}
        hint={
          supported
            ? "Add a passkey (Face ID, Touch ID, or your device PIN) to sign in from any device — not just this browser."
            : "Passkeys aren't supported on this browser."
        }
      >
        {!supported ? (
          <span className="text-[12.5px] text-faint">unsupported</span>
        ) : (
          <Button
            variant={has ? "ghost" : undefined}
            onClick={() => void add()}
            disabled={busy || !ready}
          >
            {busy ? "Adding…" : has ? "Add another" : "Add a passkey"}
          </Button>
        )}
      </Row>
      {error && (
        <div className="px-4 pb-3">
          <ErrorBanner message={error} />
        </div>
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

  // Memoised: only recomputes when the draft, the server config, or the
  // write-only API key change — not on every unrelated re-render (e.g. the Save
  // button toggling its pending state). Declared above the early return so the
  // hook order stays unconditional (rules of hooks).
  const dirty = useMemo(
    () => JSON.stringify(draft) !== JSON.stringify(data) || apiKey.trim() !== "",
    [draft, data, apiKey],
  );

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

      <PasskeyBanner variant="settings" />

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

      <TokenUsageSection managed={!!draft.managed} />
      {/* Managed cloud: subscription management + invoices + cancellation (which
          drives account deletion via Stripe → the reaper). */}
      {draft.managed && (
        <Section title="Subscription & data">
          {draft.instance_name && (
            <Row label="Instance name" hint="You sign in with this name and your passkey. Keep it handy in case you ever lose access.">
              <span className="font-mono text-[13px] text-text">{draft.instance_name}</span>
            </Row>
          )}
          {draft.billing_portal_url && (
            <Row label="Billing" hint="Manage your subscription, payment method, and monthly invoices.">
              <a
                href={draft.billing_portal_url}
                target="_blank"
                rel="noopener noreferrer"
                className="rounded-lg border border-border px-3 py-1.5 text-[13px] font-medium hover:bg-surface2"
              >
                Open billing portal
              </a>
            </Row>
          )}
          <DeleteSpaceRow
            portalURL={draft.billing_portal_url ?? ""}
            instanceName={draft.instance_name ?? ""}
          />
        </Section>
      )}
      {draft.managed && <PasskeySecuritySection />}
      {/* Local at-rest encryption + DB backup/restore are admin operations; on a
          managed cloud tenant the server owns them — hide for standard users. */}
      {!draft.managed && <EncryptionSection />}
      {!draft.managed && <BackupSection />}
      {/* Encrypted data export (GDPR portability) — available everywhere, incl.
          managed cloud tenants where it's the user's own way to take their data. */}
      <ExportSection />
      <NotificationsSection vapidPublicKey={draft.vapid_public_key ?? ""} />
      {draft.managed && <ConnectPhoneSection />}
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
      "Encrypt the local database? The key is stored in the OS keychain. " +
        "The database is migrated on Hygur's next start (the original is kept). " +
        "If you lose the key, the data is unrecoverable. Continue?",
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
      "Restore this backup? The current database will be replaced on Hygur's next start (the current one is kept as .pre-restore.bak). Continue?",
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

// MARK: - Encrypted data export (GDPR portability)

// Exports the notes & briefs Hygur produced, in an archive encrypted with a
// passphrase YOU choose (never sent anywhere reusable — it's the zip key). The
// server streams it directly; nothing is stored server-side. Decrypt with any
// tool, e.g. `openssl enc -d -aes-256-cbc -pbkdf2 -pass pass:… -in file.zip.enc`.
function ExportSection() {
  const [passphrase, setPassphrase] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  async function onExport() {
    setError(null);
    setDone(false);
    setBusy(true);
    try {
      await api.exportData(passphrase);
      setDone(true);
      setPassphrase("");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  const tooShort = passphrase.length > 0 && passphrase.length < 8;

  return (
    <Section title="Export your data">
      {error && (
        <div className="px-4 pt-3">
          <ErrorBanner message={error} />
        </div>
      )}
      <Row
        label="Download an encrypted export"
        hint="A zip of the notes and briefs Hygur produced (your emails and files stay on your device). Encrypted with the passphrase you set below — keep it safe, it's the only way to open the archive."
      >
        <div className="flex flex-col items-end gap-1.5">
          <input
            type="password"
            value={passphrase}
            onChange={(e) => setPassphrase(e.target.value)}
            placeholder="Passphrase (min. 8 chars)"
            autoComplete="new-password"
            className="w-56 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm outline-none transition-colors focus:border-accent"
          />
          <Button
            onClick={() => void onExport()}
            disabled={busy || passphrase.length < 8}
          >
            {busy ? "Preparing…" : "Export"}
          </Button>
        </div>
      </Row>
      {tooShort && (
        <div className="px-4 pb-3 text-[12.5px] text-muted">
          Use at least 8 characters.
        </div>
      )}
      {done && (
        <div className="px-4 py-3 text-[12.5px] text-accent">
          Export downloaded. Decrypt it with your passphrase (see the hint).
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

// Type-to-confirm deletion gate (AWS-style): the "Cancel & delete" link to the
// Stripe portal only activates once the user types their exact space name.
function DeleteSpaceRow({ portalURL, instanceName }: { portalURL: string; instanceName: string }) {
  const [confirm, setConfirm] = useState("");
  const expected = instanceName || "DELETE";
  const matched = confirm.trim() === expected;
  // Cancelling via the Stripe billing portal IS the deletion (per the Terms:
  // access runs to the end of the paid period, then crypto-shred + purge). There
  // is no email path — when the portal isn't configured yet, the action is simply
  // not yet available.
  const canCancel = portalURL !== "";
  return (
    <div className="px-4 py-3">
      <p className="text-[14px]">Delete my space</p>
      <p className="mt-0.5 text-[12.5px] text-muted">
        Cancelling ends your subscription. Your access continues until the end of the paid period
        (no refund). When it ends, your encryption key is destroyed immediately and the space is
        permanently purged after 30 days. This cannot be undone.
      </p>
      <p className="mt-2 text-[12.5px] text-muted">
        Want a copy? Export your data before you cancel.
      </p>
      {instanceName ? (
        <p className="mt-2 text-[12.5px] text-muted">
          To confirm, type your space name{" "}
          <code className="select-all rounded bg-surface2 px-1.5 py-0.5 font-mono text-[12px] text-text">
            {instanceName}
          </code>{" "}
          below.
        </p>
      ) : (
        <p className="mt-2 text-[12.5px] text-muted">
          To confirm, type{" "}
          <code className="select-all rounded bg-surface2 px-1.5 py-0.5 font-mono text-[12px] text-text">
            DELETE
          </code>{" "}
          below.
        </p>
      )}
      <div className="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center">
        <input
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          placeholder={`Type "${expected}" to confirm`}
          spellCheck={false}
          autoCapitalize="off"
          className="flex-1 rounded-lg border border-border bg-surface px-3 py-1.5 text-[13px] outline-none focus:border-danger"
        />
        {!canCancel ? (
          <span className="shrink-0 cursor-not-allowed rounded-lg border border-border px-3 py-1.5 text-center text-[13px] font-medium text-faint">
            Available at launch
          </span>
        ) : matched ? (
          <a
            href={portalURL}
            target="_blank"
            rel="noopener noreferrer"
            className="shrink-0 rounded-lg border border-danger/50 bg-danger/10 px-3 py-1.5 text-center text-[13px] font-medium text-danger hover:bg-danger/20"
          >
            Cancel &amp; delete
          </a>
        ) : (
          <span className="shrink-0 cursor-not-allowed rounded-lg border border-border px-3 py-1.5 text-center text-[13px] font-medium text-faint">
            Cancel &amp; delete
          </span>
        )}
      </div>
    </div>
  );
}

function TokenUsageSection({ managed }: { managed: boolean }) {
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

  // Managed cloud tenant: a single merged consumption bar (IN+OUT), no raw token
  // counters, no prices, no per-category table — like Claude's usage panel. The
  // full breakdown stays for self-hosted operators.
  if (managed) {
    const usedWk = wk.total_in + wk.total_out;
    const budgetWk = weekBudget(MONTHLY_IN + MONTHLY_OUT);
    const g = gauge(usedWk, budgetWk);
    const pct = Math.round(g.pct * 100);
    return (
      <Section title="Usage">
        <div className="px-4 pb-4 pt-3">
          <div className="mb-1.5 flex items-baseline justify-between text-[12px]">
            <span className="font-medium">This week</span>
            <span className={`tabular-nums ${g.over ? "font-semibold text-danger" : "text-muted"}`}>
              {pct}% used
            </span>
          </div>
          <div className="h-2 overflow-hidden rounded-full bg-border">
            <div className={`h-full rounded-full ${g.color}`} style={{ width: `${g.pct * 100}%` }} />
          </div>
          <p className="mt-2 text-[11.5px] text-faint">Resets weekly. Your plan covers normal daily use.</p>
        </div>
      </Section>
    );
  }

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

// WebPushRow enrols the browser for Web Push (notifications when the tab is
// closed). Shown on the web shell where there's no native notification bridge.
function WebPushRow({ vapidPublicKey }: { vapidPublicKey: string }) {
  const [on, setOn] = useState(false);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");

  useEffect(() => {
    if (!pushSupported()) return;
    void navigator.serviceWorker
      .getRegistration()
      .then((reg) => reg?.pushManager.getSubscription())
      .then((s) => setOn(!!s))
      .catch(() => {});
  }, []);

  const change = async (v: boolean) => {
    setBusy(true);
    setMsg("");
    try {
      if (v) {
        const ok = await enablePush(vapidPublicKey);
        setOn(ok);
        if (!ok) setMsg("Permission denied or unsupported on this browser.");
      } else {
        await disablePush();
        setOn(false);
      }
    } catch (e) {
      setMsg((e as Error).message);
    }
    setBusy(false);
  };

  const test = async () => {
    setBusy(true);
    setMsg("");
    try {
      const r = await api.testPush();
      setMsg(`Sent to ${r.sent} device(s) — check your notifications.`);
    } catch (e) {
      setMsg((e as Error).message);
    }
    setBusy(false);
  };

  return (
    <>
      <Row label="Browser notifications" hint={msg || "Get your daily brief even when the tab is closed."}>
        <Toggle checked={on} disabled={busy} onChange={(v) => void change(v)} />
      </Row>
      {on && (
        <Row label="Test" hint="Send a test notification to this browser.">
          <Button variant="ghost" onClick={() => void test()} disabled={busy}>
            Send test
          </Button>
        </Row>
      )}
    </>
  );
}

// ConnectPhoneSection shows a QR that deep-links a phone to this space with a
// one-time code (WhatsApp-Web style): scanning signs the phone in, no typing.
function ConnectPhoneSection() {
  const [url, setUrl] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const generate = async () => {
    setBusy(true);
    setErr("");
    try {
      const { code, slug } = await linkDeviceCode();
      setUrl(`https://cloud.hygur.ai/${slug}?code=${encodeURIComponent(code)}`);
    } catch (e) {
      setErr((e as Error).message);
    }
    setBusy(false);
  };
  return (
    <Section title="Connect a phone">
      <Row
        label="Add a device"
        hint={
          err ||
          "Scan with your phone's camera to sign in there — no typing. The code works once and expires in 10 minutes."
        }
      >
        <Button variant="ghost" onClick={() => void generate()} disabled={busy}>
          {busy ? "Generating…" : url ? "New code" : "Show QR code"}
        </Button>
      </Row>
      {url && (
        <div className="flex flex-col items-center gap-3 px-4 py-5">
          <div className="rounded-xl bg-white p-3 shadow-sm">
            <QRCodeSVG value={url} size={180} />
          </div>
          <p className="max-w-full break-all text-center text-[11px] text-faint">{url}</p>
        </div>
      )}
    </Section>
  );
}

function NotificationsSection({ vapidPublicKey }: { vapidPublicKey: string }) {
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
    // Web shell: no native bridge, but the browser can do Web Push when the
    // tenant has a VAPID key configured.
    if (pushSupported() && vapidPublicKey) {
      return (
        <Section title="Notifications">
          <WebPushRow vapidPublicKey={vapidPublicKey} />
        </Section>
      );
    }
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

