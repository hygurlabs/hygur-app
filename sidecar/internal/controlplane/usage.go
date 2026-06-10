package controlplane

import (
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
