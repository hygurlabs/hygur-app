package main

import "testing"

// TestPivotDays: chat keeps direction; embedding + indexing fold into ingest.
func TestPivotDays(t *testing.T) {
	days := []dumpDay{
		{Day: "2026-06-10", Category: "chat", TokensIn: 100, TokensOut: 50},
		{Day: "2026-06-10", Category: "embedding", TokensIn: 10, TokensOut: 0},
		{Day: "2026-06-10", Category: "indexing", TokensIn: 5, TokensOut: 0},
		{Day: "2026-06-11", Category: "chat", TokensIn: 7, TokensOut: 3},
	}
	got := pivotDays(days)
	if len(got) != 2 {
		t.Fatalf("days: got %d, want 2", len(got))
	}
	if got["2026-06-10"] != [3]int{100, 50, 15} {
		t.Fatalf("2026-06-10: got %v, want [100 50 15]", got["2026-06-10"])
	}
	if got["2026-06-11"] != [3]int{7, 3, 0} {
		t.Fatalf("2026-06-11: got %v, want [7 3 0]", got["2026-06-11"])
	}
}
