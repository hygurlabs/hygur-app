import { useRef, useState } from "react";
import { isRemote } from "../../lib/connection";
import { api } from "../../lib/api";
import { Button, ErrorBanner } from "../../components/ui";
import { Row, Section } from "./common";

// MARK: - Database backup / restore

export function BackupSection() {
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
