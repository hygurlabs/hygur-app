import { useState } from "react";
import { clearConnection, getConnection, isRemote, setConnection } from "../../lib/connection";
import { Button, TextInput } from "../../components/ui";
import { Row, Section } from "./common";

// MARK: - Connection (local sidecar vs remote endpoint + key)

export function ConnectionSection() {
  const initial = getConnection();
  const [endpoint, setEndpoint] = useState(initial.endpoint);
  const [key, setKey] = useState(initial.key);
  const remote = isRemote();

  const connect = () => {
    setConnection(endpoint, key);
    // Reload so every query refetches against the new base origin.
    window.location.reload();
  };
  const disconnect = () => {
    clearConnection();
    window.location.reload();
  };

  return (
    <Section title="Connection">
      <Row
        label="Mode"
        hint={
          remote
            ? `Remote — ${initial.endpoint}`
            : "Local — served by the sidecar on this machine"
        }
      >
        {remote && (
          <Button variant="ghost" onClick={disconnect}>
            Disconnect
          </Button>
        )}
      </Row>
      <Row label="Server endpoint" hint="Empty = local sidecar. e.g. https://app.hygur.eu">
        <TextInput
          value={endpoint}
          spellCheck={false}
          autoCapitalize="off"
          placeholder="https://app.hygur.eu"
          onChange={(e) => setEndpoint(e.target.value)}
          className="w-64"
        />
      </Row>
      <Row label="API key" hint="Sent as X-Hygur-Token. Stored on this device only.">
        <TextInput
          type="password"
          value={key}
          spellCheck={false}
          autoCapitalize="off"
          onChange={(e) => setKey(e.target.value)}
          className="w-64"
        />
      </Row>
      <Row label="Apply">
        <Button onClick={connect} disabled={!endpoint.trim()}>
          {remote ? "Update & reconnect" : "Connect"}
        </Button>
      </Row>
    </Section>
  );
}
