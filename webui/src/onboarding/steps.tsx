import { useEffect, useState, type ReactNode } from "react";
import {
  Bell,
  Calendar,
  CalendarDays,
  CheckCircle2,
  Cpu,
  FolderOpen,
  Mail,
  Mic,
  PlugZap,
  ShieldCheck,
  Sparkles,
} from "lucide-react";
import { api } from "../lib/api";
import { native } from "../lib/native";
import type { SidecarConfig } from "../lib/types";
import { Button, TextInput } from "../components/ui";

/** Navigation/lifecycle handed to every step by the wizard container. */
export interface StepContext {
  /** Advance to the next step (or finish on the last one). */
  next: () => void;
  /** Finish onboarding now. Pass a hash route (e.g. "#/connectors") to land
   *  the user there instead of the default chat view. */
  complete: (route?: string) => void;
}

// MARK: - Shared layout

function StepHeader({
  icon,
  title,
  subtitle,
}: {
  icon: ReactNode;
  title: string;
  subtitle?: string;
}) {
  return (
    <div className="mb-7 flex flex-col items-center text-center">
      <div className="mb-4 flex size-14 items-center justify-center rounded-2xl bg-accent-weak text-accent">
        {icon}
      </div>
      <h1 className="font-display text-[26px] font-semibold leading-tight tracking-tight">
        {title}
      </h1>
      {subtitle && (
        <p className="mt-2 max-w-[46ch] text-[13.5px] leading-relaxed text-muted">
          {subtitle}
        </p>
      )}
    </div>
  );
}

function InfoRow({
  icon,
  title,
  detail,
  trailing,
}: {
  icon: ReactNode;
  title: string;
  detail: string;
  trailing?: ReactNode;
}) {
  return (
    <div className="flex items-start gap-3.5 px-4 py-3.5">
      <div className="mt-0.5 shrink-0 text-accent">{icon}</div>
      <div className="min-w-0 flex-1">
        <p className="text-[14px] font-medium">{title}</p>
        <p className="mt-0.5 text-[12.5px] leading-relaxed text-muted">{detail}</p>
      </div>
      {trailing && <div className="shrink-0 self-center">{trailing}</div>}
    </div>
  );
}

function Card({ children }: { children: ReactNode }) {
  return (
    <div className="flex flex-col divide-y divide-border rounded-xl border border-border bg-surface">
      {children}
    </div>
  );
}

// MARK: - Welcome

export function StepWelcome() {
  return (
    <div>
      <StepHeader
        icon={<Sparkles size={26} strokeWidth={1.6} />}
        title="Welcome to Hygur"
        subtitle="Your local digital twin — a private memory of your documents, mail and notes, powered by your own LLM. Everything runs on this Mac. No cloud, no account."
      />
      <Card>
        <InfoRow
          icon={<ShieldCheck size={18} strokeWidth={1.7} />}
          title="Private by design"
          detail="Your data never leaves your machine. The app talks only to a local sidecar and the AI runtime you point it at."
        />
        <InfoRow
          icon={<Cpu size={18} strokeWidth={1.7} />}
          title="Your model, your rules"
          detail="Bring any OpenAI-compatible runtime — LM Studio, Ollama, vLLM or llama.cpp."
        />
        <InfoRow
          icon={<PlugZap size={18} strokeWidth={1.7} />}
          title="Connect what matters"
          detail="Index mail, calendar and folders so Hygur can answer with your own context."
        />
      </Card>
    </div>
  );
}

// MARK: - macOS permissions

const PERM_STATUS_LABEL: Record<string, string> = {
  authorized: "Allowed",
  denied: "Denied",
  restricted: "Restricted",
  writeOnly: "Write only",
  notDetermined: "Not set",
  unknown: "Unknown",
};

function StatusDot({ status }: { status?: string }) {
  const color =
    status === "authorized" || status === "writeOnly"
      ? "var(--accent)"
      : status === "denied" || status === "restricted"
        ? "var(--danger)"
        : "var(--faint)";
  return (
    <span className="inline-flex items-center gap-2 text-[12.5px] text-muted">
      <span aria-hidden className="size-2 rounded-full" style={{ background: color }} />
      {PERM_STATUS_LABEL[status ?? "unknown"] ?? "Unknown"}
    </span>
  );
}

export function StepPermissions() {
  const [status, setStatus] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<string | null>(null);

  const refresh = () => {
    if (!native.available) return;
    void native.perms.status().then(setStatus);
  };

  useEffect(refresh, []);

  if (!native.available) {
    return (
      <div>
        <StepHeader
          icon={<ShieldCheck size={26} strokeWidth={1.6} />}
          title="macOS permissions"
          subtitle="When you run Hygur as the desktop app, you can grant Calendar, Notifications and Microphone access here. In the browser these are managed by your browser."
        />
      </div>
    );
  }

  const grantCalendar = async () => {
    setBusy("calendar");
    await native.calendar.authorize();
    refresh();
    setBusy(null);
  };

  const grantNotifications = async () => {
    setBusy("notifications");
    // Enabling the daily-brief category triggers the system notification
    // prompt (the bridge requests authorization for any "notify.*" toggle).
    await native.prefs.setBool("notify.dailyBrief", true);
    refresh();
    setBusy(null);
  };

  const isOn = (k: string) => status[k] === "authorized" || status[k] === "writeOnly";

  return (
    <div>
      <StepHeader
        icon={<ShieldCheck size={26} strokeWidth={1.6} />}
        title="macOS permissions"
        subtitle="Hygur asks for these only when you use the matching feature. Grant them now or later — you can change any of them in System Settings at any time."
      />
      <Card>
        <InfoRow
          icon={<Calendar size={18} strokeWidth={1.7} />}
          title="Calendar"
          detail="Surfaces upcoming events in your agenda and meeting briefings. Creating events always asks you to confirm first."
          trailing={
            isOn("calendar") ? (
              <StatusDot status={status.calendar} />
            ) : (
              <Button variant="ghost" onClick={grantCalendar} disabled={busy === "calendar"}>
                {busy === "calendar" ? "Asking…" : "Allow"}
              </Button>
            )
          }
        />
        <InfoRow
          icon={<Bell size={18} strokeWidth={1.7} />}
          title="Notifications"
          detail="Daily briefs and priority alerts. Opt-in — nothing fires until you turn a category on."
          trailing={
            isOn("notifications") ? (
              <StatusDot status={status.notifications} />
            ) : (
              <Button
                variant="ghost"
                onClick={grantNotifications}
                disabled={busy === "notifications"}
              >
                {busy === "notifications" ? "Asking…" : "Enable"}
              </Button>
            )
          }
        />
        <InfoRow
          icon={<Mic size={18} strokeWidth={1.7} />}
          title="Microphone & speech"
          detail="On-device voice input in chat. Requested the first time you use the mic — transcription stays on this Mac."
          trailing={<StatusDot status={status.microphone} />}
        />
      </Card>
      <button
        onClick={() =>
          void native.openExternal(
            "x-apple.systempreferences:com.apple.preference.security?Privacy",
          )
        }
        className="mt-4 text-[12.5px] text-muted underline-offset-2 hover:text-accent hover:underline"
      >
        Open System Settings → Privacy & Security
      </button>
    </div>
  );
}

// MARK: - Connect AI model

type ModelStatus =
  | { kind: "idle" }
  | { kind: "saving" }
  | { kind: "checking" }
  | { kind: "connected" }
  | { kind: "warn"; message: string }
  | { kind: "error"; message: string };

const sleep = (ms: number) => new Promise((r) => window.setTimeout(r, ms));

/** Polls the public /health endpoint until the inference endpoint reports
 *  connected, or the budget runs out. Tolerates the transient failures while
 *  the sidecar restarts to pick up the new config. */
async function waitForInference(timeoutMs: number): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const r = await fetch("/health", { cache: "no-store" });
      if (r.ok) {
        const h = (await r.json()) as { inference?: string };
        if (h.inference === "connected") return true;
      }
    } catch {
      /* sidecar is restarting — keep polling */
    }
    await sleep(700);
  }
  return false;
}

export function StepModel({ ctx }: { ctx: StepContext }) {
  const [url, setUrl] = useState("http://localhost:1234");
  const [chatModel, setChatModel] = useState("");
  const [embeddingModel, setEmbeddingModel] = useState("");
  const [embeddingUrl, setEmbeddingUrl] = useState("");
  const [status, setStatus] = useState<ModelStatus>({ kind: "idle" });

  useEffect(() => {
    let cancelled = false;
    void api
      .config()
      .then((cfg: SidecarConfig) => {
        if (cancelled) return;
        if (cfg.lm_studio.url) setUrl(cfg.lm_studio.url);
        setChatModel(cfg.lm_studio.model_default);
        setEmbeddingModel(cfg.lm_studio.embedding_model);
        setEmbeddingUrl(cfg.lm_studio.embedding_url);
      })
      .catch(() => {
        /* sidecar unreachable (e.g. browser dev) — keep the defaults */
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const busy = status.kind === "saving" || status.kind === "checking";
  const canSave = url.trim() !== "" && chatModel.trim() !== "" && !busy;

  const testAndContinue = async () => {
    setStatus({ kind: "saving" });
    try {
      await api.patchConfig({
        lm_studio: {
          url: url.trim(),
          model_default: chatModel.trim(),
          embedding_model: embeddingModel.trim(),
          embedding_url: embeddingUrl.trim(),
        },
      });
    } catch (e) {
      setStatus({
        kind: "error",
        message: `Couldn't save the configuration: ${(e as Error).message}`,
      });
      return;
    }

    // The sidecar restarts itself ~750ms after the PATCH to apply the new
    // endpoint. Give it a head start, then poll /health for a live inference
    // connection so "Connected" reflects the new config, not the old process.
    setStatus({ kind: "checking" });
    await sleep(1300);
    const ok = await waitForInference(15_000);
    if (ok) {
      setStatus({ kind: "connected" });
      await sleep(600);
      ctx.next();
    } else {
      setStatus({
        kind: "warn",
        message:
          "Saved, but the model didn't answer yet. You can keep going and fix this later in Settings.",
      });
    }
  };

  return (
    <div>
      <StepHeader
        icon={<Cpu size={26} strokeWidth={1.6} />}
        title="Connect your AI model"
        subtitle="Point Hygur at a local OpenAI-compatible runtime — LM Studio, Ollama, vLLM or llama.cpp. These settings live in the sidecar and can be changed anytime."
      />

      <div className="flex flex-col gap-4">
        <Field label="Inference URL" hint="The OpenAI-compatible chat endpoint, e.g. http://localhost:1234">
          <TextInput
            value={url}
            spellCheck={false}
            autoCapitalize="off"
            onChange={(e) => setUrl(e.target.value)}
            placeholder="http://localhost:1234"
          />
        </Field>
        <Field label="Chat model">
          <TextInput
            value={chatModel}
            spellCheck={false}
            autoCapitalize="off"
            onChange={(e) => setChatModel(e.target.value)}
            placeholder="e.g. llama-3.1-8b-instruct"
          />
        </Field>
        <Field
          label="Embedding model"
          hint="Used to index your documents for semantic search. Leave empty to set later."
        >
          <TextInput
            value={embeddingModel}
            spellCheck={false}
            autoCapitalize="off"
            onChange={(e) => setEmbeddingModel(e.target.value)}
            placeholder="e.g. nomic-embed-text"
          />
        </Field>
        <Field label="Embedding URL" hint="Optional — leave empty to reuse the inference URL.">
          <TextInput
            value={embeddingUrl}
            spellCheck={false}
            autoCapitalize="off"
            onChange={(e) => setEmbeddingUrl(e.target.value)}
            placeholder="(same as inference URL)"
          />
        </Field>

        <ModelStatusBanner status={status} />

        <div className="flex justify-center pt-1">
          <Button onClick={testAndContinue} disabled={!canSave}>
            {status.kind === "saving"
              ? "Saving…"
              : status.kind === "checking"
                ? "Connecting…"
                : "Test & continue"}
          </Button>
        </div>
      </div>
    </div>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <label className="block">
      <span className="mb-1.5 block text-[13px] font-medium">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-[12px] text-faint">{hint}</span>}
    </label>
  );
}

function ModelStatusBanner({ status }: { status: ModelStatus }) {
  if (status.kind === "idle") return null;
  const tone =
    status.kind === "error"
      ? "border-danger/40 bg-danger/5 text-danger"
      : status.kind === "warn"
        ? "border-danger/30 bg-danger/5 text-danger"
        : "border-border bg-surface text-muted";
  const text =
    status.kind === "saving"
      ? "Saving configuration…"
      : status.kind === "checking"
        ? "Restarting the runtime and checking the connection…"
        : status.kind === "connected"
          ? "Connected. Continuing…"
          : status.message;
  return (
    <div className={`rounded-lg border px-3.5 py-2.5 text-[12.5px] ${tone}`}>{text}</div>
  );
}

// MARK: - Accounts (skippable)

export function StepAccounts({ ctx }: { ctx: StepContext }) {
  return (
    <div>
      <StepHeader
        icon={<PlugZap size={26} strokeWidth={1.6} />}
        title="Connect your accounts"
        subtitle="Hygur answers with your own context once you connect a few sources. This is optional — you can set it up now or anytime from the Connectors tab."
      />
      <Card>
        <InfoRow
          icon={<Mail size={18} strokeWidth={1.7} />}
          title="Mail"
          detail="Gmail, any IMAP provider, or the macOS Mail.app (read-only) — indexed locally."
        />
        <InfoRow
          icon={<CalendarDays size={18} strokeWidth={1.7} />}
          title="Calendar"
          detail="Your macOS calendars feed the agenda and meeting briefings."
        />
        <InfoRow
          icon={<FolderOpen size={18} strokeWidth={1.7} />}
          title="Files & folders"
          detail="Point Hygur at a folder of PDFs, notes and documents to make them searchable."
        />
      </Card>
      <div className="mt-5 flex justify-center">
        <Button onClick={() => ctx.complete("#/connectors")}>Set up connectors →</Button>
      </div>
    </div>
  );
}

// MARK: - Notifications

const NOTIF_TOGGLES: { key: string; label: string; hint: string }[] = [
  { key: "notify.dailyBrief", label: "Daily brief", hint: "When your morning digest is ready" },
  { key: "notify.priorityMail", label: "Important mail", hint: "On actionable incoming mail" },
  { key: "notify.agendaAlerts", label: "Deadline alerts", hint: "Ahead of upcoming deadlines" },
];

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

export function StepNotifications() {
  const [state, setState] = useState<Record<string, boolean>>({});
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    if (!native.available) {
      setLoaded(true);
      return;
    }
    let cancelled = false;
    void Promise.all(
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

  const toggle = (key: string, v: boolean) => {
    setState((s) => ({ ...s, [key]: v }));
    void native.prefs.setBool(key, v);
  };

  return (
    <div>
      <StepHeader
        icon={<Bell size={26} strokeWidth={1.6} />}
        title="Stay in the loop"
        subtitle={
          native.available
            ? "Choose which native notifications Hygur may send. Enabling any one asks macOS for permission once."
            : "Notifications are available when you run Hygur as the desktop app."
        }
      />
      {native.available && (
        <Card>
          {NOTIF_TOGGLES.map((t) => (
            <InfoRow
              key={t.key}
              icon={<Bell size={18} strokeWidth={1.7} />}
              title={t.label}
              detail={t.hint}
              trailing={
                <Toggle
                  checked={Boolean(state[t.key])}
                  disabled={!loaded}
                  onChange={(v) => toggle(t.key, v)}
                />
              }
            />
          ))}
        </Card>
      )}
    </div>
  );
}

// MARK: - Ready

export function StepReady() {
  return (
    <div>
      <StepHeader
        icon={<CheckCircle2 size={26} strokeWidth={1.6} />}
        title="You're all set"
        subtitle="Ask anything in plain language. Hygur searches your indexed sources and answers with citations — entirely on this Mac."
      />
      <Card>
        <InfoRow
          icon={<Sparkles size={18} strokeWidth={1.7} />}
          title="Ask Hygur"
          detail="Type a question, or summon it anywhere with ⌘⇧H."
        />
        <InfoRow
          icon={<PlugZap size={18} strokeWidth={1.7} />}
          title="Add more later"
          detail="Connectors, models and notifications are all editable from the sidebar and Settings."
        />
      </Card>
    </div>
  );
}
