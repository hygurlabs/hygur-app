package controlplane

import (
	"testing"
	"time"
)

// FleetStats aggregates the fleet by provision-state and account-status, and
// surfaces the retention signal: how many accounts stopped paying (past_due) but
// still hold data, plus the age of the oldest unpaid one.
func TestFleetStats(t *testing.T) {
	store := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()

	mk := func(sub, email, provState, status string, validUntil *time.Time) {
		t.Helper()
		if _, _, err := store.UpsertSubscriptionAccount(now, sub, "cus_"+sub, "cs_"+sub, email, nil); err != nil {
			t.Fatalf("upsert %s: %v", sub, err)
		}
		if err := store.SetProvisionState(sub, provState); err != nil {
			t.Fatalf("provstate %s: %v", sub, err)
		}
		if err := store.SetSubscriptionBySub(sub, status, validUntil); err != nil {
			t.Fatalf("substatus %s: %v", sub, err)
		}
	}

	unpaidSince := now.Add(-40 * 24 * time.Hour)
	mk("sub_live", "live@b.com", "ready", "active", nil)
	mk("sub_unpaid", "unpaid@b.com", "suspended", "past_due", &unpaidSince)
	mk("sub_gone", "gone@b.com", "gone", "canceled", &now)

	f, err := store.FleetStats(now)
	if err != nil {
		t.Fatalf("FleetStats: %v", err)
	}

	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"Live", f.Live, 1},
		{"Suspended", f.Suspended, 1},
		{"Reaped", f.Reaped, 1},
		{"Active", f.Active, 1},
		{"PastDue", f.PastDue, 1},
		{"Canceled", f.Canceled, 1},
		{"Total", f.Total, 3},
		{"UnpaidRetained", f.UnpaidRetained, 1},
		{"PayingTenants", f.PayingTenants, 1}, // only sub_live is active + Stripe-backed
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if f.OldestUnpaidDays < 39 || f.OldestUnpaidDays > 41 {
		t.Errorf("OldestUnpaidDays = %d, want ~40", f.OldestUnpaidDays)
	}
	if f.ChurnRatio != 0.5 { // canceled 1 / (active 1 + canceled 1)
		t.Errorf("ChurnRatio = %v, want 0.5", f.ChurnRatio)
	}
}
