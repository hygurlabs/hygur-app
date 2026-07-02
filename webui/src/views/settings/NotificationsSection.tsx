import { useEffect, useState } from "react";
import { native } from "../../lib/native";
import { enablePush, disablePush, pushSupported } from "../../lib/push";
import { api } from "../../lib/api";
import { Button } from "../../components/ui";
import { Row, Section, Toggle } from "./common";

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

export function NotificationsSection({ vapidPublicKey }: { vapidPublicKey: string }) {
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
