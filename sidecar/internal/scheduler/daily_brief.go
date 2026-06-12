package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/config"
	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// DailyBrief produces a single LLM-generated digest of recent knowledge-base
// activity. Designed to run once per day at a configurable local-time hour.
type DailyBrief struct {
	store    *store.DB
	llm      *llm.Client
	indexing *llm.Client // small model for decision-claim extraction (G4); falls back to llm
	broker   *events.Broker
	cfg      config.DailyBriefConfig
	logger   zerolog.Logger
}

// SetIndexingClient sets the small/cheap model used for decision-claim extraction
// (G4 contradiction guardrail). Off the chat budget; falls back to the main client
// when unset. Nil-safe.
func (d *DailyBrief) SetIndexingClient(c *llm.Client) {
	if d != nil {
		d.indexing = c
	}
}

// NewDailyBrief builds a brief task. Returns nil when any required dependency
// is missing — caller should treat that as "feature disabled".
func NewDailyBrief(store *store.DB, llmClient *llm.Client, broker *events.Broker, cfg config.DailyBriefConfig, logger zerolog.Logger) *DailyBrief {
	if store == nil || llmClient == nil || broker == nil {
		return nil
	}
	if cfg.MaxItems <= 0 {
		cfg.MaxItems = 80
	}
	if cfg.LookbackHours <= 0 {
		// Daily = delta: the brief reports what moved since yesterday, not a
		// rolling 7-day re-summary. Matches the config loader default (48h).
		cfg.LookbackHours = 48
	}
	if cfg.HourLocal == "" {
		cfg.HourLocal = "08:00"
	}
	if cfg.MaxItemAgeDays == 0 {
		// 6 months. Set explicitly to a negative value in config to disable.
		cfg.MaxItemAgeDays = 180
	}
	if cfg.MaxItemAgeDays < 0 {
		cfg.MaxItemAgeDays = 0 // disabled
	}
	return &DailyBrief{
		store:  store,
		llm:    llmClient,
		broker: broker,
		cfg:    cfg,
		logger: logger.With().Str("component", "daily_brief").Logger(),
	}
}

// Start launches a goroutine that fires Run() at the configured local hour
// each day. Exits when ctx is cancelled. No-op if the brief is disabled.
func (d *DailyBrief) Start(ctx context.Context) {
	if d == nil || !d.cfg.Enabled {
		return
	}
	go d.scheduleLoop(ctx)
}

func (d *DailyBrief) scheduleLoop(ctx context.Context) {
	for {
		next := nextOccurrence(time.Now(), d.cfg.HourLocal)
		wait := time.Until(next)
		d.logger.Info().Time("next_run", next).Dur("wait", wait).Msg("daily brief scheduled")

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		if err := d.Run(ctx); err != nil {
			d.logger.Warn().Err(err).Msg("daily brief run failed")
		}
	}
}

// nextOccurrence returns the next wall-clock time at hh:mm in the local
// timezone that is strictly after `now`. Falls back to "08:00" when the
// hourLocal string is malformed.
func nextOccurrence(now time.Time, hourLocal string) time.Time {
	parts := strings.SplitN(hourLocal, ":", 2)
	if len(parts) != 2 {
		parts = []string{"08", "00"}
	}
	h, errH := strconv.Atoi(parts[0])
	m, errM := strconv.Atoi(parts[1])
	if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		h, m = 8, 0
	}
	loc := time.Local
	candidate := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, loc)
	if !candidate.After(now) {
		candidate = candidate.Add(24 * time.Hour)
	}
	return candidate
}

// RunOptions overrides default config for a single brief execution. All
// fields are optional — zero values mean "use the configured default".
type RunOptions struct {
	// ProjectID, when set, scopes the brief to items linked to that project.
	// The brief queries store.GetItemsForProject and ignores LookbackHours
	// (project briefs cover the full project history by default).
	ProjectID string
	// LookbackHours overrides cfg.LookbackHours for this run only. Ignored
	// when ProjectID is set. 0 means use the configured default.
	LookbackHours int

	// --- On-demand custom briefing (WebUI "New briefing") ---
	// ProjectIDs / ContentIDs explicitly scope the brief to those projects'
	// items and/or those individual knowledge items. Instructions is a
	// free-text focus injected into the prompt. When any is set the brief is
	// "custom": it gets its own content_id (doesn't overwrite the daily brief).
	ProjectIDs   []string
	ContentIDs   []string
	Instructions string
}

// isCustom reports whether the options describe an on-demand contextual brief
// rather than the scheduled daily / single-project brief.
func (o RunOptions) isCustom() bool {
	return o.Instructions != "" || len(o.ContentIDs) > 0 || len(o.ProjectIDs) > 0
}

// Run executes a single brief immediately with the configured defaults.
// Equivalent to RunWith(ctx, RunOptions{}).
func (d *DailyBrief) Run(ctx context.Context) error {
	return d.RunWith(ctx, RunOptions{})
}

// RunWith executes a brief with explicit options. Public so the HTTP
// /brief/run endpoint and tests can trigger scoped briefs on demand.
func (d *DailyBrief) RunWith(ctx context.Context, opts RunOptions) error {
	if d == nil {
		return fmt.Errorf("daily brief not configured")
	}

	items, projectName, err := d.gatherItems(ctx, opts)
	if err != nil {
		return err
	}

	dateLabel := time.Now().Format("2006-01-02")
	briefKey := briefContentID(dateLabel, opts.ProjectID)
	title := briefTitle(dateLabel, projectName)
	if opts.isCustom() {
		briefKey = customBriefContentID(dateLabel, opts)
		title = customBriefTitle(dateLabel, opts)
	}

	if len(items) == 0 {
		// Empty window — persist a placeholder and emit so the UI sees
		// "Pas d'activité" instead of nothing at all.
		placeholder := emptyPlaceholder(opts, projectName)
		ci, _ := d.persistBrief(ctx, briefKey, title, placeholder, opts)
		d.broker.Publish(events.NewBriefEvent(events.BriefPayload{
			Date:      dateLabel,
			ContentID: ci,
			Bullets:   nil,
			ItemCount: 0,
		}))
		return nil
	}

	enriched := d.enrichItems(ctx, items)
	// Personal daily brief → ground it in the user's open task deadlines
	// (overdue + due soon). Project/scoped briefs keep their own focus.
	var dueTasks []*store.Task
	if opts.ProjectID == "" && d.store != nil {
		cutoff := time.Now().AddDate(0, 0, followupDueHorizonDays).UTC().Format(time.RFC3339)
		dueTasks, _ = d.store.TasksDueBefore(ctx, cutoff)
	}
	prompt := buildBriefPrompt(enriched, opts, projectName, dueTasks)
	resp, err := d.llm.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			// Reasoning-capable backends (LM Studio + Nemotron/Qwen) emit
			// their scratch thinking before any visible content. Without
			// guidance they burn the entire token budget in `reasoning`
			// and return `content: null` — the empty-brief regression. The
			// system prompt asks the model to keep reasoning short.
			{Role: "system", Content: "You are a personal assistant. You produce a short, prioritised brief that puts what truly matters first: actions to take, decisions to make, upcoming deadlines. You ignore noise. Keep internal reasoning very brief and reply in Markdown, in English."},
			{Role: "user", Content: prompt},
		},
		Temperature: 0.3,
		// The structured operational+executive prompt produces longer
		// output (5 sections, ~1.5-2 k tokens) on top of Nemotron's 3-5 k
		// of reasoning. 12288 leaves headroom; if we observe empty briefs
		// again the breadcrumb log will show finish_reason=length and we
		// bump further.
		MaxTokens: 12288,
	})

	briefText := ""
	finishReason := ""
	reasoningLen := 0
	if err == nil && resp != nil && len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		msg := resp.Choices[0].Message
		briefText = stripReasoningTags(msg.Content)
		reasoningLen = len(msg.Reasoning)
		finishReason = resp.Choices[0].FinishReason
	}

	// Either the LLM call errored or the visible (post-reasoning) content
	// came back empty — both produce an unusable brief. Fall back to a
	// deterministic placeholder so the user has something to read and the
	// UI can flag the failure. Structured fields here are the breadcrumbs
	// for diagnosing "empty brief" reports: long `reasoning_len` with
	// `finish_reason=length` means budget exhausted; `had_response=false`
	// means transport failure.
	if briefText == "" {
		d.logger.Warn().
			Err(err).
			Bool("had_response", resp != nil).
			Str("finish_reason", finishReason).
			Int("reasoning_len", reasoningLen).
			Msg("LLM call failed or returned empty content, emitting placeholder brief")
		fallback := fallbackBrief(items)
		ci, _ := d.persistBrief(ctx, briefKey, title, fallback, opts)
		d.broker.Publish(events.NewBriefEvent(events.BriefPayload{
			Date:      dateLabel,
			ContentID: ci,
			Bullets:   firstBullets(fallback, 3),
			ItemCount: len(items),
			Error:     true,
		}))
		return nil
	}

	// Sources rendered deterministically from the items actually used (W1.2):
	// never the model's prose, so the brief can't cite inputs it didn't see.
	// Replace any Sources section the model emitted despite the instruction.
	if src := deterministicSources(enriched); src != "" {
		briefText = strings.TrimRight(stripSourcesSection(briefText), "\n") + "\n\n" + src
	}

	contentID, perr := d.persistBrief(ctx, briefKey, title, briefText, opts)
	if perr != nil {
		return fmt.Errorf("persist brief: %w", perr)
	}

	d.broker.Publish(events.NewBriefEvent(events.BriefPayload{
		Date:      dateLabel,
		ContentID: contentID,
		Bullets:   firstBullets(briefText, 5),
		ItemCount: len(items),
	}))

	d.logger.Info().
		Str("content_id", contentID).
		Str("project_id", opts.ProjectID).
		Int("items", len(items)).
		Msg("brief published")
	return nil
}

// gatherItems returns the items the brief should summarise, plus the
// project name when scoped (for the prompt + persisted title).
//
// Two filters are layered:
//  1. ListKnowledgeItemsSince trims to items created/updated within
//     `LookbackHours` (catches the recent batch of indexed activity).
//  2. dropStaleByCanonicalDate then drops items whose intrinsic date
//     (mail_date for emails, doc_date for files) is older than
//     `MaxItemAgeDays`. Without (2) a backfill of 2024 emails would land
//     in today's brief because they were *indexed* yesterday.
func (d *DailyBrief) gatherItems(ctx context.Context, opts RunOptions) ([]*store.KnowledgeItem, string, error) {
	// Custom context: explicit projects + individual items the user picked.
	// We do NOT drop-by-stale-date here — the user chose these deliberately.
	if len(opts.ProjectIDs) > 0 || len(opts.ContentIDs) > 0 {
		seen := map[string]struct{}{}
		var items []*store.KnowledgeItem
		for _, pid := range opts.ProjectIDs {
			its, err := d.store.GetItemsForProject(ctx, pid)
			if err != nil {
				continue
			}
			for _, it := range its {
				if _, ok := seen[it.ContentID]; ok {
					continue
				}
				seen[it.ContentID] = struct{}{}
				items = append(items, it)
			}
		}
		for _, cid := range opts.ContentIDs {
			if _, ok := seen[cid]; ok {
				continue
			}
			it, err := d.store.GetKnowledgeItem(ctx, cid)
			if err != nil || it == nil {
				continue
			}
			seen[cid] = struct{}{}
			items = append(items, it)
		}
		if len(items) > d.cfg.MaxItems {
			items = items[:d.cfg.MaxItems]
		}
		return items, "", nil
	}

	if opts.ProjectID != "" {
		project, err := d.store.GetProject(ctx, opts.ProjectID)
		if err != nil || project == nil {
			return nil, "", fmt.Errorf("project not found: %s", opts.ProjectID)
		}
		items, err := d.store.GetItemsForProject(ctx, opts.ProjectID)
		if err != nil {
			return nil, project.Name, fmt.Errorf("get items for project: %w", err)
		}
		items = d.dropStaleByCanonicalDate(items)
		// Cap to MaxItems even for project briefs — keeps the LLM prompt bounded.
		if len(items) > d.cfg.MaxItems {
			items = items[:d.cfg.MaxItems]
		}
		return items, project.Name, nil
	}

	lookback := d.cfg.LookbackHours
	if opts.LookbackHours > 0 {
		lookback = opts.LookbackHours
	}
	since := time.Now().Add(-time.Duration(lookback) * time.Hour)
	// Pull more than MaxItems pre-filter — the canonical-date filter may
	// drop a chunk of them (especially right after a mailbox backfill),
	// and we still want MaxItems-worth of usable items.
	rawLimit := d.cfg.MaxItems * 2
	if rawLimit < 100 {
		rawLimit = 100
	}
	items, err := d.store.ListKnowledgeItemsSince(
		ctx, since,
		// Mail (both source-type variants) + notes/docs + calendar events (W3).
		// store.MailAndSourceTypes guarantees no mail variant is forgotten.
		store.MailAndSourceTypes("note", "file", "pdf", "markdown", "md", "txt", "event"),
		rawLimit,
	)
	if err != nil {
		return nil, "", fmt.Errorf("list items since: %w", err)
	}
	items = d.dropStaleByCanonicalDate(items)
	items = dropUndatedMail(items)
	if len(items) > d.cfg.MaxItems {
		items = items[:d.cfg.MaxItems]
	}
	return items, "", nil
}

// dropUndatedMail removes mail whose canonical (sent) date is absent: for such
// items ListKnowledgeItemsSince matched on created_at, which is only the
// ingestion timestamp — so a years-old mail that arrived without a parseable
// Date header would otherwise leak into the recency window. Non-mail items keep
// created_at as a legitimate date and are untouched. Applied to the lookback
// (recent) path only; project briefs intentionally include all linked items.
func dropUndatedMail(items []*store.KnowledgeItem) []*store.KnowledgeItem {
	out := items[:0]
	for _, it := range items {
		if store.IsMailSourceType(it.SourceType) && store.GetCanonicalDate(it).IsZero() {
			continue
		}
		out = append(out, it)
	}
	return out
}

// dropStaleByCanonicalDate removes items whose mail_date / canonical_date is
// older than MaxItemAgeDays. Items without a parseable canonical date keep
// their position (we only drop items we can confidently date as too old).
func (d *DailyBrief) dropStaleByCanonicalDate(items []*store.KnowledgeItem) []*store.KnowledgeItem {
	if d.cfg.MaxItemAgeDays <= 0 {
		return items
	}
	cutoff := time.Now().AddDate(0, 0, -d.cfg.MaxItemAgeDays)
	out := items[:0]
	for _, it := range items {
		canonical := store.GetCanonicalDate(it)
		if !canonical.IsZero() && canonical.Before(cutoff) {
			continue
		}
		out = append(out, it)
	}
	return out
}

// briefContentID composes a stable key for the persisted brief item.
// Date-keyed for daily briefs; project-keyed when scoped, with date suffix
// so multiple per-project briefs in the same day each get their own entry.
func briefContentID(dateLabel, projectID string) string {
	if projectID != "" {
		return "brief:project:" + projectID + ":" + dateLabel
	}
	return "brief:" + dateLabel
}

func briefTitle(dateLabel, projectName string) string {
	if projectName != "" {
		return "Brief — " + projectName + " — " + dateLabel
	}
	return "Daily brief — " + dateLabel
}

// customBriefContentID derives a stable id for an on-demand contextual brief
// from its inputs, so re-running the same request overwrites rather than
// duplicates, while distinct requests each get their own entry.
func customBriefContentID(dateLabel string, opts RunOptions) string {
	h := sha256.New()
	h.Write([]byte(opts.Instructions))
	ids := append(append([]string{}, opts.ProjectIDs...), opts.ContentIDs...)
	sort.Strings(ids)
	for _, id := range ids {
		h.Write([]byte("|" + id))
	}
	return "brief:custom:" + hex.EncodeToString(h.Sum(nil))[:12] + ":" + dateLabel
}

func customBriefTitle(dateLabel string, opts RunOptions) string {
	if s := strings.TrimSpace(opts.Instructions); s != "" {
		r := []rune(s)
		if len(r) > 50 {
			s = strings.TrimSpace(string(r[:50])) + "…"
		}
		return "Briefing: " + s
	}
	return "Custom briefing — " + dateLabel
}

func emptyPlaceholder(opts RunOptions, projectName string) string {
	if projectName != "" {
		return "No activity recorded on project \"" + projectName + "\"."
	}
	hours := opts.LookbackHours
	if hours == 0 {
		hours = 24
	}
	return "No activity in the last " + strconv.Itoa(hours) + "h."
}

// persistBrief stores the brief as a knowledge_item with source_type="brief".
// The content_id is supplied by the caller so daily briefs and project-scoped
// briefs can each have stable, distinct identities.
// Replaces any existing brief at the same key — manual re-runs are explicit
// re-generations and should overwrite, not duplicate.
func (d *DailyBrief) persistBrief(ctx context.Context, contentID, title, content string, opts RunOptions) (string, error) {
	hash := sha256.Sum256([]byte(content))
	versionID := hex.EncodeToString(hash[:])[:16]

	metadata := map[string]any{
		"date": time.Now().Format("2006-01-02"),
	}
	if opts.ProjectID != "" {
		metadata["project_id"] = opts.ProjectID
	}

	now := time.Now()
	item := &store.KnowledgeItem{
		ContentID:      contentID,
		SourceType:     "brief",
		Title:          title,
		NormalizedText: content,
		Metadata:       metadata,
		VersionID:      versionID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	existing, err := d.store.GetKnowledgeItem(ctx, contentID)
	if err != nil {
		return contentID, fmt.Errorf("brief: check existing: %w", err)
	}
	if existing != nil {
		if err := d.store.DeleteKnowledgeItem(ctx, contentID); err != nil {
			return contentID, fmt.Errorf("brief: delete existing: %w", err)
		}
	}
	if err := d.store.InsertKnowledgeItem(ctx, item); err != nil {
		return contentID, err
	}
	return contentID, nil
}

// briefItem is the augmented view of a knowledge item that the prompt
// builder consumes — KnowledgeItem plus the tags and project name we
// fetch out-of-band. Built by enrichItems.
type briefItem struct {
	*store.KnowledgeItem
	tags        []string
	projectName string
}

// enrichItems decorates each knowledge item with its tags and linked
// project name. Performed as N+2 queries because the existing store API
// is per-item; with MaxItems=80 this is comfortably under the LLM call
// cost. Errors are tolerated — missing tags/project just means an empty
// tags list rather than failing the whole brief.
func (d *DailyBrief) enrichItems(ctx context.Context, items []*store.KnowledgeItem) []briefItem {
	out := make([]briefItem, 0, len(items))
	// Cache project names so we don't re-query the same id N times.
	projCache := map[string]string{}
	for _, it := range items {
		bi := briefItem{KnowledgeItem: it}
		if tags, err := d.store.GetTagsForItem(ctx, it.ContentID); err == nil {
			for _, t := range tags {
				bi.tags = append(bi.tags, t.Name)
			}
		}
		if pid, err := d.store.GetProjectIDForItem(ctx, it.ContentID); err == nil && pid != nil && *pid != "" {
			if name, ok := projCache[*pid]; ok {
				bi.projectName = name
			} else if proj, perr := d.store.GetProject(ctx, *pid); perr == nil && proj != nil {
				bi.projectName = proj.Name
				projCache[*pid] = proj.Name
			}
		}
		out = append(out, bi)
	}
	return out
}

// buildBriefPrompt builds the single-LLM-call prompt. The output we ask
// for is split into two halves:
//
//   - Executive: priorities, risks, what to push back on
//   - Operational: concrete tasks for today, ordered by morning/afternoon,
//     plus emails awaiting reply and items to add to a task list
//
// We close with a "Sources" section listing the tags and project names
// the brief drew from, so the user can audit the inputs without leaving
// the brief.
func buildBriefPrompt(items []briefItem, opts RunOptions, projectName string, dueTasks []*store.Task) string {
	var sb strings.Builder
	if opts.Instructions != "" {
		sb.WriteString("User-requested focus: ")
		sb.WriteString(opts.Instructions)
		sb.WriteString("\nPrioritise this focus in the brief below.\n\n")
	}
	if projectName != "" {
		sb.WriteString("Here are the items related to the project \"")
		sb.WriteString(projectName)
		sb.WriteString("\" in the user's personal knowledge base. ")
		sb.WriteString("Generate an operational and executive brief in English, structured exactly as follows:\n\n")
		sb.WriteString("## Executive summary\n")
		sb.WriteString("3-5 bullets: where the project stands, decisions made / pending, blocking risks, upcoming deadlines.\n\n")
		sb.WriteString("## Action plan\n")
		sb.WriteString("- **This morning:** concrete, short tasks with clear deliverables.\n")
		sb.WriteString("- **This afternoon:** logical follow-ups, validations.\n")
		sb.WriteString("- **This week:** deadlines and deeper work.\n\n")
		sb.WriteString("## Emails to handle\n")
		sb.WriteString("List the emails in the context awaiting a reply, as \"From [sender] — [subject] → [recommended action]\".\n\n")
		sb.WriteString("## To add to the task list\n")
		sb.WriteString("Tasks to create (action verb first), with deadline and amount if present.\n\n")
	} else {
		hours := opts.LookbackHours
		if hours == 0 {
			hours = 48
		}
		if hours <= 72 {
			sb.WriteString("Here is what moved in the last ")
			sb.WriteString(strconv.Itoa(hours))
			sb.WriteString(" h ")
		} else {
			sb.WriteString("Here is what moved in the last ")
			sb.WriteString(strconv.Itoa(hours / 24))
			sb.WriteString(" days ")
		}
		sb.WriteString("in the user's personal knowledge base (emails, notes, documents). ")
		sb.WriteString("This is a daily check-in: focus on the DELTA — what is new or needs follow-up — not an exhaustive summary. Structure:\n\n")
		sb.WriteString("## Key points\n")
		sb.WriteString("The 3 to 6 items that truly matter over the period: what needs an action, a decision, or is approaching a deadline. One bullet per item, with the stake and the recommended action when relevant. If there is genuinely nothing notable, write a single bullet: \"Nothing critical over the period.\"\n\n")
		sb.WriteString("## Open loops\n")
		sb.WriteString("Exchanges still pending, visible in the context: an email left unanswered, a request the user hasn't replied to yet, a commitment made but not confirmed. Format \"[date] — [contact / subject] → awaiting [what]\". Only what genuinely shows up in the items; if nothing is pending, omit the section entirely.\n\n")
		sb.WriteString("## To handle now\n")
		sb.WriteString("- Payments / invoices: amount, deadline, to whom.\n")
		sb.WriteString("- Upcoming administrative, tax or legal deadlines.\n\n")
		sb.WriteString("## Tasks to create\n")
		sb.WriteString("Actions to add to the todo (action verb first), with deadline and amount when present.\n\n")
	}
	sb.WriteString("\nRules:\n")
	sb.WriteString("- Prioritise what's urgent or actionable; ignore noise — newsletters, auto-notifications, and spam/marketing/phishing — and never turn it into a task.\n")
	sb.WriteString("- Use only what the context says; never invent or distort. Omit any section with no content.\n")
	sb.WriteString("- Don't add a \"Sources\" section (it's appended automatically). Output raw Markdown, no preamble.\n\n")

	// The user's own open tasks with deadlines — ground the deadline/action
	// sections in what's already tracked, and don't re-suggest creating them.
	if len(dueTasks) > 0 {
		sb.WriteString("The user's open tasks with deadlines (already tracked — fold these into the deadlines/actions, do not recreate them):\n")
		for _, t := range dueTasks {
			due := t.DueDate
			if len(due) >= 10 {
				due = due[:10]
			}
			sb.WriteString(fmt.Sprintf("- due %s — %s\n", due, t.Title))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Context (")
	sb.WriteString(strconv.Itoa(len(items)))
	sb.WriteString(" items):\n")

	for i, it := range items {
		sb.WriteString(fmt.Sprintf("- [%s] %s", it.SourceType, it.Title))
		// canonical date helps the model assess freshness and detect stale items.
		// Weekday included so the model never has to derive it (small models err).
		if cd := store.GetCanonicalDate(it.KnowledgeItem); !cd.IsZero() {
			sb.WriteString(" (")
			sb.WriteString(cd.Format("2006-01-02 Mon"))
			sb.WriteByte(')')
		}
		// Sender: direct-IMAP uses "mail_from"; edge/Proton uses "from".
		from, _ := it.Metadata["mail_from"].(string)
		if from == "" {
			from, _ = it.Metadata["from"].(string)
		}
		if from != "" {
			sb.WriteString(" — from ")
			sb.WriteString(from)
		}
		if amount := firstStringFromMetadata(it.Metadata, "extracted_amounts"); amount != "" {
			sb.WriteString(" · amount=")
			sb.WriteString(amount)
		}
		if due := firstStringFromMetadata(it.Metadata, "extracted_due_dates"); due != "" {
			sb.WriteString(" · due=")
			sb.WriteString(due)
		}
		if it.projectName != "" {
			sb.WriteString(" · project=")
			sb.WriteString(it.projectName)
		}
		if len(it.tags) > 0 {
			sb.WriteString(" · tags=")
			sb.WriteString(strings.Join(it.tags, ","))
		}
		// Capped excerpt for context. 600 chars gives the model enough to
		// surface the actionable bits without ballooning the prompt.
		excerpt := it.NormalizedText
		if len(excerpt) > 600 {
			excerpt = excerpt[:600] + "…"
		}
		sb.WriteString("\n  > ")
		sb.WriteString(strings.ReplaceAll(excerpt, "\n", " "))
		sb.WriteByte('\n')
		// Hard prompt cap — defensive, MaxItems already limits this.
		if i > 80 {
			break
		}
	}
	return sb.String()
}

// deterministicSources renders the "## Sources" section in code from the items
// actually used — the projects + tags present — so it can't be hallucinated or
// reworded by the model. Returns "" when there's nothing to cite.
func deterministicSources(items []briefItem) string {
	projSet := map[string]struct{}{}
	tagSet := map[string]struct{}{}
	for _, it := range items {
		if it.projectName != "" {
			projSet[it.projectName] = struct{}{}
		}
		for _, t := range it.tags {
			tagSet[t] = struct{}{}
		}
	}
	if len(projSet) == 0 && len(tagSet) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Sources\n")
	if len(projSet) > 0 {
		sb.WriteString("- Projects: ")
		sb.WriteString(strings.Join(sortedSet(projSet), ", "))
		sb.WriteByte('\n')
	}
	if len(tagSet) > 0 {
		sb.WriteString("- Tags: ")
		sb.WriteString(strings.Join(sortedSet(tagSet), ", "))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func sortedSet(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// stripSourcesSection removes a "## Sources" heading and its body (up to the
// next "## " heading or EOF) so the deterministic section can replace any the
// model emitted despite being told not to.
func stripSourcesSection(md string) string {
	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines))
	skipping := false
	for _, ln := range lines {
		if t := strings.TrimSpace(ln); strings.HasPrefix(t, "## ") {
			skipping = strings.EqualFold(t, "## Sources")
		}
		if !skipping {
			out = append(out, ln)
		}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// firstStringFromMetadata pulls the first element from a metadata field
// that may have been deserialised as []string or []any (depending on the
// JSON round-trip). Returns "" when nothing usable.
func firstStringFromMetadata(meta map[string]any, key string) string {
	switch v := meta[key].(type) {
	case []string:
		if len(v) > 0 {
			return v[0]
		}
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s
			}
		}
	}
	return ""
}

// fallbackBrief produces a deterministic placeholder when the LLM is down.
func fallbackBrief(items []*store.KnowledgeItem) string {
	var sb strings.Builder
	sb.WriteString("**Brief unavailable — LLM offline.**\n\nRaw activity:\n")
	for i, it := range items {
		if i >= 10 {
			sb.WriteString(fmt.Sprintf("- … and %d more\n", len(items)-10))
			break
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", it.SourceType, it.Title))
	}
	return sb.String()
}

// stripReasoningTags removes `<think>…</think>` blocks emitted by reasoning
// models like Nemotron-super before persisting the brief. Without this the
// stored content can be (a) empty when the model spent the entire token
// budget thinking, or (b) full of internal scratch reasoning the user
// shouldn't see. Returns the trimmed visible content; empty string if
// nothing remains, so the caller can fall through to the failure path.
func stripReasoningTags(text string) string {
	for {
		start := strings.Index(text, "<think>")
		if start < 0 {
			break
		}
		end := strings.Index(text[start:], "</think>")
		if end < 0 {
			// Unclosed reasoning block — drop everything from <think> on.
			text = text[:start]
			break
		}
		text = text[:start] + text[start+end+len("</think>"):]
	}
	return strings.TrimSpace(text)
}

// firstBullets returns up to n leading bullet lines from a Markdown brief —
// shipped to the macOS app as a preview in the SSE event payload.
func firstBullets(text string, n int) []string {
	out := make([]string, 0, n)
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "*") {
			continue
		}
		out = append(out, strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "-"), "*")))
		if len(out) >= n {
			break
		}
	}
	return out
}
