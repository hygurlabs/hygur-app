package scheduler

import (
	"context"
	"fmt"
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
	chronicleLifeChapterID  = "life"
	chronicleMaxTraces      = 40  // cap the input traces per act
	chronicleTraceCharCap   = 220 // per-trace snippet length
	chronicleActMaxTokens   = 700 // act + synopsis (marker-delimited), bounded
	chronicleSynopsisMarker = "===SYNOPSIS==="
)

// chronicleSystemPrompt — narrative voice, strictly grounded (anti-fiction is the
// prime directive), continuity via the synopsis, and a marker-delimited synopsis
// update. Generic principles, no enumerated cases.
const chronicleSystemPrompt = `You are Hygur, quietly chronicling the user's life from their own records. Write the next entry of an ongoing chronicle.

You are given THE STORY SO FAR (a short synopsis) and NEW TRACES from the recent period (mail, notes, events — dated). Write ONE entry, in flowing narrative prose — a short-story / journal voice, never a bullet list. Recount what happened in this period as a story: the people, the decisions, what moved, what is still pending.

Rules:
- Use ONLY the synopsis and the traces. NEVER invent an event, name, date, figure or outcome. If the traces are thin, write a short entry; if there is nothing of substance, write nothing at all.
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

	var since time.Time
	if chap.Watermark != "" {
		if t, e := time.Parse(time.RFC3339, chap.Watermark); e == nil {
			since = t
		}
	}
	items, err := w.store.ListKnowledgeItemsSince(ctx, since,
		store.MailAndSourceTypes(store.SourceTypeNote, store.SourceTypeEvent), chronicleMaxTraces)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", nil // nothing new to chronicle
	}

	act, synopsis := w.generate(ctx, chap.Synopsis, buildChronicleTraces(items), today)
	// New watermark = this write time → traces fed now are never re-fed.
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

// generate runs the single bounded LLM call → (act, updatedSynopsis). An empty
// updatedSynopsis means "keep the previous one" (parse miss / model didn't comply).
func (w *ChronicleWriter) generate(ctx context.Context, synopsis, traces, dateLabel string) (string, string) {
	storySoFar := strings.TrimSpace(synopsis)
	if storySoFar == "" {
		storySoFar = "(this is the first entry — there is no prior story yet)"
	}
	user := fmt.Sprintf("STORY SO FAR:\n%s\n\nNEW TRACES (period ending %s):\n%s", storySoFar, dateLabel, traces)
	resp, err := w.llm.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: chronicleSystemPrompt},
			{Role: "user", Content: user},
		},
		Temperature:        0.5, // narrative needs a little life, still grounded
		MaxTokens:          chronicleActMaxTokens,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	})
	if err != nil || resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		if err != nil {
			w.logger.Warn().Err(err).Msg("chronicle generate failed")
		}
		return "", ""
	}
	raw := resp.Choices[0].Message.Content
	if strings.TrimSpace(raw) == "" {
		raw = resp.Choices[0].Message.Reasoning
	}
	parts := strings.SplitN(raw, chronicleSynopsisMarker, 2)
	act := strings.TrimSpace(parts[0])
	newSynopsis := ""
	if len(parts) == 2 {
		newSynopsis = strings.TrimSpace(parts[1])
	}
	return act, newSynopsis
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
