package store

import (
	"context"
	"testing"
	"time"
)

func TestChatTokensThisMonth(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if got, _ := db.ChatTokensThisMonth(ctx); got != 0 {
		t.Fatalf("empty = %d, want 0", got)
	}
	mustRec := func(cat string, in, out int) {
		if err := db.RecordTokenUsage(ctx, cat, in, out); err != nil {
			t.Fatalf("record %s: %v", cat, err)
		}
	}
	mustRec(TokenCategoryChat, 1000, 200)
	mustRec(TokenCategoryChat, 500, 100)
	mustRec(TokenCategoryEmbedding, 9999, 0) // excluded from the LLM cap
	mustRec(TokenCategoryIndexing, 8888, 0)  // excluded

	got, err := db.ChatTokensThisMonth(ctx)
	if err != nil {
		t.Fatalf("ChatTokensThisMonth: %v", err)
	}
	if got != 1800 {
		t.Fatalf("chat this month = %d, want 1800 (chat only, in+out)", got)
	}
}

// The 429-incident proof: a batch of background/ingest-tagged completions must
// NOT move the chat cap counters (ChatTokensToday / ChatTokensThisMonth) — that
// runaway background work exhausting the user's Ask cap was the real incident.
// A chat-tagged completion still moves them. WP16a.
func TestBackgroundIngestUsageNeverTouchesChatCap(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// Simulate a burst of background + ingest work (chronicle, briefs, claims,
	// tier2, memory extraction, a ?model=main backfill…). Mirror the sink: each
	// records both the cap row and the per-pass detail row.
	mustRec := func(cat, pass string, in, out int) {
		if err := db.RecordTokenUsage(ctx, cat, in, out); err != nil {
			t.Fatalf("record %s: %v", cat, err)
		}
		if err := db.RecordTokenUsagePass(ctx, cat, pass, in, out); err != nil {
			t.Fatalf("record pass %s/%s: %v", cat, pass, err)
		}
	}
	mustRec("background", "chronicle_act", 50000, 20000)
	mustRec("background", "daily_brief", 30000, 10000)
	mustRec("background", "memory_extract", 5000, 1000)
	mustRec("ingest", "tier2", 80000, 4000) // e.g. a ?model=main backfill
	mustRec("ingest", "claims", 40000, 2000)

	// The chat cap must be pristine after all that background/ingest work.
	if got, _ := db.ChatTokensToday(ctx); got != 0 {
		t.Fatalf("ChatTokensToday after background/ingest burst = %d, want 0", got)
	}
	if got, _ := db.ChatTokensThisMonth(ctx); got != 0 {
		t.Fatalf("ChatTokensThisMonth after background/ingest burst = %d, want 0", got)
	}

	// A genuine chat turn DOES move the cap counters.
	mustRec("chat", "ask", 1000, 400)
	if got, _ := db.ChatTokensToday(ctx); got != 1400 {
		t.Fatalf("ChatTokensToday after chat turn = %d, want 1400", got)
	}
	if got, _ := db.ChatTokensThisMonth(ctx); got != 1400 {
		t.Fatalf("ChatTokensThisMonth after chat turn = %d, want 1400", got)
	}
}

// The per-pass detail feeds /usage/by-pass and must aggregate independently of
// the cap table (which stays the source of truth for the caps).
func TestPassUsageSinceAggregates(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// Same (category, pass) accumulates; distinct passes stay separate.
	if err := db.RecordTokenUsagePass(ctx, "background", "chronicle_act", 100, 40); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordTokenUsagePass(ctx, "background", "chronicle_act", 50, 10); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordTokenUsagePass(ctx, "ingest", "tier2", 200, 5); err != nil {
		t.Fatal(err)
	}
	// Negative/zero is a no-op.
	if err := db.RecordTokenUsagePass(ctx, "chat", "ask", 0, 0); err != nil {
		t.Fatal(err)
	}

	today := time.Now().Format("2006-01-02")
	rows, err := db.PassUsageSince(ctx, today)
	if err != nil {
		t.Fatalf("PassUsageSince: %v", err)
	}
	got := map[string]PassUsage{}
	for _, r := range rows {
		got[r.Category+"/"+r.Pass] = r
	}
	if c := got["background/chronicle_act"]; c.TokensIn != 150 || c.TokensOut != 50 {
		t.Errorf("chronicle_act = (%d,%d), want (150,50)", c.TokensIn, c.TokensOut)
	}
	if c := got["ingest/tier2"]; c.TokensIn != 200 || c.TokensOut != 5 {
		t.Errorf("tier2 = (%d,%d), want (200,5)", c.TokensIn, c.TokensOut)
	}
	if _, ok := got["chat/ask"]; ok {
		t.Errorf("zero-count pass should not have been recorded")
	}
}

func TestRecordTokenUsageAccumulates(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// Two records on the same day+category must accumulate (UPSERT).
	if err := db.RecordTokenUsage(ctx, TokenCategoryChat, 100, 40); err != nil {
		t.Fatalf("record 1: %v", err)
	}
	if err := db.RecordTokenUsage(ctx, TokenCategoryChat, 50, 10); err != nil {
		t.Fatalf("record 2: %v", err)
	}
	if err := db.RecordTokenUsage(ctx, TokenCategoryEmbedding, 200, 0); err != nil {
		t.Fatalf("record embedding: %v", err)
	}

	// Zero / negative counts are no-ops.
	if err := db.RecordTokenUsage(ctx, TokenCategoryChat, 0, 0); err != nil {
		t.Fatalf("record zero: %v", err)
	}

	today := time.Now().Format("2006-01-02")
	rows, err := db.TokenUsageSince(ctx, today)
	if err != nil {
		t.Fatalf("TokenUsageSince: %v", err)
	}

	got := map[string]CategoryUsage{}
	for _, r := range rows {
		got[r.Category] = r
	}
	if c := got[TokenCategoryChat]; c.TokensIn != 150 || c.TokensOut != 50 {
		t.Errorf("chat = (%d,%d), want (150,50)", c.TokensIn, c.TokensOut)
	}
	if e := got[TokenCategoryEmbedding]; e.TokensIn != 200 || e.TokensOut != 0 {
		t.Errorf("embedding = (%d,%d), want (200,0)", e.TokensIn, e.TokensOut)
	}
}

func TestTokenUsageSinceFiltersByDay(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// Seed an old row directly (RecordTokenUsage always uses "today").
	if _, err := db.db.ExecContext(ctx,
		`INSERT INTO token_usage (day, category, tokens_in, tokens_out) VALUES (?,?,?,?)`,
		"2000-01-01", TokenCategoryChat, 999, 999); err != nil {
		t.Fatalf("seed old: %v", err)
	}
	if err := db.RecordTokenUsage(ctx, TokenCategoryChat, 10, 5); err != nil {
		t.Fatalf("record today: %v", err)
	}

	today := time.Now().Format("2006-01-02")
	rows, err := db.TokenUsageSince(ctx, today)
	if err != nil {
		t.Fatalf("TokenUsageSince: %v", err)
	}
	if len(rows) != 1 || rows[0].TokensIn != 10 || rows[0].TokensOut != 5 {
		t.Errorf("expected only today's (10,5), got %+v", rows)
	}
}

func TestPricingRoundtripAndDefaults(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// Defaults: zero prices, euro currency.
	p, err := db.GetPricing(ctx)
	if err != nil {
		t.Fatalf("GetPricing default: %v", err)
	}
	if p.ChatInPer1M != 0 || p.ChatOutPer1M != 0 || p.IngestPer1M != 0 {
		t.Errorf("default prices should be 0, got %+v", p)
	}
	if p.Currency == "" {
		t.Error("default currency should not be empty")
	}

	want := Pricing{ChatInPer1M: 2, ChatOutPer1M: 6, IngestPer1M: 0.13, Currency: "$"}
	if err := db.SetPricing(ctx, want); err != nil {
		t.Fatalf("SetPricing: %v", err)
	}
	got, err := db.GetPricing(ctx)
	if err != nil {
		t.Fatalf("GetPricing: %v", err)
	}
	if got != want {
		t.Errorf("pricing roundtrip = %+v, want %+v", got, want)
	}

	// Negative prices are clamped to zero on save.
	if err := db.SetPricing(ctx, Pricing{ChatInPer1M: -5}); err != nil {
		t.Fatalf("SetPricing negative: %v", err)
	}
	got2, _ := db.GetPricing(ctx)
	if got2.ChatInPer1M != 0 {
		t.Errorf("negative price should clamp to 0, got %v", got2.ChatInPer1M)
	}
}
