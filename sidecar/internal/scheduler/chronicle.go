package scheduler

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// Chronicle — Hygur writes a grounded, narrative chronicle of the user's life,
// one dated act per night, in continuity (rolling synopsis + watermark) so it
// never re-narrates. v1: the always-open "life" chapter only.

const (
	chronicleLifeChapterID = "life"
	chronicleMaxTraces     = 40  // cap the input traces per act
	chronicleTraceCharCap  = 220 // per-trace snippet length
	chronicleActMaxTokens  = 700 // act + synopsis (marker-delimited), bounded
	// The entry covers a window by REAL (canonical) date, not ingestion: a 2024
	// mail synced today, or a future calendar event, must NOT land in "today".
	chronicleFirstRunDays = 7  // first entry covers ~the last week of real activity
	chronicleLookbackDays = 60 // ingestion lookback to gather candidates, then filter by canonical date
	chronicleCandidates   = 400
	chronicleSynopsisMarker = "===SYNOPSIS==="
)

// chronicleSystemPrompt — narrative voice, strictly grounded (anti-fiction is the
// prime directive), continuity via the synopsis, and a marker-delimited synopsis
// update. Generic principles, no enumerated cases.
const chronicleSystemPrompt = `You are Hygur, quietly chronicling the user's life from their own records. Write the next entry of an ongoing chronicle.

You are given THE STORY SO FAR (a short synopsis) and NEW TRACES from the recent period (mail, notes, events — dated). Write ONE entry, in flowing narrative prose — a short-story / journal voice, never a bullet list. Recount what happened in this period as a story: the people, the decisions, what moved, what is still pending.

Rules:
- Use ONLY the synopsis and the traces. NEVER invent an event, name, date, figure or outcome. If the traces are thin, write a short entry; if there is nothing of substance, write nothing at all.
- Chronicle ONLY what happened in this period (the dates shown in the traces). Do NOT narrate anything dated outside it, and NEVER speculate about the future.
- Continue the narrative: REFERENCE the past (from the synopsis) but do NOT re-tell it. Cover only what is new.
- A sober, readable register in your own plain voice. Do NOT imitate any author's style.
- No heading, no preamble — only the prose of the entry.

After the entry, output a line containing exactly ` + chronicleSynopsisMarker + ` and then an updated "story so far" (<= 120 words): the durable threads and current state, folding in this entry. Keep it tight.`

// ChronicleWriter appends nightly acts to a chapter.
type ChronicleWriter struct {
	store  *store.DB
	llm    *llm.Client
	logger zerolog.Logger
}

// NewChronicleWriter builds the writer; nil if a dependency is missing.
func NewChronicleWriter(db *store.DB, client *llm.Client, logger zerolog.Logger) *ChronicleWriter {
	if db == nil || client == nil {
		return nil
	}
	return &ChronicleWriter{store: db, llm: client, logger: logger.With().Str("component", "chronicle").Logger()}
}

// WriteLifeChapter appends tonight's act to the "life" chapter from traces ingested
// since the watermark. force regenerates today's act (manual trigger). Returns the
// act text ("" when nothing was written).
func (w *ChronicleWriter) WriteLifeChapter(ctx context.Context, now time.Time, force bool) (string, error) {
	if w == nil {
		return "", nil
	}
	chap, err := w.store.GetChronicleChapter(ctx, chronicleLifeChapterID)
	if err != nil {
		return "", err
	}
	if chap == nil {
		chap = &store.ChronicleChapter{ID: chronicleLifeChapterID, Title: "Life", Status: "open"}
	}

	today := now.UTC().Format("2006-01-02")
	actID := "chronicle:" + chronicleLifeChapterID + ":" + today
	if !force {
		if existing, _ := w.store.GetKnowledgeItem(ctx, actID); existing != nil {
			return "", nil // already chronicled today
		}
	}

	// The entry covers (periodStart, now] by REAL (canonical) date. periodStart =
	// the last chronicled date (watermark), but never further back than the
	// first-run window even if the chapter's been idle.
	periodStart := now.AddDate(0, 0, -chronicleFirstRunDays)
	// A manual (force) run re-narrates the recent week from scratch; the nightly
	// run continues from the watermark (the last chronicled date).
	if !force && chap.Watermark != "" {
		if t, e := time.Parse(time.RFC3339, chap.Watermark); e == nil && t.After(periodStart) {
			periodStart = t
		}
	}
	// Gather candidates broadly by ingestion, then keep ONLY those whose canonical
	// (real) date is inside the window and not in the future — so a freshly-synced
	// 2024 mail or an upcoming calendar event never lands in "what just happened".
	candidates, err := w.store.ListKnowledgeItemsSince(ctx, now.AddDate(0, 0, -chronicleLookbackDays),
		store.MailAndSourceTypes(store.SourceTypeNote, store.SourceTypeEvent), chronicleCandidates)
	if err != nil {
		return "", err
	}
	var items []*store.KnowledgeItem
	for _, it := range candidates {
		cd := store.GetCanonicalDate(it)
		if cd.IsZero() || !cd.After(periodStart) || cd.After(now) {
			continue // undated, older than the window, or in the future
		}
		items = append(items, it)
	}
	if len(items) == 0 {
		return "", nil // nothing recent to chronicle
	}
	sort.Slice(items, func(i, j int) bool {
		return store.GetCanonicalDate(items[i]).Before(store.GetCanonicalDate(items[j]))
	})
	if len(items) > chronicleMaxTraces {
		items = items[len(items)-chronicleMaxTraces:] // keep the most recent within the window
	}

	periodLabel := periodStart.Format("2 Jan") + " – " + now.Format("2 Jan 2006")
	act, synopsis, gerr := w.generate(ctx, chap.Synopsis, buildChronicleTraces(items), periodLabel)
	if gerr != nil {
		// Surface the failure (the manual run shows it); don't advance the
		// watermark so the next run retries the same period.
		return "", fmt.Errorf("chronicle generation failed: %w", gerr)
	}
	// Advance the watermark to now → this period is never re-narrated.
	chap.Watermark = now.UTC().Format(time.RFC3339)

	if strings.TrimSpace(act) == "" {
		// Nothing of substance — still advance the watermark + synopsis stays.
		_ = w.store.UpsertChronicleChapter(ctx, chap)
		return "", nil
	}

	if force {
		_ = w.store.DeleteKnowledgeItem(ctx, actID)
	}
	if err := w.store.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
		ContentID:      actID,
		SourceType:     store.SourceTypeChronicleAct,
		Title:          now.Format("2 January 2006"),
		NormalizedText: act,
		Metadata: map[string]any{
			"chapter_id":     chronicleLifeChapterID,
			"act_date":       today,
			"canonical_date": now.UTC().Format(time.RFC3339),
		},
		VersionID: today,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return "", err
	}

	if s := strings.TrimSpace(synopsis); s != "" {
		chap.Synopsis = s
	}
	if err := w.store.UpsertChronicleChapter(ctx, chap); err != nil {
		return "", err
	}
	w.logger.Info().Str("act", actID).Int("traces", len(items)).Msg("chronicle act written")
	return act, nil
}

// generate runs the bounded LLM call → (act, updatedSynopsis, err). Streamed on
// the no-timeout client: a long narrative on a slow backend must not be cut by the
// 30s request timeout (the chronicle is a background task). An empty updatedSynopsis
// means "keep the previous one" (parse miss / model didn't emit the marker).
func (w *ChronicleWriter) generate(ctx context.Context, synopsis, traces, dateLabel string) (string, string, error) {
	storySoFar := strings.TrimSpace(synopsis)
	if storySoFar == "" {
		storySoFar = "(this is the first entry — there is no prior story yet)"
	}
	user := fmt.Sprintf("STORY SO FAR:\n%s\n\nNEW TRACES (period %s):\n%s", storySoFar, dateLabel, traces)
	var sb strings.Builder
	err := w.llm.StreamChat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: chronicleSystemPrompt},
			{Role: "user", Content: user},
		},
		Temperature:        0.5, // narrative needs a little life, still grounded
		MaxTokens:          chronicleActMaxTokens,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	}, func(delta string, _ bool, _ *llm.Usage) error {
		sb.WriteString(delta)
		return nil
	})
	if err != nil {
		w.logger.Warn().Err(err).Msg("chronicle generate failed")
		return "", "", err
	}
	parts := strings.SplitN(sb.String(), chronicleSynopsisMarker, 2)
	act := strings.TrimSpace(parts[0])
	newSynopsis := ""
	if len(parts) == 2 {
		newSynopsis = strings.TrimSpace(parts[1])
	}
	return act, newSynopsis, nil
}

// buildChronicleTraces renders the new items as dated, capped trace lines.
func buildChronicleTraces(items []*store.KnowledgeItem) string {
	var b strings.Builder
	for _, it := range items {
		if it == nil {
			continue
		}
		date := ""
		if t := store.GetCanonicalDate(it); !t.IsZero() {
			date = t.Format("2006-01-02")
		}
		snippet := strings.ReplaceAll(strings.TrimSpace(it.NormalizedText), "\n", " ")
		if len(snippet) > chronicleTraceCharCap {
			snippet = snippet[:chronicleTraceCharCap] + "…"
		}
		fmt.Fprintf(&b, "- [%s] %s — %s\n", date, strings.TrimSpace(it.Title), snippet)
	}
	return b.String()
}

// ChronicleScheduler runs the nightly chronicle write once a day at the night hour.
type ChronicleScheduler struct {
	writer *ChronicleWriter
	hour   int
	logger zerolog.Logger
}

// NewChronicleScheduler builds the loop; nil when the writer is missing.
func NewChronicleScheduler(writer *ChronicleWriter, nightHour int, logger zerolog.Logger) *ChronicleScheduler {
	if writer == nil {
		return nil
	}
	if nightHour < 0 || nightHour > 23 {
		nightHour = 22
	}
	return &ChronicleScheduler{writer: writer, hour: nightHour, logger: logger.With().Str("component", "chronicle_scheduler").Logger()}
}

// Start launches the loop. Hourly tick; writes once at the night hour (the
// act-per-day content_id makes a same-hour double-fire a no-op anyway).
func (s *ChronicleScheduler) Start(ctx context.Context) {
	if s == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				if now.Hour() != s.hour {
					continue
				}
				if _, err := s.writer.WriteLifeChapter(ctx, now, false); err != nil {
					s.logger.Debug().Err(err).Msg("nightly chronicle failed")
				}
			}
		}
	}()
}
