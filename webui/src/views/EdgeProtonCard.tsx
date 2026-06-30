import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { getDesktopConfig, setDesktopConfig } from "../lib/desktop";

/** Proton "connector" for the cloud desktop thin client. Proton Bridge lives on
 *  THIS device (loopback), so the cloud pod can never reach it — this card talks
 *  to the local sidecar's /edge/* routes instead: list folders, show the sync
 *  status (green dot), pick mailboxes, and trigger a sync. The edge push loop
 *  streams the extracted text to the central KB. Hidden in a browser (no local
 *  Bridge) and when the sidecar isn't a thin client (/edge → 503). */
export function EdgeProtonCard() {
  const qc = useQueryClient();
  const [folders, setFolders] = useState<string[] | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [cfgOpen, setCfgOpen] = useState(false);
  const [user, setUser] = useState<string | null>(null);
  const [pass, setPass] = useState("");
  const [backfill, setBackfill] = useState<number | null>(null);

  // Probe /edge/status: 200 only on a local thin-client sidecar (desktop cloud).
  // This IS the capability gate — no isDesktop()/isTauri() check, which is
  // unreliable on the remote-origin (127.0.0.1:8420) webview. A browser shell
  // (404) or local/self-host sidecar (503) errors → the card hides. Stop polling
  // once it errors so a non-thin-client doesn't get hammered.
  const statusQ = useQuery({
    queryKey: ["edge-status"],
    queryFn: () => api.edgeStatus(),
    retry: false,
    refetchInterval: (q) => (q.state.error ? false : 5000),
  });

  const sync = useMutation({
    mutationFn: () => api.edgeSync(),
    onSuccess: () => {
      // give the push a moment, then refresh the status badge + indexed count
      setTimeout(() => {
        void qc.invalidateQueries({ queryKey: ["edge-status"] });
        void qc.invalidateQueries({ queryKey: ["mail-count"] });
      }, 1500);
    },
  });

  // Total mail indexed in the library (cloud KB) — the "something is happening"
  // signal: it grows after each sync. Counts all mail sources, not Proton only.
  const mailCountQ = useQuery({
    queryKey: ["mail-count"],
    queryFn: () => api.knowledgeCount("mail"),
    retry: false,
    refetchInterval: 30000,
  });

  // Current edge config (proton_user + whether a password is stored) so the
  // Bridge-login form can show what's set without ever echoing the secret.
  const cfgQ = useQuery({ queryKey: ["edge-cfg"], queryFn: () => getDesktopConfig(), retry: false });

  // Hide ONLY when genuinely not a thin client: a browser shell hits a static host
  // (404), a local/self-host sidecar returns 503. ANY other state — loading, 200,
  // or a transient 401/network error — keeps the card visible on the desktop so it
  // can never silently vanish; we surface the reason instead of disappearing.
  const errMsg = statusQ.error instanceof Error ? statusQ.error.message : "";
  if (errMsg.includes("404") || errMsg.includes("503")) return null;

  const st = statusQ.data;
  const healthy = !!st && !st.last_error && st.errors === 0 && !!st.last_sync_at;
  const dot =
    errMsg || st?.last_error || (st && st.errors > 0)
      ? "bg-danger"
      : healthy
        ? "bg-green-500"
        : "bg-amber-500";
  const statusLine = statusQ.isLoading
    ? "Vérification de la synchro sur l’appareil…"
    : errMsg
      ? `Edge injoignable : ${errMsg}`
      : st?.running
        ? "Synchronisation…"
        : st?.last_error
          ? st.last_error
          : `Dernière synchro ${st?.last_sync_at ? new Date(st.last_sync_at).toLocaleString() : "jamais"}${st?.mail_pushed ? ` · ${st.mail_pushed} envoyés au dernier passage` : ""}`;
  const syncing = !!st?.running;
  const userVal = user ?? cfgQ.data?.proton_user ?? "";
  const pwSet = !!cfgQ.data?.proton_password_set;
  const backfillVal = backfill ?? cfgQ.data?.backfill_count ?? 200;

  const loadFolders = async () => {
    setBusy(true);
    setErr(null);
    try {
      const r = await api.edgeMailboxes();
      setFolders(r.mailboxes ?? []);
      // Pre-select the currently configured mailbox(es). Best-effort: a Tauri IPC
      // hiccup must not block showing the folders.
      try {
        const c = await getDesktopConfig();
        setSelected(
          new Set(
            (c.proton_mailbox || "All Mail")
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean),
          ),
        );
      } catch {
        setSelected(new Set(["All Mail"]));
      }
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const toggle = (f: string) => {
    setSaved(false);
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(f)) next.delete(f);
      else next.add(f);
      return next;
    });
  };

  // Persist the mailbox selection to the edge config via Tauri. Reads the current
  // config and writes it back unchanged except the mailbox — blank token /
  // proton_password mean "keep stored", so secrets are preserved.
  const saveFolders = async () => {
    setBusy(true);
    setErr(null);
    try {
      const c = await getDesktopConfig();
      await setDesktopConfig({
        mode: c.mode || "cloud",
        server: c.server,
        folder: c.folder,
        proton_user: c.proton_user,
        proton_mailbox: Array.from(selected).join(",") || "All Mail",
        interval_secs: c.interval_secs,
        backfill_count: backfillVal,
      });
      setSaved(true);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  // Save the Proton Bridge credentials to the edge config (via Tauri). A blank
  // password means "keep the stored one" — it is never echoed back to the page.
  const saveCredentials = async () => {
    setBusy(true);
    setErr(null);
    try {
      const c = await getDesktopConfig();
      await setDesktopConfig({
        mode: c.mode || "cloud",
        server: c.server,
        folder: c.folder,
        proton_user: userVal.trim(),
        proton_password: pass, // blank = keep stored
        proton_mailbox: c.proton_mailbox,
        interval_secs: c.interval_secs,
      });
      setPass("");
      setSaved(true);
      void qc.invalidateQueries({ queryKey: ["edge-cfg"] });
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mb-6 rounded-xl border border-border bg-surface p-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2.5">
          <span className={`inline-block size-2.5 rounded-full ${dot}`} />
          <div>
            <h3 className="text-[14px] font-semibold">
              Proton Mail · cet appareil
              {typeof mailCountQ.data?.total === "number" && (
                <span className="ml-2 rounded-full bg-accent/10 px-2 py-0.5 text-[11px] font-medium text-accent">
                  {mailCountQ.data.total.toLocaleString("fr-FR")} mails indexés
                </span>
              )}
            </h3>
            <p className="text-[12px] text-muted">{statusLine}</p>
          </div>
        </div>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={() => {
              setCfgOpen((v) => !v);
              setSaved(false);
            }}
            className="rounded-md border border-border px-2.5 py-1 text-[12px] text-muted transition-colors hover:border-accent/40 hover:text-accent"
          >
            Connexion Bridge
          </button>
          <button
            type="button"
            onClick={loadFolders}
            disabled={busy}
            className="rounded-md border border-border px-2.5 py-1 text-[12px] text-muted transition-colors hover:border-accent/40 hover:text-accent disabled:opacity-40"
          >
            {busy ? "…" : folders ? "Actualiser les dossiers" : "Charger les dossiers"}
          </button>
          <button
            type="button"
            onClick={() => sync.mutate()}
            disabled={sync.isPending || syncing}
            className="rounded-md bg-accent px-2.5 py-1 text-[12px] font-medium text-white transition-opacity hover:opacity-90 disabled:opacity-40"
          >
            {sync.isPending || syncing ? "Synchronisation…" : "Synchroniser maintenant"}
          </button>
        </div>
      </div>

      {err && <p className="mt-3 text-[12px] text-danger">{err}</p>}

      {cfgOpen && (
        <div className="mt-3 space-y-2 rounded-lg border border-border p-3">
          <p className="text-[12px] text-muted">
            Proton Bridge tourne sur ce Mac. Utilisez son adresse <strong>Bridge</strong> + le
            mot de passe d’application qu’il affiche (pas votre mot de passe Proton).
          </p>
          <label className="block">
            <span className="mb-1 block text-[12px] font-medium">Nom d’utilisateur Bridge</span>
            <input
              value={userVal}
              onChange={(e) => {
                setUser(e.target.value);
                setSaved(false);
              }}
              placeholder="you@proton.me"
              spellCheck={false}
              autoCapitalize="off"
              className="w-full rounded-md border border-border bg-bg px-2.5 py-1 text-[12.5px]"
            />
          </label>
          <label className="block">
            <span className="mb-1 block text-[12px] font-medium">Mot de passe d’application Bridge</span>
            <input
              type="password"
              value={pass}
              onChange={(e) => {
                setPass(e.target.value);
                setSaved(false);
              }}
              placeholder={pwSet ? "•••••••• (enregistré — laisser vide pour conserver)" : "depuis Proton Bridge"}
              spellCheck={false}
              autoCapitalize="off"
              className="w-full rounded-md border border-border bg-bg px-2.5 py-1 text-[12.5px]"
            />
          </label>
          <button
            type="button"
            onClick={saveCredentials}
            disabled={busy || !userVal.trim() || (!pass && !pwSet)}
            className="rounded-md bg-accent px-3 py-1 text-[12px] font-medium text-white transition-opacity hover:opacity-90 disabled:opacity-40"
          >
            Enregistrer la connexion
          </button>
          {saved && <span className="ml-2 text-[12px] text-green-600">Enregistré — appliqué à la prochaine synchro.</span>}
        </div>
      )}

      {folders && (
        <div className="mt-3">
          <div className="max-h-44 overflow-y-auto rounded-lg border border-border p-1">
            {folders.length === 0 ? (
              <p className="px-2 py-1 text-[12px] text-muted">Aucun dossier renvoyé.</p>
            ) : (
              folders.map((f) => (
                <label
                  key={f}
                  className="flex cursor-pointer items-center gap-2 rounded px-2 py-1 text-[12.5px] hover:bg-bg"
                >
                  <input type="checkbox" checked={selected.has(f)} onChange={() => toggle(f)} />
                  {f}
                </label>
              ))
            )}
          </div>
          <label className="mt-2 flex items-center gap-2 text-[12px]">
            <span className="text-muted">Mails à récupérer par dossier (première synchro) :</span>
            <input
              type="number"
              min={1}
              value={backfillVal}
              onChange={(e) => {
                setBackfill(Math.max(1, Number(e.target.value) || 0));
                setSaved(false);
              }}
              className="w-20 rounded-md border border-border bg-bg px-2 py-1 text-[12.5px] tabular-nums"
            />
          </label>
          <div className="mt-2 flex items-center gap-3">
            <button
              type="button"
              onClick={saveFolders}
              disabled={busy}
              className="rounded-md bg-accent px-3 py-1 text-[12px] font-medium text-white transition-opacity hover:opacity-90 disabled:opacity-40"
            >
              Enregistrer la sélection
            </button>
            {saved && <span className="text-[12px] text-green-600">Enregistré — appliqué à la prochaine synchro.</span>}
          </div>
        </div>
      )}
    </div>
  );
}
