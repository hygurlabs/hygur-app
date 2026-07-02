import { useState } from "react";
import { linkDeviceCode } from "../../lib/api";
import { QRCodeSVG } from "qrcode.react";
import { Button } from "../../components/ui";
import { Row, Section } from "./common";

// ConnectPhoneSection shows a QR that deep-links a phone to this space with a
// one-time code (WhatsApp-Web style): scanning signs the phone in, no typing.
export function ConnectPhoneSection() {
  const [url, setUrl] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const generate = async () => {
    setBusy(true);
    setErr("");
    try {
      const { code, slug } = await linkDeviceCode();
      setUrl(`https://cloud.hygur.ai/${slug}?code=${encodeURIComponent(code)}`);
    } catch (e) {
      setErr((e as Error).message);
    }
    setBusy(false);
  };
  return (
    <Section title="Connect a phone">
      <Row
        label="Add a device"
        hint={
          err ||
          "Scan with your phone's camera to sign in there — no typing. The code works once and expires in 10 minutes."
        }
      >
        <Button variant="ghost" onClick={() => void generate()} disabled={busy}>
          {busy ? "Generating…" : url ? "New code" : "Show QR code"}
        </Button>
      </Row>
      {url && (
        <div className="flex flex-col items-center gap-3 px-4 py-5">
          <div className="rounded-xl bg-white p-3 shadow-sm">
            <QRCodeSVG value={url} size={180} />
          </div>
          <p className="max-w-full break-all text-center text-[11px] text-faint">{url}</p>
        </div>
      )}
    </Section>
  );
}
