import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { getDesktopConfig, isDesktop, setDesktopConfig } from "../lib/desktop";

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

  const statusQ = useQuery({
    queryKey: ["edge-status"],
    queryFn: () => api.edgeStatus(),
    enabled: isDesktop(),
    retry: false, // a 503 (not a thin client) should resolve to "hidden" fast
    refetchInterval: 5000,
  });

  const sync = useMutation({
    mutationFn: () => api.edgeSync(),
    onSuccess: () => {
      // give the push a moment, then refresh the status badge
      setTimeout(() => void qc.invalidateQueries({ queryKey: ["edge-status"] }), 1500);
    },
  });

  // Web shell (no local Bridge) or local/self-host sidecar (/edge → 503) → hide.
  if (!isDesktop() || statusQ.isError || statusQ.isLoading || !statusQ.data) return null;
  const st = statusQ.data;
  const healthy = !st.last_error && st.errors === 0 && !!st.last_sync_at;
  const dot = st.last_error || st.errors > 0 ? "bg-danger" : healthy ? "bg-green-500" : "bg-amber-500";

  const loadFolders = async () => {
    setBusy(true);
    setErr(null);
    try {
      const r = await api.edgeMailboxes();
      setFolders(r.mailboxes ?? []);
      const c = await getDesktopConfig();
      setSelected(
        new Set(
          (c.proton_mailbox || "All Mail")
            .split(",")
            .map((s) => s.trim())
            .filter(Boolean),
        ),
      );
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
      });
      setSaved(true);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const lastSynced = st.last_sync_at ? new Date(st.last_sync_at).toLocaleString() : "never";

  return (
    <div className="mb-6 rounded-xl border border-border bg-surface p-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2.5">
          <span className={`inline-block size-2.5 rounded-full ${dot}`} />
          <div>
            <h3 className="text-[14px] font-semibold">Proton Mail · this device</h3>
            <p className="text-[12px] text-muted">
              {st.running
                ? "Syncing…"
                : st.last_error
                  ? st.last_error
                  : `Last synced ${lastSynced}${st.mail_pushed ? ` · ${st.mail_pushed} pushed last run` : ""}`}
            </p>
          </div>
        </div>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={loadFolders}
            disabled={busy}
            className="rounded-md border border-border px-2.5 py-1 text-[12px] text-muted transition-colors hover:border-accent/40 hover:text-accent disabled:opacity-40"
          >
            {busy ? "…" : folders ? "Refresh folders" : "Load folders"}
          </button>
          <button
            type="button"
            onClick={() => sync.mutate()}
            disabled={sync.isPending || st.running}
            className="rounded-md bg-accent px-2.5 py-1 text-[12px] font-medium text-white transition-opacity hover:opacity-90 disabled:opacity-40"
          >
            {sync.isPending || st.running ? "Syncing…" : "Sync now"}
          </button>
        </div>
      </div>

      {err && <p className="mt-3 text-[12px] text-danger">{err}</p>}

      {folders && (
        <div className="mt-3">
          <div className="max-h-44 overflow-y-auto rounded-lg border border-border p-1">
            {folders.length === 0 ? (
              <p className="px-2 py-1 text-[12px] text-muted">No folders returned.</p>
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
          <div className="mt-2 flex items-center gap-3">
            <button
              type="button"
              onClick={saveFolders}
              disabled={busy}
              className="rounded-md bg-accent px-3 py-1 text-[12px] font-medium text-white transition-opacity hover:opacity-90 disabled:opacity-40"
            >
              Save selection
            </button>
            {saved && <span className="text-[12px] text-green-600">Saved — applies on next sync.</span>}
          </div>
        </div>
      )}
    </div>
  );
}
