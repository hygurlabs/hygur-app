import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { fmtNumber } from "../../lib/format";
import { useToast } from "../../lib/toast";
import type {
  TokenPeriodUsage,
  TokenPricing,
  TokenUsageResponse,
} from "../../lib/types";
import { Button } from "../../components/ui";
import { Row, Section } from "./common";

// MARK: - Token usage & cost

/** One labelled progress bar for the token-budget gauge. */
function GaugeRow({
  label,
  used,
  budget,
  pct,
  over,
  color,
}: {
  label: string;
  used: number;
  budget: number;
  pct: number;
  over: boolean;
  color: string;
}) {
  const f = (n: number) => fmtNumber(n);
  return (
    <div className="mb-2.5 last:mb-0">
      <div className="mb-1 flex items-baseline justify-between text-[12px]">
        <span className="font-medium">{label}</span>
        <span className={`tabular-nums ${over ? "font-semibold text-danger" : "text-muted"}`}>
          {f(used)} / {f(budget)}
          {over && " — over budget"}
        </span>
      </div>
      <div className="h-2 overflow-hidden rounded-full bg-border">
        <div className={`h-full rounded-full ${color}`} style={{ width: `${pct * 100}%` }} />
      </div>
    </div>
  );
}

// A locale-tolerant decimal input. `type="number"` rejects comma decimals
// (French keyboards) and a controlled numeric value clobbers half-typed states
// like "0,"; so this is an uncontrolled text field that accepts both "," and
// "." and reports the parsed number upward. Seeded once at mount.
function PriceField({
  value,
  onChange,
}: {
  value: number;
  onChange: (n: number) => void;
}) {
  return (
    <input
      type="text"
      inputMode="decimal"
      defaultValue={value ? String(value).replace(".", ",") : ""}
      placeholder="0"
      onChange={(e) => {
        const n = parseFloat(e.target.value.replace(",", "."));
        onChange(Number.isFinite(n) ? n : 0);
      }}
      className="w-28 rounded-lg border border-border bg-surface px-2 py-1 text-right text-sm tabular-nums outline-none focus:border-accent"
    />
  );
}

export function TokenUsageSection({ managed }: { managed: boolean }) {
  const qc = useQueryClient();
  const toast = useToast();
  const { data } = useQuery({
    queryKey: ["usage"],
    queryFn: api.getTokenUsage,
  });
  // Local draft of the price fields, seeded once from the server values.
  // Seeding during render (not in an effect) is React's sanctioned pattern for
  // deriving initial state from async data — it converges once price is set.
  const [price, setPrice] = useState<TokenPricing | null>(null);
  if (price === null && data?.pricing) {
    setPrice(data.pricing);
  }

  const save = useMutation({
    mutationFn: (p: TokenPricing) => api.setTokenPricing(p),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["usage"] }),
    onError: (e) => toast.error(`Couldn't save prices: ${(e as Error).message}`),
  });

  if (!data || !price) {
    return (
      <Section title="Token usage & cost">
        <Row label="Usage">
          <span className="text-[12.5px] text-faint">loading…</span>
        </Row>
      </Section>
    );
  }

  const cur = price.currency || data.currency || "€";
  const fmtTok = (n: number) => fmtNumber(n);
  const money = (n: number) => `${n.toFixed(n > 0 && n < 1 ? 4 : 2)} ${cur}`;
  const chatCost = (p: TokenPeriodUsage) =>
    (p.chat_in / 1e6) * price.chat_in_per_1m +
    (p.chat_out / 1e6) * price.chat_out_per_1m;
  const ingestCost = (p: TokenPeriodUsage) =>
    ((p.embedding + p.indexing) / 1e6) * price.ingest_per_1m;

  const periods: { key: keyof TokenUsageResponse["periods"]; label: string }[] = [
    { key: "today", label: "Today" },
    { key: "this_week", label: "This week" },
    { key: "this_month", label: "This month" },
  ];
  const dirty = JSON.stringify(price) !== JSON.stringify(data.pricing);

  // Monthly inference caps (hardcoded) and the weekly slice we watch against.
  // Both directions sit in one weekly gauge so we can judge whether 8M IN / 2M
  // OUT per month leaves enough gross margin at the current price.
  const MONTHLY_IN = 8_000_000;
  const MONTHLY_OUT = 2_000_000;
  const weekBudget = (monthly: number) => Math.round((monthly * 7) / 30);
  const wk = data.periods.this_week;
  const gauge = (used: number, budget: number) => {
    const pct = budget > 0 ? Math.min(used / budget, 1) : 0;
    const over = budget > 0 && used > budget;
    const color = over ? "bg-danger" : pct >= 0.75 ? "bg-warn" : "bg-success";
    return { pct, over, color };
  };
  const inG = gauge(wk.total_in, weekBudget(MONTHLY_IN));
  const outG = gauge(wk.total_out, weekBudget(MONTHLY_OUT));

  // Managed cloud tenant: a single merged consumption bar (IN+OUT), no raw token
  // counters, no prices, no per-category table — like Claude's usage panel. The
  // full breakdown stays for self-hosted operators.
  if (managed) {
    const usedWk = wk.total_in + wk.total_out;
    const budgetWk = weekBudget(MONTHLY_IN + MONTHLY_OUT);
    const g = gauge(usedWk, budgetWk);
    const pct = Math.round(g.pct * 100);
    return (
      <Section title="Usage">
        <div className="px-4 pb-4 pt-3">
          <div className="mb-1.5 flex items-baseline justify-between text-[12px]">
            <span className="font-medium">This week</span>
            <span className={`tabular-nums ${g.over ? "font-semibold text-danger" : "text-muted"}`}>
              {pct}% used
            </span>
          </div>
          <div className="h-2 overflow-hidden rounded-full bg-border">
            <div className={`h-full rounded-full ${g.color}`} style={{ width: `${g.pct * 100}%` }} />
          </div>
          <p className="mt-2 text-[11.5px] text-faint">Resets weekly. Your plan covers normal daily use.</p>
        </div>
      </Section>
    );
  }

  return (
    <Section title="Token usage & cost">
      <div className="px-4 pb-1 pt-3">
        <div className="mb-1 flex items-baseline justify-between">
          <span className="text-[12px] font-medium">This week's budget</span>
          <span className="text-[11px] text-faint">
            caps: {fmtTok(MONTHLY_IN)} IN · {fmtTok(MONTHLY_OUT)} OUT / month
          </span>
        </div>
        <GaugeRow label="Input" used={wk.total_in} budget={weekBudget(MONTHLY_IN)} {...inG} />
        <GaugeRow label="Output" used={wk.total_out} budget={weekBudget(MONTHLY_OUT)} {...outG} />
      </div>
      <div className="overflow-x-auto px-4 py-3">
        <table className="w-full text-[13px]">
          <thead>
            <tr className="text-[11px] uppercase tracking-wide text-faint">
              <th className="pb-2 text-left font-medium">Period</th>
              <th className="pb-2 text-right font-medium">Chat IN</th>
              <th className="pb-2 text-right font-medium">Chat OUT</th>
              <th className="pb-2 text-right font-medium">Embeddings</th>
              <th className="pb-2 text-right font-medium">Indexing</th>
              <th className="pb-2 text-right font-medium">Cost</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {periods.map(({ key, label }) => {
              const p = data.periods[key];
              return (
                <tr key={key}>
                  <td className="py-1.5">{label}</td>
                  <td className="py-1.5 text-right tabular-nums">{fmtTok(p.chat_in)}</td>
                  <td className="py-1.5 text-right tabular-nums">{fmtTok(p.chat_out)}</td>
                  <td className="py-1.5 text-right tabular-nums">{fmtTok(p.embedding)}</td>
                  <td className="py-1.5 text-right tabular-nums">{fmtTok(p.indexing)}</td>
                  <td className="py-1.5 text-right font-medium tabular-nums">
                    {money(chatCost(p) + ingestCost(p))}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      <Row label="Chat IN price" hint={`Per 1M tokens (${cur})`}>
        <PriceField
          value={price.chat_in_per_1m}
          onChange={(n) => setPrice({ ...price, chat_in_per_1m: n })}
        />
      </Row>
      <Row label="Chat OUT price" hint={`Per 1M tokens (${cur})`}>
        <PriceField
          value={price.chat_out_per_1m}
          onChange={(n) => setPrice({ ...price, chat_out_per_1m: n })}
        />
      </Row>
      <Row
        label="Embeddings & indexing price"
        hint={`Per 1M tokens (${cur}) — applied to both`}
      >
        <PriceField
          value={price.ingest_per_1m}
          onChange={(n) => setPrice({ ...price, ingest_per_1m: n })}
        />
      </Row>
      <div className="flex items-center justify-end gap-3 px-4 py-3">
        <Button
          onClick={() => save.mutate(price)}
          disabled={!dirty || save.isPending}
        >
          {save.isPending ? "Saving…" : "Save prices"}
        </Button>
      </div>
    </Section>
  );
}
