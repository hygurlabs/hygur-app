package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// UsageHandler serves LLM token-usage aggregates and the cost pricing settings.
type UsageHandler struct {
	db     *store.DB
	logger zerolog.Logger
}

// NewUsageHandler creates a UsageHandler backed by the given store.
func NewUsageHandler(db *store.DB, logger zerolog.Logger) *UsageHandler {
	return &UsageHandler{db: db, logger: logger}
}

// periodUsage is the per-period token breakdown returned to the UI. Chat keeps
// IN/OUT separate (priced per direction); embeddings and indexing are reported
// as total tokens each (they share one ingest price).
type periodUsage struct {
	ChatIn    int `json:"chat_in"`
	ChatOut   int `json:"chat_out"`
	Embedding int `json:"embedding"`
	Indexing  int `json:"indexing"`
	// Totals across all categories — the input/output token budget the inference
	// box actually sees (drives the usage gauge against the monthly caps).
	TotalIn  int `json:"total_in"`
	TotalOut int `json:"total_out"`
}

type usageResponse struct {
	Currency string                 `json:"currency"`
	Pricing  store.Pricing          `json:"pricing"`
	Periods  map[string]periodUsage `json:"periods"`
}

// GetTokens returns token sums for today / this week / this month plus the
// stored pricing. Cost is computed client-side so it updates live as the user
// edits the price fields.
func (h *UsageHandler) GetTokens(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	starts := map[string]string{
		"today":      now.Format("2006-01-02"),
		"this_week":  weekStart(now).Format("2006-01-02"),
		"this_month": monthStart(now).Format("2006-01-02"),
	}

	out := usageResponse{Periods: make(map[string]periodUsage, len(starts))}
	for name, start := range starts {
		rows, err := h.db.TokenUsageSince(ctx, start)
		if err != nil {
			h.logger.Warn().Err(err).Str("period", name).Msg("token usage query failed")
			http.Error(w, `{"error":"token usage query failed"}`, http.StatusInternalServerError)
			return
		}
		var p periodUsage
		for _, row := range rows {
			p.TotalIn += row.TokensIn
			p.TotalOut += row.TokensOut
			switch row.Category {
			case store.TokenCategoryChat:
				p.ChatIn += row.TokensIn
				p.ChatOut += row.TokensOut
			case store.TokenCategoryEmbedding:
				p.Embedding += row.TokensIn + row.TokensOut
			case store.TokenCategoryIndexing:
				p.Indexing += row.TokensIn + row.TokensOut
			}
		}
		out.Periods[name] = p
	}

	pricing, err := h.db.GetPricing(ctx)
	if err != nil {
		h.logger.Warn().Err(err).Msg("pricing read failed")
		http.Error(w, `{"error":"pricing read failed"}`, http.StatusInternalServerError)
		return
	}
	out.Pricing = pricing
	out.Currency = pricing.Currency

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// SetPricing persists the per-1M token prices. Stored in the DB (not the YAML
// config) so editing them never triggers a sidecar restart.
func (h *UsageHandler) SetPricing(w http.ResponseWriter, r *http.Request) {
	var p store.Pricing
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, `{"error":"invalid pricing body"}`, http.StatusBadRequest)
		return
	}
	if err := h.db.SetPricing(r.Context(), p); err != nil {
		h.logger.Warn().Err(err).Msg("pricing save failed")
		http.Error(w, `{"error":"pricing save failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// weekStart returns midnight on the Monday of t's week, in t's location.
func weekStart(t time.Time) time.Time {
	offset := (int(t.Weekday()) + 6) % 7 // Monday = 0
	y, m, d := t.AddDate(0, 0, -offset).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// monthStart returns midnight on the first day of t's month, in t's location.
func monthStart(t time.Time) time.Time {
	y, m, _ := t.Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, t.Location())
}
