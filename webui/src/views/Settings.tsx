import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useToast } from "../lib/toast";
import type { SidecarConfig, SidecarConfigPatch } from "../lib/types";
import { PasskeyBanner } from "../components/PasskeyNudge";
import {
  Button,
  ErrorBanner,
  Page,
  PageHeader,
  Skeleton,
  TextInput,
} from "../components/ui";
import { Row, Section, Toggle } from "./settings/common";
import { ConnectionSection } from "./settings/ConnectionSection";
import { EngineModeSection } from "./settings/EngineModeSection";
import { BillingSection } from "./settings/BillingSection";
import { PasskeySecuritySection } from "./settings/PasskeySecuritySection";
import { EncryptionSection } from "./settings/EncryptionSection";
import { BackupSection } from "./settings/BackupSection";
import { ExportSection } from "./settings/ExportSection";
import { TokenUsageSection } from "./settings/TokenUsageSection";
import { DeleteSpaceRow } from "./settings/DeleteSpaceRow";
import { NotificationsSection } from "./settings/NotificationsSection";
import { ConnectPhoneSection } from "./settings/ConnectPhoneSection";
import { PermissionsSection } from "./settings/PermissionsSection";

export function Settings() {
  const qc = useQueryClient();
  const toast = useToast();
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
      toast.success("Settings saved.");
    },
    onError: (e) => toast.error(`Couldn't save settings: ${(e as Error).message}`),
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
