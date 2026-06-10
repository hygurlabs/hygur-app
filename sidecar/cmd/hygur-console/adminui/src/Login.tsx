import { useState } from "react";
import { enrollAndRegister, passkeyLogin } from "./auth";

export function Login({ onAuthed }: { onAuthed: (token: string) => void }) {
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");

  const run = async (fn: () => Promise<string>) => {
    setBusy(true);
    setMsg("");
    try {
      onAuthed(await fn());
    } catch (e) {
      setMsg((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="login">
      <div className="card">
        <h1>Hygur Cloud · Admin</h1>
        <p>Operator access — sign in with your passkey.</p>
        <button className="btn" disabled={busy} onClick={() => run(passkeyLogin)}>
          Sign in with passkey
        </button>
        <div className="sep">first time on this device</div>
        <input
          value={code}
          onChange={(e) => setCode(e.target.value)}
          placeholder="one-time code"
          autoCapitalize="off"
          autoCorrect="off"
          spellCheck={false}
        />
        <button className="btn ghost" disabled={busy || !code.trim()} onClick={() => run(() => enrollAndRegister(code))}>
          Register this device
        </button>
        <div className="msg">{msg}</div>
      </div>
    </div>
  );
}
