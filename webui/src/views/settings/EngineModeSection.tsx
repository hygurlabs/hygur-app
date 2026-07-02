import { useEffect, useState } from "react";
import { isDesktop, getDesktopConfig, type DesktopConfig } from "../../lib/desktop";
import { ModePicker } from "../../onboarding/ModePicker";
import { Button } from "../../components/ui";
import { Row, Section } from "./common";

// MARK: - Engine mode (desktop only: local full engine vs cloud thin client)

export function EngineModeSection() {
  const [cfg, setCfg] = useState<DesktopConfig | null>(null);
  const [picker, setPicker] = useState(false);

  const reload = () => {
    void getDesktopConfig()
      .then(setCfg)
      .catch(() => {});
  };
  useEffect(() => {
    if (isDesktop()) reload();
  }, []);

  if (!isDesktop()) return null;
  if (picker) {
    // A proxy-mode change reloads the page; otherwise we just close + refresh.
    return (
      <ModePicker
        onDone={() => {
          setPicker(false);
          reload();
        }}
        onCancel={() => setPicker(false)}
      />
    );
  }

  const cloud = cfg?.mode === "cloud";
  return (
    <Section title="Engine mode">
      <Row
        label="Mode"
        hint={
          cloud
            ? `Hygur Cloud — ${cfg?.server || "not set"}`
            : "Local — full engine on this Mac"
        }
      >
        <Button variant="ghost" onClick={() => setPicker(true)}>
          {cloud ? "Reconfigure" : "Switch…"}
        </Button>
      </Row>
    </Section>
  );
}
