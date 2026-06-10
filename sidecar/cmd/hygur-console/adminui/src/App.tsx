import { useEffect, useState } from "react";
import { getToken, refresh } from "./auth";
import { Login } from "./Login";
import { Dashboard } from "./Dashboard";

export function App() {
  const [token, setToken] = useState<string>(getToken());
  const [booting, setBooting] = useState<boolean>(!getToken());

  useEffect(() => {
    if (token) return;
    // Returning operator: try a silent cookie refresh before showing the login.
    let alive = true;
    refresh()
      .then((t) => {
        if (alive) setToken(t);
      })
      .catch(() => {})
      .finally(() => {
        if (alive) setBooting(false);
      });
    return () => {
      alive = false;
    };
  }, [token]);

  if (booting) {
    return (
      <div className="login">
        <div className="label">loading…</div>
      </div>
    );
  }
  if (!token) return <Login onAuthed={setToken} />;
  return <Dashboard token={token} onSignOut={() => setToken("")} />;
}
