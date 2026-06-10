import { useCallback, useEffect, useRef, useState } from "react";
import { AuthError, fetchCost, fetchErrors, type ClientError, type CostResponse } from "./api";
import { signOut } from "./auth";
import { CountUp, MetricTile, Skeleton, fmtInt, fmtMoney } from "./ui";

function ago(iso: string): string {
  if (!iso) return "never";
  const s = Math.max(0, Math.floor((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  return `${Math.floor(s / 3600)}h ago`;
}

export function Dashboard({ token, onSignOut }: { token: string; onSignOut: () => void }) {
  const [data, setData] = useState<CostResponse | null>(null);
  const [errors, setErrors] = useState<ClientError[]>([]);
  const [err, setErr] = useState("");
  const tok = useRef(token);

  const load = useCallback(async () => {
    try {
      setData(await fetchCost(tok.current));
      setErrors(await fetchErrors(tok.current));
      setErr("");
    } catch (e) {
      if (e instanceof AuthError) {
        signOut();
        onSignOut();
      } else {
        setErr((e as Error).message);
      }
    }
  }, [onSignOut]);

  useEffect(() => {
    void load();
    const id = setInterval(() => void load(), 30_000);
    return () => clearInterval(id);
  }, [load]);

  const cur = data?.summary.currency ?? "€";
  const money = (v: number) => <CountUp value={v} prefix={cur} decimals={2} />;

  return (
    <div className="shell">
      <aside className="side">
        <div className="brand">
          Hygur Cloud
          <small>operator</small>
        </div>
        <nav>
          <span className="navitem active">Cost</span>
        </nav>
        <div className="spacer" />
        <button className="sign-out" onClick={() => { signOut(); onSignOut(); }}>
          Sign out
        </button>
      </aside>

      <main className="main">
        <div className="topbar">
          <h1>Cost</h1>
          <span className="fresh">
            <span className="dot" />
            updated {data ? ago(data.captured_at) : "…"}
          </span>
        </div>

        {err ? <div className="err">Couldn&apos;t load: {err}</div> : null}

        <div className="kpis">
          <MetricTile label="Spend · MTD">{data ? money(data.summary.month.cost) : <Skeleton w={100} h={30} />}</MetricTile>
          <MetricTile label="Run-rate · /day">{data ? money(data.summary.run_rate_per_day) : <Skeleton w={80} h={30} />}</MetricTile>
          <MetricTile
            label="Forecast · EOM"
            alert
            sub={data ? `day ${data.summary.days_elapsed} / ${data.summary.days_in_month}` : undefined}
          >
            {data ? money(data.summary.forecast_eom_cost) : <Skeleton w={100} h={30} />}
          </MetricTile>
          <MetricTile label="Tenants" sub="active">
            {data ? <CountUp value={data.tenants.length} decimals={0} /> : <Skeleton w={44} h={30} />}
          </MetricTile>
        </div>

        <div className="section-head">
          <span className="idx">01</span>
          <span className="label">By tenant · month-to-date</span>
        </div>

        {!data ? (
          <Skeleton h={120} />
        ) : data.tenants.length === 0 ? (
          <div className="empty">No usage recorded this month yet. The poller ingests each tenant's usage on its schedule.</div>
        ) : (
          <table className="data">
            <thead>
              <tr>
                <th className="label">Tenant</th>
                <th className="label">Account</th>
                <th className="num label">Chat in</th>
                <th className="num label">Chat out</th>
                <th className="num label">Ingest</th>
                <th className="num label">Cost MTD</th>
              </tr>
            </thead>
            <tbody>
              {data.tenants.map((t) => (
                <tr key={t.tenant_id}>
                  <td className="ten">{t.tenant_id}</td>
                  <td className="ten">{t.account}</td>
                  <td className="num">{fmtInt(t.month.chat_in)}</td>
                  <td className="num">{fmtInt(t.month.chat_out)}</td>
                  <td className="num">{fmtInt(t.month.ingest)}</td>
                  <td className="num cost">{fmtMoney(t.month.cost, cur)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}

        <div className="section-head">
          <span className="idx">02</span>
          <span className="label">Recent client errors</span>
        </div>
        {errors.length === 0 ? (
          <div className="empty">
            No client errors reported. Cloud sessions report crashes here automatically (first-party, no third-party tracking).
          </div>
        ) : (
          <table className="data">
            <thead>
              <tr>
                <th className="label">When</th>
                <th className="label">Message</th>
                <th className="label">Build</th>
              </tr>
            </thead>
            <tbody>
              {errors.map((e) => (
                <tr key={e.id}>
                  <td className="ten">{ago(e.occurred_at)}</td>
                  <td>
                    {e.message}
                    {e.url ? <div className="ten">{e.url}</div> : null}
                    {e.stack ? (
                      <details>
                        <summary style={{ cursor: "pointer", color: "#888", fontSize: 11 }}>stack</summary>
                        <pre style={{ whiteSpace: "pre-wrap", fontSize: 11, color: "#888", margin: "4px 0 0" }}>
                          {e.stack}
                        </pre>
                      </details>
                    ) : null}
                  </td>
                  <td className="ten">{e.app_version || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </main>
    </div>
  );
}
