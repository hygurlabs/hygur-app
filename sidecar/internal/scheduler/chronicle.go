package scheduler

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/prose"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// Chronicle — Hygur writes a grounded, narrative chronicle of the user's life,
// one dated act per night per chapter, in continuity (rolling synopsis + watermark)
// so it never re-narrates. v2: an always-open "life" chapter + one chapter per
// project, with clickable source anchors.

const (
	chronicleLifeChapterID = "life"
	chronicleMaxTraces     = 40  // cap the input traces per act
	chronicleTraceCharCap  = 220 // per-trace snippet length
	chronicleActMaxTokens  = 700 // act + synopsis (marker-delimited), bounded
	// The entry covers a window by REAL (canonical) date, not ingestion: a 2024
	// mail synced today, or a future calendar event, must NOT land in "today".
	chronicleFirstRunDays       = 7  // first entry covers ~the last week of real activity
	chronicleLookbackDays       = 60 // ingestion lookback to gather Life candidates
	chronicleCandidates         = 400
	chronicleMaxChaptersPerNight = 8 // cap project chapters written per pass (token budget)
	chronicleSynopsisMarker      = "===SYNOPSIS==="
)

// chronicleSystemPrompt — narrative voice, strictly grounded (anti-fiction is the
// prime directive), continuity via the synopsis, numbered source anchors, and a
// marker-delimited synopsis update. Generic principles, no enumerated cases.
const chronicleSystemPromptBase = `You are Hygur, quietly chronicling the user's life from their own records, chapter by chapter. Write the next entry of the chapter named at the top.

You are given the CHAPTER, THE STORY SO FAR (a short synopsis) and NEW TRACES from the recent period (numbered; mail, notes, events — dated). Write ONE entry, in flowing narrative prose — a short-story / journal voice, never a bullet list. Recount what happened in this period as a story: the people, the decisions, what moved, what is still pending.

Rules:
- Use ONLY the synopsis and the traces. NEVER invent an event, name, date, figure or outcome. If the traces are thin, write a short entry; if there is nothing of substance, write nothing at all.
- Chronicle ONLY what happened in this period (the dates shown in the traces). Do NOT narrate anything dated outside it, and NEVER speculate about the future.
- Continue the narrative: REFERENCE the past (from the synopsis) but do NOT re-tell it. Cover only what is new.
- Cite a source inline as [1], [2] … matching the trace numbers, sparingly — only where a specific fact comes from a specific message.
- A sober, readable register in your own plain voice. Do NOT imitate any author's style.
- No heading, no preamble — only the prose of the entry.

After the entry, output a line containing exactly ` + chronicleSynopsisMarker + ` and then an updated "story so far" (<= 120 words): the durable threads and current state, folding in this entry. Keep it tight.`

// chronicleSystemPrompt = base + the shared prose-voice block.
var chronicleSystemPrompt = llm.WithVoice(chronicleSystemPromptBase)

// chronicleClosingPromptBase writes the farewell entry that closes a chapter, from the
// story-so-far + the user's optional closing note. Grounded, no synopsis update.
const chronicleClosingPromptBase = `You are Hygur, closing a chapter of the user's chronicle. Write a brief CLOSING entry — a few sentences of flowing prose wrapping up where things landed and how this chapter ends.

Use ONLY the story-so-far and the user's closing note below; never invent. A sober, readable register in your own plain voice — no heading, no bullet list. It is a farewell to the chapter, so it may be lightly reflective, but stay grounded in what's given.`

// chronicleClosingPrompt = base + the shared prose-voice block.
var chronicleClosingPrompt = llm.WithVoice(chronicleClosingPromptBase)

// ChronicleWriter appends nightly acts to chapters.
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

// RunAll writes tonight's act to the "life" chapter and to each active project's
// chapter that has new in-window material (capped). force regenerates today's act
// from the recent week (manual trigger). Returns the number of acts written.
func (w *ChronicleWriter) RunAll(ctx context.Context, now time.Time, force bool) (int, error) {
	if w == nil {
		return 0, nil
	}
	written := 0

	life, _ := w.store.GetChronicleChapter(ctx, chronicleLifeChapterID)
	if life == nil {
		life = &store.ChronicleChapter{ID: chronicleLifeChapterID, Title: "Life", Status: "open"}
	}
	if ok, err := w.writeChapter(ctx, life, now, force); err != nil {
		if force {
			return written, err
		}
		w.logger.Debug().Err(err).Msg("chronicle: life chapter")
	} else if ok {
		written++
	}

	projects, err := w.store.ListProjects(ctx)
	if err != nil {
		return written, err
	}
	projWrites := 0
	for _, p := range projects {
		if p == nil || p.Archived {
			continue
		}
		if projWrites >= chronicleMaxChaptersPerNight {
			break
		}
		chapID := "proj:" + p.ProjectID
		chap, _ := w.store.GetChronicleChapter(ctx, chapID)
		if chap != nil && chap.Status == "closed" {
			continue // the user closed this chapter — it stays closed
		}
		if chap == nil {
			chap = &store.ChronicleChapter{ID: chapID, ProjectID: p.ProjectID, Title: p.Name, Status: "open"}
		} else {
			chap.Title = p.Name // keep the chapter title in sync with the project
		}
		ok, werr := w.writeChapter(ctx, chap, now, force)
		if werr != nil {
			if force {
				return written, werr
			}
			w.logger.Debug().Err(werr).Str("chapter", chapID).Msg("chronicle: project chapter")
			continue
		}
		if ok {
			written++
			projWrites++
		}
	}
	return written, nil
}

// CloseChapter writes a final, grounded closing act to a chapter from its synopsis
// plus the user's optional note, then marks it closed. The nightly pass skips closed
// chapters, so the closure sticks. The "life" chapter cannot be closed.
func (w *ChronicleWriter) CloseChapter(ctx context.Context, chapterID, note string, now time.Time) error {
	if w == nil {
		return fmt.Errorf("chronicle writer not configured")
	}
	if chapterID == chronicleLifeChapterID {
		return fmt.Errorf("the life chapter cannot be closed")
	}
	chap, err := w.store.GetChronicleChapter(ctx, chapterID)
	if err != nil {
		return err
	}
	if chap == nil {
		return fmt.Errorf("chapter not found")
	}
	act, gerr := w.generateClosing(ctx, chap.Synopsis, note)
	if gerr != nil {
		return gerr // surface; the chapter stays open so the user can retry
	}
	if strings.TrimSpace(act) == "" {
		act = "This chapter closes here."
	}
	today := now.UTC().Format("2006-01-02")
	actID := "chronicle:" + chap.ID + ":" + today
	_ = w.store.DeleteKnowledgeItem(ctx, actID) // the closing supersedes any auto-entry written today
	if err := w.store.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
		ContentID:      actID,
		SourceType:     store.SourceTypeChronicleAct,
		Title:          now.Format("2 January 2006"),
		NormalizedText: act,
		Metadata: map[string]any{
			"chapter_id":     chap.ID,
			"act_date":       today,
			"canonical_date": now.UTC().Format(time.RFC3339),
			"sources":        []string{},
			"closing":        true,
		},
		VersionID: today,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return err
	}
	chap.Status = "closed"
	chap.Watermark = now.UTC().Format(time.RFC3339)
	return w.store.UpsertChronicleChapter(ctx, chap)
}

// ReopenChapter flips a closed chapter back to open and stages the user's free-text
// reason as a pending note. No LLM here — the next pass (nightly, or a manual run)
// folds the note into a resumption entry, corroborated by traces in the window. The
// "life" chapter is always open and cannot be reopened.
func (w *ChronicleWriter) ReopenChapter(ctx context.Context, chapterID, note string, now time.Time) error {
	if w == nil {
		return fmt.Errorf("chronicle writer not configured")
	}
	if chapterID == chronicleLifeChapterID {
		return fmt.Errorf("the life chapter is always open")
	}
	chap, err := w.store.GetChronicleChapter(ctx, chapterID)
	if err != nil {
		return err
	}
	if chap == nil {
		return fmt.Errorf("chapter not found")
	}
	chap.Status = "open"
	chap.PendingNote = strings.TrimSpace(note)
	return w.store.UpsertChronicleChapter(ctx, chap)
}

// generateClosing runs the bounded closing LLM call (no synopsis update). Streamed
// on the no-timeout client like generate().
func (w *ChronicleWriter) generateClosing(ctx context.Context, synopsis, note string) (string, error) {
	storySoFar := strings.TrimSpace(synopsis)
	if storySoFar == "" {
		storySoFar = "(no prior story was recorded for this chapter)"
	}
	closingNote := strings.TrimSpace(note)
	if closingNote == "" {
		closingNote = "(none)"
	}
	user := fmt.Sprintf("STORY SO FAR:\n%s\n\nCLOSING NOTE (from the user):\n%s", storySoFar, closingNote)
	var sb strings.Builder
	err := w.llm.StreamChat(ctx, llm.ChatRequest{
		Category: "background",
		Pass:     "chronicle_close",
		Messages: []llm.Message{
			{Role: "system", Content: chronicleClosingPrompt},
			{Role: "user", Content: user},
		},
		Temperature:        llm.Temp(0.5),
		MaxTokens:          chronicleActMaxTokens,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	}, func(delta string, _ bool, _ *llm.Usage) error {
		sb.WriteString(delta)
		return nil
	})
	if err != nil {
		w.logger.Warn().Err(err).Msg("chronicle closing generate failed")
		return "", err
	}
	return prose.Tidy(strings.TrimSpace(sb.String()), ""), nil
}

// writeChapter appends one act to chap from its in-window items. Returns whether
// an act was written.
func (w *ChronicleWriter) writeChapter(ctx context.Context, chap *store.ChronicleChapter, now time.Time, force bool) (bool, error) {
	// (periodStart, now] by REAL date. A manual run re-narrates the recent week;
	// the nightly run continues from the watermark.
	periodStart := now.AddDate(0, 0, -chronicleFirstRunDays)
	if !force && chap.Watermark != "" {
		if t, e := time.Parse(time.RFC3339, chap.Watermark); e == nil && t.After(periodStart) {
			periodStart = t
		}
	}
	items, err := w.gatherFor(ctx, chap, periodStart, now)
	if err != nil {
		return false, err
	}
	pendingNote := strings.TrimSpace(chap.PendingNote)
	if len(items) == 0 && pendingNote == "" {
		return false, nil // nothing new and no reopen note → skip (0 tokens)
	}

	today := now.UTC().Format("2006-01-02")
	actID := "chronicle:" + chap.ID + ":" + today
	if !force && pendingNote == "" {
		if existing, _ := w.store.GetKnowledgeItem(ctx, actID); existing != nil {
			return false, nil // already chronicled today
		}
	}

	traces, sources := buildNumberedTraces(items)
	periodLabel := periodStart.Format("2 Jan") + " – " + now.Format("2 Jan 2006")
	act, synopsis, gerr := w.generate(ctx, chap.Title, chap.Synopsis, traces, periodLabel, pendingNote)
	if gerr != nil {
		return false, gerr // surface; don't advance the watermark → next run retries
	}
	chap.Watermark = now.UTC().Format(time.RFC3339)
	if strings.TrimSpace(act) == "" {
		_ = w.store.UpsertChronicleChapter(ctx, chap)
		return false, nil
	}

	if force || pendingNote != "" {
		_ = w.store.DeleteKnowledgeItem(ctx, actID) // overwrite today's act on force / reopen
	}
	if err := w.store.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
		ContentID:      actID,
		SourceType:     store.SourceTypeChronicleAct,
		Title:          now.Format("2 January 2006"),
		NormalizedText: act,
		Metadata: map[string]any{
			"chapter_id":     chap.ID,
			"act_date":       today,
			"canonical_date": now.UTC().Format(time.RFC3339),
			"sources":        sources,
		},
		VersionID: today,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return false, err
	}

	chap.Status = "open" // (re)open on write
	chap.PendingNote = "" // the reopen note, if any, is now narrated
	if s := strings.TrimSpace(synopsis); s != "" {
		chap.Synopsis = s
	}
	if err := w.store.UpsertChronicleChapter(ctx, chap); err != nil {
		return false, err
	}
	w.logger.Info().Str("chapter", chap.ID).Int("traces", len(items)).Msg("chronicle act written")
	return true, nil
}

// gatherFor returns a chapter's candidate items, filtered to the recent canonical
// window (non-future), sorted oldest-first, capped. Life = recent mail + notes;
// a project chapter = that project's own items.
func (w *ChronicleWriter) gatherFor(ctx context.Context, chap *store.ChronicleChapter, periodStart, now time.Time) ([]*store.KnowledgeItem, error) {
	var candidates []*store.KnowledgeItem
	var err error
	if chap.ProjectID != "" {
		candidates, err = w.store.GetItemsForProject(ctx, chap.ProjectID)
	} else {
		candidates, err = w.store.ListKnowledgeItemsSince(ctx, now.AddDate(0, 0, -chronicleLookbackDays),
			store.MailAndSourceTypes(store.SourceTypeNote, store.SourceTypeEvent), chronicleCandidates)
	}
	if err != nil {
		return nil, err
	}
	var items []*store.KnowledgeItem
	for _, it := range candidates {
		cd := store.GetCanonicalDate(it)
		if cd.IsZero() || !cd.After(periodStart) || cd.After(now) {
			continue // undated, older than the window, or in the future
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		return store.GetCanonicalDate(items[i]).Before(store.GetCanonicalDate(items[j]))
	})
	if len(items) > chronicleMaxTraces {
		items = items[len(items)-chronicleMaxTraces:]
	}
	return items, nil
}

// generate runs the bounded LLM call → (act, updatedSynopsis, err). Streamed on the
// no-timeout client (a background narrative must not be cut by the 30s timeout).
// reopenNote, when set, is the user's own words on why the chapter resumes — it seeds
// a resumption entry, corroborated by whatever traces fall in the window.
func (w *ChronicleWriter) generate(ctx context.Context, title, synopsis, traces, dateLabel, reopenNote string) (string, string, error) {
	storySoFar := strings.TrimSpace(synopsis)
	if storySoFar == "" {
		storySoFar = "(this is the first entry — there is no prior story yet)"
	}
	if strings.TrimSpace(traces) == "" {
		traces = "(none)"
	}
	user := fmt.Sprintf("CHAPTER: %s\n\nSTORY SO FAR:\n%s\n\nNEW TRACES (period %s):\n%s",
		title, storySoFar, dateLabel, traces)
	if n := strings.TrimSpace(reopenNote); n != "" {
		user += fmt.Sprintf("\n\nTHE USER IS REOPENING THIS CHAPTER. In their words: «%s»\n"+
			"Write a resumption entry now: pick the story back up from here, grounded in their words and in any traces above that corroborate them. If the traces are thin, narrate the resumption itself from what they said — do not invent beyond it.", n)
	}
	var sb strings.Builder
	err := w.llm.StreamChat(ctx, llm.ChatRequest{
		Category: "background",
		Pass:     "chronicle_act",
		Messages: []llm.Message{
			{Role: "system", Content: chronicleSystemPrompt},
			{Role: "user", Content: user},
		},
		Temperature:        llm.Temp(0.5),
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
	// Couche B: tidy the prose act only — never the machine synopsis after the marker.
	act := prose.Tidy(strings.TrimSpace(parts[0]), "")
	newSynopsis := ""
	if len(parts) == 2 {
		newSynopsis = strings.TrimSpace(parts[1])
	}
	return act, newSynopsis, nil
}

// buildNumberedTraces renders items as numbered, dated, capped lines and returns
// the parallel source content_ids (index n-1 ↔ "[n]" in the prose, for anchors).
func buildNumberedTraces(items []*store.KnowledgeItem) (string, []string) {
	var b strings.Builder
	sources := make([]string, 0, len(items))
	n := 0
	for _, it := range items {
		if it == nil {
			continue
		}
		n++
		date := ""
		if t := store.GetCanonicalDate(it); !t.IsZero() {
			date = t.Format("2006-01-02")
		}
		snippet := strings.ReplaceAll(strings.TrimSpace(it.NormalizedText), "\n", " ")
		if len(snippet) > chronicleTraceCharCap {
			snippet = snippet[:chronicleTraceCharCap] + "…"
		}
		fmt.Fprintf(&b, "%d. [%s] %s — %s\n", n, date, strings.TrimSpace(it.Title), snippet)
		sources = append(sources, it.ContentID)
	}
	return b.String(), sources
}

// ChronicleScheduler runs the nightly chronicle pass once a day at the night hour.
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
				if _, err := s.writer.RunAll(ctx, now, false); err != nil {
					s.logger.Debug().Err(err).Msg("nightly chronicle failed")
				}
			}
		}
	}()
}
