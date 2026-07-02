import { usePasskeyCount, useAddPasskey } from "../../lib/usePasskey";
import { passkeysSupported } from "../../lib/passkey";
import { Button, ErrorBanner } from "../../components/ui";
import { Row, Section } from "./common";

// MARK: - Security (passkey) — managed cloud only

// Lets a user who enrolled by code and skipped passkey setup add one later, so
// they're no longer locked to the enrolling browser. The red banner at the top of
// Settings (and the global one) covers the "none yet" case; this is the durable
// home for it + adding more.
export function PasskeySecuritySection() {
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
