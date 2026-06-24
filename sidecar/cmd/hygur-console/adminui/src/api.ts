// The admin SPA is served from the console at /admin, so all calls are same-origin.

export interface PeriodCost {
  chat_in: number;
  chat_out: number;
  ingest: number;
  cost: number;
}

export interface CostSummary {
  currency: string;
  today: PeriodCost;
  week: PeriodCost;
  month: PeriodCost;
  run_rate_per_day: number;
  days_elapsed: number;
  days_in_month: number;
  forecast_eom_cost: number;
}

export interface TenantCost {
  account: string;
  tenant_id: string;
  month: PeriodCost;
}

export interface FleetBudget {
  tokens_per_day: number; // configured daily budget (0 = unset/disabled)
  today_tokens: number;
  ratio: number;
  status: "ok" | "warn" | "over";
}

export interface CostResponse {
  summary: CostSummary;
  tenants: TenantCost[];
  budget: FleetBudget;
  captured_at: string;
  generated_at: string;
}

export interface ClientError {
  id: number;
  occurred_at: string;
  message: string;
  stack?: string;
  url?: string;
  app_version?: string;
  user_agent?: string;
  origin?: string;
}

export class AuthError extends Error {}

export async function fetchCost(token: string): Promise<CostResponse> {
  const r = await fetch("/admin/cost", { headers: { Authorization: `Bearer ${token}` } });
  if (r.status === 401 || r.status === 403) throw new AuthError("unauthorized");
  if (!r.ok) throw new Error(`cost fetch failed (${r.status})`);
  return (await r.json()) as CostResponse;
}

export async function fetchErrors(token: string): Promise<ClientError[]> {
  const r = await fetch("/admin/errors", { headers: { Authorization: `Bearer ${token}` } });
  if (r.status === 401 || r.status === 403) throw new AuthError("unauthorized");
  if (!r.ok) throw new Error(`errors fetch failed (${r.status})`);
  return ((await r.json()) as { errors: ClientError[] }).errors;
}
