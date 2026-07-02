import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { Button, ErrorBanner } from "../../components/ui";
import { Row, Section } from "./common";

// MARK: - Local encryption

export function EncryptionSection() {
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
