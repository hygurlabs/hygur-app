package controlplane

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTenantUsageSnapshots_AndPricing(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "console.db"), "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	now := time.Now()

	if err := s.UpsertTenantUsage(now, TenantUsageDay{TenantID: "home", Account: "000042", Day: "2026-06-10", ChatIn: 1000, ChatOut: 500, Ingest: 200}); err != nil {
		t.Fatal(err)
	}
	// Re-poll the same day → overwrite, not duplicate.
	if err := s.UpsertTenantUsage(now, TenantUsageDay{TenantID: "home", Account: "000042", Day: "2026-06-10", ChatIn: 1100, ChatOut: 550, Ingest: 220}); err != nil {
		t.Fatal(err)
	}
	var n, chatIn int
	if err := s.db.QueryRow(`SELECT COUNT(*), chat_in FROM tenant_usage_snapshots WHERE tenant_id='home' AND day='2026-06-10'`).Scan(&n, &chatIn); err != nil {
		t.Fatal(err)
	}
	if n != 1 || chatIn != 1100 {
		t.Fatalf("idempotency: got n=%d chat_in=%d, want 1/1100", n, chatIn)
	}

	if err := s.SetFleetPricing(FleetPricing{ChatInPer1M: 2, ChatOutPer1M: 6, IngestPer1M: 0.5, Currency: "€"}); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetFleetPricing()
	if err != nil {
		t.Fatal(err)
	}
	if p.ChatInPer1M != 2 || p.ChatOutPer1M != 6 || p.IngestPer1M != 0.5 || p.Currency != "€" {
		t.Fatalf("pricing round-trip: got %+v", p)
	}

	if _, err := s.ListLiveTenants(); err != nil { // no accounts yet → empty, no error
		t.Fatalf("ListLiveTenants: %v", err)
	}
}

func TestEvaluateFleetBudget(t *testing.T) {
	// Today = 800k CHAT tokens (500k+300k). Ingest is deliberately huge (1M) to
	// prove it is EXCLUDED from the budget total — only chat counts toward the cap.
	today := PeriodCost{ChatIn: 500_000, ChatOut: 300_000, Ingest: 1_000_000}
	cases := []struct {
		name        string
		budget      int
		wantStatus  string
		wantDisable bool
	}{
		{"disabled when budget unset", 0, FleetBudgetOK, true},
		{"ok well under budget", 2_000_000, FleetBudgetOK, false},
		{"warn at 80%", 1_000_000, FleetBudgetWarn, false}, // 800k/1M = 0.80
		{"over at 100%", 800_000, FleetBudgetOver, false},   // 800k/800k = 1.0
		{"over above 100%", 500_000, FleetBudgetOver, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fb := EvaluateFleetBudget(today, c.budget)
			if fb.Status != c.wantStatus {
				t.Fatalf("status = %q, want %q (ratio %.3f)", fb.Status, c.wantStatus, fb.Ratio)
			}
			if fb.TodayTokens != 800_000 {
				t.Fatalf("today tokens = %d, want 800000", fb.TodayTokens)
			}
			if c.wantDisable && fb.Ratio != 0 {
				t.Fatalf("disabled budget should have ratio 0, got %.3f", fb.Ratio)
			}
		})
	}
}

func approxEq(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

func TestCostSummary_AndForecast(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "c.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetFleetPricing(FleetPricing{ChatInPer1M: 2, ChatOutPer1M: 6, IngestPer1M: 0.5, Currency: "€"}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC) // June (30 days), day 10
	// 1M chat-in (=2) + 1M chat-out (=6) + 4M ingest (=2) → cost 10
	if err := s.UpsertTenantUsage(now, TenantUsageDay{TenantID: "home", Account: "42", Day: "2026-06-10", ChatIn: 1_000_000, ChatOut: 1_000_000, Ingest: 4_000_000}); err != nil {
		t.Fatal(err)
	}

	cs, err := s.GlobalCostSummary(now)
	if err != nil {
		t.Fatal(err)
	}
	if !approxEq(cs.Today.Cost, 10) || !approxEq(cs.Month.Cost, 10) {
		t.Fatalf("cost: today=%v month=%v want 10", cs.Today.Cost, cs.Month.Cost)
	}
	if cs.DaysInMonth != 30 || cs.DaysElapsed != 10 {
		t.Fatalf("days: elapsed=%d inMonth=%d want 10/30", cs.DaysElapsed, cs.DaysInMonth)
	}
	if !approxEq(cs.RunRatePerDay, 1) || !approxEq(cs.ForecastEOMCost, 30) {
		t.Fatalf("forecast: runrate=%v eom=%v want 1/30", cs.RunRatePerDay, cs.ForecastEOMCost)
	}

	pt, err := s.PerTenantCost(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pt) != 1 || !approxEq(pt[0].Month.Cost, 10) {
		t.Fatalf("per-tenant: %+v", pt)
	}
}
