import { useState } from "react";
import { api } from "../../lib/api";
import { Button, ErrorBanner } from "../../components/ui";
import { Row, Section } from "./common";

// MARK: - Encrypted data export (GDPR portability)

// Exports the notes & briefs Hygur produced, in an archive encrypted with a
// passphrase YOU choose (never sent anywhere reusable — it's the zip key). The
// server streams it directly; nothing is stored server-side. Decrypt with any
// tool, e.g. `openssl enc -d -aes-256-cbc -pbkdf2 -pass pass:… -in file.zip.enc`.
export function ExportSection() {
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
