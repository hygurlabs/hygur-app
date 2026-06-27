package controlplane

import "time"

// FleetStats is the operator dashboard's lifecycle view of the tenant fleet:
// how many tenants are live, suspended (unpaid, data still on disk), or reaped,
// plus the storage-retention signal the operator needs — how many accounts
// stopped paying but still hold data, and how long the oldest has waited.
type FleetStats struct {
	// Provision-state counts (stripe_subscriptions.provision_state).
	Live      int `json:"live"`      // pending + ready (mirrors CountActiveTenants)
	Suspended int `json:"suspended"` // suspend + suspended: pod scaled-to-0, data retained
	Reaped    int `json:"reaped"`    // gone: crypto-shredded, within the 30-day purge window

	// Account-status counts (accounts.status).
	Active   int `json:"active"`
	Trialing int `json:"trialing"`
	PastDue  int `json:"past_due"`
	Canceled int `json:"canceled"`
	Total    int `json:"total"`

	// PayingTenants = active accounts that came through Stripe (have a subscription
	// row), i.e. real paying customers — excludes hand-provisioned operator
	// instances (home/operator). The dashboard multiplies it by the flat monthly
	// price for a Stripe-free MRR estimate.
	PayingTenants int `json:"paying_tenants"`

	// Retention signal: accounts that stopped paying (past_due) but still hold
	// data (not yet reaped) — the set the operator must eventually purge for GDPR
	// hygiene. OldestUnpaidDays = age of the longest-unpaid one (now − valid_until).
	UnpaidRetained   int `json:"unpaid_retained"`
	OldestUnpaidDays int `json:"oldest_unpaid_days"`

	// Churn = canceled / (active + canceled), 0..1 (0 when there's no paying base).
	ChurnRatio float64 `json:"churn_ratio"`
}

// FleetStats computes the operator dashboard's fleet lifecycle view from the
// provision-state table and the accounts table. Cheap: two small aggregates over
// tables that hold one row per subscription / account.
func (s *Store) FleetStats(now time.Time) (FleetStats, error) {
	var f FleetStats

	// Provision-state breakdown (what's running / retained / shredded).
	rows, err := s.db.Query(`SELECT provision_state, COUNT(*) FROM stripe_subscriptions GROUP BY provision_state`)
	if err != nil {
		return f, err
	}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			rows.Close()
			return f, err
		}
		switch state {
		case "pending", "ready":
			f.Live += n
		case "suspend", "suspended":
			f.Suspended += n
		case "gone":
			f.Reaped += n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return f, err
	}

	// Account-status breakdown + the unpaid-retained retention signal. Reuses the
	// shared account scan (valid_until already parsed) so there's no date-format
	// drift between writer and reader.
	accounts, err := s.ListAccounts()
	if err != nil {
		return f, err
	}
	var oldestUnpaid *time.Time
	for _, a := range accounts {
		f.Total++
		switch a.Status {
		case "active":
			f.Active++
		case "trialing":
			f.Trialing++
		case "past_due":
			f.PastDue++
			if a.ValidUntil != nil && (oldestUnpaid == nil || a.ValidUntil.Before(*oldestUnpaid)) {
				oldestUnpaid = a.ValidUntil
			}
		case "canceled":
			f.Canceled++
		}
	}
	f.UnpaidRetained = f.PastDue
	if oldestUnpaid != nil {
		if d := int(now.Sub(*oldestUnpaid).Hours() / 24); d > 0 {
			f.OldestUnpaidDays = d
		}
	}
	if base := f.Active + f.Canceled; base > 0 {
		f.ChurnRatio = float64(f.Canceled) / float64(base)
	}

	// Paying tenants for the MRR estimate: active accounts that originated from a
	// Stripe checkout (have a subscription row). The EXISTS clause drops the
	// hand-provisioned operator instances, which have no stripe_subscriptions row.
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM accounts a WHERE a.status='active' AND EXISTS (` +
			`SELECT 1 FROM stripe_subscriptions ss WHERE ss.account_number = a.account_number)`,
	).Scan(&f.PayingTenants); err != nil {
		return f, err
	}
	return f, nil
}
