package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/hygur/sidecar/internal/controlplane"
)

// dumpDay mirrors one row of `hygur usage dump` output (per-day, per-category).
type dumpDay struct {
	Day       string `json:"day"`
	Category  string `json:"category"`
	TokensIn  int    `json:"tokens_in"`
	TokensOut int    `json:"tokens_out"`
}

type usageDump struct {
	Pricing struct {
		ChatInPer1M  float64 `json:"chat_in_per_1m"`
		ChatOutPer1M float64 `json:"chat_out_per_1m"`
		IngestPer1M  float64 `json:"ingest_per_1m"`
		Currency     string  `json:"currency"`
	} `json:"pricing"`
	Days []dumpDay `json:"days"`
}

// pivotDays folds per-category daily rows into per-day [chatIn, chatOut, ingest]:
// chat keeps its direction; embedding + indexing fold into the ingest bucket.
func pivotDays(days []dumpDay) map[string][3]int {
	out := map[string][3]int{}
	for _, d := range days {
		v := out[d.Day]
		switch d.Category {
		case "chat":
			v[0] += d.TokensIn
			v[1] += d.TokensOut
		default: // embedding, indexing
			v[2] += d.TokensIn + d.TokensOut
		}
		out[d.Day] = v
	}
	return out
}

// runConsoleUsage handles `hygur-console usage ingest --tenant ID --account N`:
// read a tenant's `hygur usage dump` JSON on stdin (the on-box poller pipes it
// via kubectl exec), pivot it, and upsert per-day snapshots + the fleet pricing.
func runConsoleUsage(args []string) {
	if len(args) == 0 || args[0] != "ingest" {
		die(fmt.Errorf("usage: hygur-console usage ingest --tenant ID --account N  (reads `hygur usage dump` JSON on stdin)"))
	}
	fs := flag.NewFlagSet("usage ingest", flag.ExitOnError)
	tenant := fs.String("tenant", "", "tenant id (required)")
	account := fs.String("account", "", "account number")
	_ = fs.Parse(args[1:])
	if *tenant == "" {
		die(fmt.Errorf("usage ingest: --tenant is required"))
	}

	var dump usageDump
	if err := json.NewDecoder(os.Stdin).Decode(&dump); err != nil {
		die(fmt.Errorf("usage ingest: decode stdin: %w", err))
	}

	st := openStore()
	defer st.Close()
	now := time.Now()
	byDay := pivotDays(dump.Days)
	for day, v := range byDay {
		if err := st.UpsertTenantUsage(now, controlplane.TenantUsageDay{
			TenantID: *tenant, Account: *account, Day: day,
			ChatIn: v[0], ChatOut: v[1], Ingest: v[2],
		}); err != nil {
			die(fmt.Errorf("usage ingest: upsert %s: %w", day, err))
		}
	}
	// Fleet-wide single price: the latest ingest wins (all tenants share one price).
	p := dump.Pricing
	if p.ChatInPer1M > 0 || p.ChatOutPer1M > 0 || p.IngestPer1M > 0 || p.Currency != "" {
		if err := st.SetFleetPricing(controlplane.FleetPricing{
			ChatInPer1M:  p.ChatInPer1M,
			ChatOutPer1M: p.ChatOutPer1M,
			IngestPer1M:  p.IngestPer1M,
			Currency:     p.Currency,
		}); err != nil {
			die(fmt.Errorf("usage ingest: set pricing: %w", err))
		}
	}
	fmt.Printf("ingested %d day(s) for tenant %s\n", len(byDay), *tenant)
}
