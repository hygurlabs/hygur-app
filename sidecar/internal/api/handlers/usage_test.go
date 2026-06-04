package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

func newUsageTestHandler(t *testing.T) (*UsageHandler, *store.DB) {
	t.Helper()
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewUsageHandler(db, zerolog.Nop()), db
}

func TestUsageHandler_GetTokensAggregatesByCategory(t *testing.T) {
	h, db := newUsageTestHandler(t)
	ctx := context.Background()

	// Simulate what the llm.Client records during normal operation.
	if err := db.RecordTokenUsage(ctx, store.TokenCategoryChat, 1000, 300); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordTokenUsage(ctx, store.TokenCategoryEmbedding, 5000, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordTokenUsage(ctx, store.TokenCategoryIndexing, 200, 80); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.GetTokens(rec, httptest.NewRequest(http.MethodGet, "/usage/tokens", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}

	var resp usageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, period := range []string{"today", "this_week", "this_month"} {
		p, ok := resp.Periods[period]
		if !ok {
			t.Fatalf("missing period %q", period)
		}
		if p.ChatIn != 1000 || p.ChatOut != 300 {
			t.Errorf("%s chat = (%d,%d), want (1000,300)", period, p.ChatIn, p.ChatOut)
		}
		if p.Embedding != 5000 {
			t.Errorf("%s embedding = %d, want 5000", period, p.Embedding)
		}
		if p.Indexing != 280 { // 200 in + 80 out
			t.Errorf("%s indexing = %d, want 280", period, p.Indexing)
		}
	}
	if resp.Currency == "" {
		t.Error("currency should default to a non-empty symbol")
	}
}

func TestUsageHandler_SetPricingPersists(t *testing.T) {
	h, db := newUsageTestHandler(t)

	body := `{"chat_in_per_1m":2,"chat_out_per_1m":6,"ingest_per_1m":0.1,"currency":"$"}`
	rec := httptest.NewRecorder()
	h.SetPricing(rec, httptest.NewRequest(http.MethodPut, "/usage/pricing", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}

	got, err := db.GetPricing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := store.Pricing{ChatInPer1M: 2, ChatOutPer1M: 6, IngestPer1M: 0.1, Currency: "$"}
	if got != want {
		t.Errorf("persisted pricing = %+v, want %+v", got, want)
	}

	// And it surfaces back through GetTokens.
	rec2 := httptest.NewRecorder()
	h.GetTokens(rec2, httptest.NewRequest(http.MethodGet, "/usage/tokens", nil))
	var resp usageResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Pricing != want {
		t.Errorf("GetTokens pricing = %+v, want %+v", resp.Pricing, want)
	}
}
