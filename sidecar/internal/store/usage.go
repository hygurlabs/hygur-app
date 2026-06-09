package store

import (
	"context"
	"strconv"
	"time"
)

// Token usage categories recorded by RecordTokenUsage.
const (
	TokenCategoryChat      = "chat"
	TokenCategoryEmbedding = "embedding"
	TokenCategoryIndexing  = "indexing"
)

// CategoryUsage holds summed token counts for one category over a period.
type CategoryUsage struct {
	Category  string `json:"category"`
	TokensIn  int    `json:"tokens_in"`
	TokensOut int    `json:"tokens_out"`
}

// RecordTokenUsage adds token counts to the running daily total for a category.
// It is an UPSERT on (day, category) keyed by the local calendar date, so the
// table holds at most a handful of rows per day. A no-op when both counts are
// non-positive.
func (d *DB) RecordTokenUsage(ctx context.Context, category string, tokensIn, tokensOut int) error {
	if category == "" || (tokensIn <= 0 && tokensOut <= 0) {
		return nil
	}
	day := time.Now().Format("2006-01-02")
	_, err := d.db.ExecContext(ctx, `
INSERT INTO token_usage (day, category, tokens_in, tokens_out)
VALUES (?, ?, ?, ?)
ON CONFLICT(day, category) DO UPDATE SET
    tokens_in  = tokens_in  + excluded.tokens_in,
    tokens_out = tokens_out + excluded.tokens_out`,
		day, category, clampNonNeg(tokensIn), clampNonNeg(tokensOut))
	return err
}

// ResetTokenUsage clears all recorded token usage (the running daily totals).
// Pricing settings in app_settings are untouched. Returns the rows removed.
func (d *DB) ResetTokenUsage(ctx context.Context) (int64, error) {
	res, err := d.db.ExecContext(ctx, `DELETE FROM token_usage`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// TokenUsageSince returns per-category token sums for all days on or after
// startDay (inclusive, formatted 'YYYY-MM-DD').
func (d *DB) TokenUsageSince(ctx context.Context, startDay string) ([]CategoryUsage, error) {
	rows, err := d.db.QueryContext(ctx, `
SELECT category, COALESCE(SUM(tokens_in), 0), COALESCE(SUM(tokens_out), 0)
FROM token_usage
WHERE day >= ?
GROUP BY category`, startDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CategoryUsage
	for rows.Next() {
		var c CategoryUsage
		if err := rows.Scan(&c.Category, &c.TokensIn, &c.TokensOut); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ChatTokensThisMonth returns total chat (LLM) tokens — prompt + completion —
// recorded since the first day of the current calendar month. Embedding/indexing
// (ingestion) tokens are excluded: they're a separate, much cheaper budget. This
// drives the per-tenant monthly LLM cap that protects margin under per-token
// inference pricing.
func (d *DB) ChatTokensThisMonth(ctx context.Context) (int, error) {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
	cats, err := d.TokenUsageSince(ctx, start)
	if err != nil {
		return 0, err
	}
	for _, c := range cats {
		if c.Category == TokenCategoryChat {
			return c.TokensIn + c.TokensOut, nil
		}
	}
	return 0, nil
}

// Pricing holds the per-1M-token prices used to estimate cost. Chat is billed
// per direction; embeddings + indexing share a single ingest price.
type Pricing struct {
	ChatInPer1M  float64 `json:"chat_in_per_1m"`
	ChatOutPer1M float64 `json:"chat_out_per_1m"`
	IngestPer1M  float64 `json:"ingest_per_1m"`
	Currency     string  `json:"currency"`
}

const (
	settingPriceChatIn   = "price_chat_in_per_1m"
	settingPriceChatOut  = "price_chat_out_per_1m"
	settingPriceIngest   = "price_ingest_per_1m"
	settingPriceCurrency = "price_currency"
	defaultCurrency      = "€" // €
)

// GetPricing reads the stored pricing, defaulting to zero prices and the euro
// symbol when nothing has been saved yet.
func (d *DB) GetPricing(ctx context.Context) (Pricing, error) {
	p := Pricing{Currency: defaultCurrency}
	rows, err := d.db.QueryContext(ctx,
		`SELECT key, value FROM app_settings WHERE key IN (?, ?, ?, ?)`,
		settingPriceChatIn, settingPriceChatOut, settingPriceIngest, settingPriceCurrency)
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
		case settingPriceChatIn:
			p.ChatInPer1M, _ = strconv.ParseFloat(v, 64)
		case settingPriceChatOut:
			p.ChatOutPer1M, _ = strconv.ParseFloat(v, 64)
		case settingPriceIngest:
			p.IngestPer1M, _ = strconv.ParseFloat(v, 64)
		case settingPriceCurrency:
			if v != "" {
				p.Currency = v
			}
		}
	}
	return p, rows.Err()
}

// SetPricing persists the pricing values, UPSERTing each into app_settings.
func (d *DB) SetPricing(ctx context.Context, p Pricing) error {
	if p.Currency == "" {
		p.Currency = defaultCurrency
	}
	pairs := [][2]string{
		{settingPriceChatIn, strconv.FormatFloat(clampNonNegF(p.ChatInPer1M), 'f', -1, 64)},
		{settingPriceChatOut, strconv.FormatFloat(clampNonNegF(p.ChatOutPer1M), 'f', -1, 64)},
		{settingPriceIngest, strconv.FormatFloat(clampNonNegF(p.IngestPer1M), 'f', -1, 64)},
		{settingPriceCurrency, p.Currency},
	}
	for _, kv := range pairs {
		if _, err := d.db.ExecContext(ctx,
			`INSERT INTO app_settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

func clampNonNeg(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func clampNonNegF(n float64) float64 {
	if n < 0 {
		return 0
	}
	return n
}
