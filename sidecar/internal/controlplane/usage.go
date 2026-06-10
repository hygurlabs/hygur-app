package controlplane

import (
	"database/sql"
	"sort"
	"strconv"
	"time"
)

// TenantUsageDay is one tenant's pivoted token usage for one day: chat split by
// direction, with embedding + indexing folded into a single "ingest" bucket to
// match the fleet pricing model (chat in/out billed separately, ingest one rate).
type TenantUsageDay struct {
	TenantID string
	Account  string
	Day      string // YYYY-MM-DD
	ChatIn   int
	ChatOut  int
	Ingest   int
}

// UpsertTenantUsage records (or overwrites) one tenant's usage for one day. The
// on-box poller calls it per (tenant, day) from each tenant's `usage dump`.
// Idempotent on (tenant_id, day), so re-polling the same month is safe.
func (s *Store) UpsertTenantUsage(now time.Time, u TenantUsageDay) error {
	_, err := s.db.Exec(`
INSERT INTO tenant_usage_snapshots(tenant_id,account_number,day,chat_in,chat_out,ingest,captured_at)
VALUES(?,?,?,?,?,?,?)
ON CONFLICT(tenant_id,day) DO UPDATE SET
  account_number = excluded.account_number,
  chat_in     = excluded.chat_in,
  chat_out    = excluded.chat_out,
  ingest      = excluded.ingest,
  captured_at = excluded.captured_at`,
		u.TenantID, u.Account, u.Day, u.ChatIn, u.ChatOut, u.Ingest, now.UTC().Format(rfc))
	return err
}

// FleetTenant is a provisioned tenant the cost poll should account for.
type FleetTenant struct {
	Account  string
	TenantID string
}

// ListLiveTenants returns accounts whose subscription is provisioned (ready or
// pending) — the set the cost poll dumps usage from. Sibling of ListProvisions.
func (s *Store) ListLiveTenants() ([]FleetTenant, error) {
	rows, err := s.db.Query(`
SELECT a.account_number, a.tenant_id
FROM accounts a
JOIN stripe_subscriptions ss ON ss.account_number = a.account_number
WHERE ss.provision_state IN ('ready','pending')
GROUP BY a.account_number, a.tenant_id
ORDER BY a.account_number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FleetTenant
	for rows.Next() {
		var t FleetTenant
		if err := rows.Scan(&t.Account, &t.TenantID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Fleet-wide pricing (single price for the whole fleet, fed by the latest ingest).
const (
	fsChatInPer1M  = "price_chat_in_per_1m"
	fsChatOutPer1M = "price_chat_out_per_1m"
	fsIngestPer1M  = "price_ingest_per_1m"
	fsCurrency     = "price_currency"
)

// FleetPricing holds the per-1M-token prices used to estimate cost across the fleet.
type FleetPricing struct {
	ChatInPer1M  float64
	ChatOutPer1M float64
	IngestPer1M  float64
	Currency     string
}

// SetFleetPricing upserts the fleet pricing (one row per setting).
func (s *Store) SetFleetPricing(p FleetPricing) error {
	if p.Currency == "" {
		p.Currency = "€"
	}
	pairs := [][2]string{
		{fsChatInPer1M, strconv.FormatFloat(p.ChatInPer1M, 'f', -1, 64)},
		{fsChatOutPer1M, strconv.FormatFloat(p.ChatOutPer1M, 'f', -1, 64)},
		{fsIngestPer1M, strconv.FormatFloat(p.IngestPer1M, 'f', -1, 64)},
		{fsCurrency, p.Currency},
	}
	for _, kv := range pairs {
		if _, err := s.db.Exec(
			`INSERT INTO fleet_settings(key,value) VALUES(?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// GetFleetPricing reads the fleet pricing, defaulting to zero prices + euro.
func (s *Store) GetFleetPricing() (FleetPricing, error) {
	p := FleetPricing{Currency: "€"}
	rows, err := s.db.Query(`SELECT key,value FROM fleet_settings WHERE key IN (?,?,?,?)`,
		fsChatInPer1M, fsChatOutPer1M, fsIngestPer1M, fsCurrency)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return p, err
		}
		switch k {
		case fsChatInPer1M:
			p.ChatInPer1M, _ = strconv.ParseFloat(v, 64)
		case fsChatOutPer1M:
			p.ChatOutPer1M, _ = strconv.ParseFloat(v, 64)
		case fsIngestPer1M:
			p.IngestPer1M, _ = strconv.ParseFloat(v, 64)
		case fsCurrency:
			if v != "" {
				p.Currency = v
			}
		}
	}
	return p, rows.Err()
}

// PeriodCost holds token totals + estimated cost for a time window.
type PeriodCost struct {
	ChatIn  int     `json:"chat_in"`
	ChatOut int     `json:"chat_out"`
	Ingest  int     `json:"ingest"`
	Cost    float64 `json:"cost"`
}

// CostSummary is the fleet-wide cost view: today / last 7 days / month-to-date,
// plus a month-end forecast (MTD cost extrapolated at the current run-rate).
type CostSummary struct {
	Currency        string     `json:"currency"`
	Today           PeriodCost `json:"today"`
	Week            PeriodCost `json:"week"`
	Month           PeriodCost `json:"month"`
	RunRatePerDay   float64    `json:"run_rate_per_day"`
	DaysElapsed     int        `json:"days_elapsed"`
	DaysInMonth     int        `json:"days_in_month"`
	ForecastEOMCost float64    `json:"forecast_eom_cost"`
}

// TenantCost is one tenant's month-to-date usage + cost.
type TenantCost struct {
	Account  string     `json:"account"`
	TenantID string     `json:"tenant_id"`
	Month    PeriodCost `json:"month"`
}

func (p FleetPricing) cost(chatIn, chatOut, ingest int) float64 {
	return float64(chatIn)/1e6*p.ChatInPer1M +
		float64(chatOut)/1e6*p.ChatOutPer1M +
		float64(ingest)/1e6*p.IngestPer1M
}

// daysInMonth returns the number of days in t's month (day 0 of next month ==
// the last day of this one).
func daysInMonth(t time.Time) int {
	return time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
}

func (s *Store) sumSince(startDay string) (chatIn, chatOut, ingest int, err error) {
	err = s.db.QueryRow(`
SELECT COALESCE(SUM(chat_in),0), COALESCE(SUM(chat_out),0), COALESCE(SUM(ingest),0)
FROM tenant_usage_snapshots WHERE day >= ?`, startDay).Scan(&chatIn, &chatOut, &ingest)
	return
}

// GlobalCostSummary aggregates all tenants' snapshots into today / last-7-days /
// month-to-date token + cost totals, plus a month-end forecast at the current
// run-rate (MTD cost / days elapsed × days in month).
func (s *Store) GlobalCostSummary(now time.Time) (CostSummary, error) {
	pricing, err := s.GetFleetPricing()
	if err != nil {
		return CostSummary{}, err
	}
	day := now.Format("2006-01-02")
	weekStart := now.AddDate(0, 0, -6).Format("2006-01-02")
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")

	cs := CostSummary{Currency: pricing.Currency}
	windows := []struct {
		start string
		p     *PeriodCost
	}{{day, &cs.Today}, {weekStart, &cs.Week}, {monthStart, &cs.Month}}
	for _, w := range windows {
		ci, co, ing, err := s.sumSince(w.start)
		if err != nil {
			return cs, err
		}
		*w.p = PeriodCost{ChatIn: ci, ChatOut: co, Ingest: ing, Cost: pricing.cost(ci, co, ing)}
	}
	cs.DaysInMonth = daysInMonth(now)
	cs.DaysElapsed = now.Day()
	if cs.DaysElapsed > 0 {
		cs.RunRatePerDay = cs.Month.Cost / float64(cs.DaysElapsed)
		cs.ForecastEOMCost = cs.RunRatePerDay * float64(cs.DaysInMonth)
	}
	return cs, nil
}

// PerTenantCost returns each tenant's month-to-date usage + cost, sorted by cost
// descending (biggest spenders first).
func (s *Store) PerTenantCost(now time.Time) ([]TenantCost, error) {
	pricing, err := s.GetFleetPricing()
	if err != nil {
		return nil, err
	}
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
	rows, err := s.db.Query(`
SELECT tenant_id, account_number,
       COALESCE(SUM(chat_in),0), COALESCE(SUM(chat_out),0), COALESCE(SUM(ingest),0)
FROM tenant_usage_snapshots
WHERE day >= ?
GROUP BY tenant_id, account_number`, monthStart)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TenantCost
	for rows.Next() {
		var tc TenantCost
		var ci, co, ing int
		if err := rows.Scan(&tc.TenantID, &tc.Account, &ci, &co, &ing); err != nil {
			return nil, err
		}
		tc.Month = PeriodCost{ChatIn: ci, ChatOut: co, Ingest: ing, Cost: pricing.cost(ci, co, ing)}
		out = append(out, tc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Month.Cost > out[j].Month.Cost })
	return out, nil
}

// LatestCapture returns the most recent snapshot capture time (RFC3339), or ""
// when no snapshots exist yet — drives the dashboard's "updated Xs ago" freshness.
func (s *Store) LatestCapture() (string, error) {
	var v sql.NullString
	if err := s.db.QueryRow(`SELECT MAX(captured_at) FROM tenant_usage_snapshots`).Scan(&v); err != nil {
		return "", err
	}
	if v.Valid {
		return v.String, nil
	}
	return "", nil
}
