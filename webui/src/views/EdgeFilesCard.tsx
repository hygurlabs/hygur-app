import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { getDesktopConfig, setDesktopConfig } from "../lib/desktop";

/** Local files for the cloud desktop thin client. The pod can't read this Mac's
 *  filesystem, so a chosen folder is indexed ON-DEVICE by the edge runner and its
 *  extracted text pushed to the central KB. Shares /edge/status + /edge/sync with
 *  the Proton card; configures the edge `folder`. Hidden unless a local thin-client
 *  sidecar (same gate as the Proton card: /edge probe, not isDesktop()). */
export function EdgeFilesCard() {
  const qc = useQueryClient();
  const [path, setPath] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const statusQ = useQuery({
    queryKey: ["edge-status"],
    queryFn: () => api.edgeStatus(),
    retry: false,
    refetchInterval: (q) => (q.state.error ? false : 5000),
  });
  const cfgQ = useQuery({
    queryKey: ["edge-cfg"],
    queryFn: () => getDesktopConfig(),
    retry: false,
  });

  const sync = useMutation({
    mutationFn: () => api.edgeSync(),
    onSuccess: () => setTimeout(() => void qc.invalidateQueries({ queryKey: ["edge-status"] }), 1500),
  });

  const errMsg = statusQ.error instanceof Error ? statusQ.error.message : "";
  if (errMsg.includes("404") || errMsg.includes("503")) return null;

  const st = statusQ.data;
  const configured = cfgQ.data?.folder ?? "";
  const value = path ?? configured;
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
      : configured
        ? `Indexation de ${configured}${st?.files_pushed ? ` · ${st.files_pushed} envoyés au dernier passage` : ""}`
        : "Aucun dossier sélectionné pour l’instant.";
  const syncing = !!st?.running;

  // Persist the folder to the edge config via Tauri. Reads the current config and
  // writes it back unchanged except `folder` (blank token / proton_password mean
  // "keep stored", so secrets are preserved).
  const save = async () => {
    setBusy(true);
    setErr(null);
    try {
      const c = await getDesktopConfig();
      await setDesktopConfig({
        mode: c.mode || "cloud",
        server: c.server,
        proton_user: c.proton_user,
        proton_mailbox: c.proton_mailbox,
        interval_secs: c.interval_secs,
        folder: value.trim(),
      });
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
            <h3 className="text-[14px] font-semibold">Fichiers locaux · cet appareil</h3>
            <p className="text-[12px] text-muted">{statusLine}</p>
          </div>
        </div>
        <button
          type="button"
          onClick={() => sync.mutate()}
          disabled={sync.isPending || syncing || !configured}
          className="rounded-md bg-accent px-2.5 py-1 text-[12px] font-medium text-white transition-opacity hover:opacity-90 disabled:opacity-40"
        >
          {sync.isPending || syncing ? "Synchronisation…" : "Synchroniser maintenant"}
        </button>
      </div>
      <div className="mt-3 flex items-center gap-2">
        <input
          value={value}
          onChange={(e) => {
            setPath(e.target.value);
            setSaved(false);
          }}
          placeholder="/Users/you/Documents/…"
          spellCheck={false}
          autoCapitalize="off"
          className="flex-1 rounded-md border border-border bg-bg px-2.5 py-1 text-[12.5px]"
        />
        <button
          type="button"
          onClick={save}
          disabled={busy || !value.trim()}
          className="rounded-md bg-accent px-3 py-1 text-[12px] font-medium text-white transition-opacity hover:opacity-90 disabled:opacity-40"
        >
          Enregistrer le dossier
        </button>
      </div>
      {err && <p className="mt-2 text-[12px] text-danger">{err}</p>}
      {saved && <p className="mt-2 text-[12px] text-green-600">Enregistré — indexé à la prochaine synchro.</p>}
    </div>
  );
}
