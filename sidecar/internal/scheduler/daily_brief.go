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
	store  *store.DB
	llm    *llm.Client
	broker *events.Broker
	cfg    config.DailyBriefConfig
	logger zerolog.Logger
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
		// 7 days catches yesterday-evening mail without making the morning
		// brief feel stale on Mondays.
		cfg.LookbackHours = 168
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

	if len(items) == 0 {
		// Empty window — persist a placeholder and emit so the UI sees
		// "Pas d'activité" instead of nothing at all.
		placeholder := emptyPlaceholder(opts, projectName)
		ci, _ := d.persistBrief(ctx, briefKey, briefTitle(dateLabel, projectName), placeholder, opts)
		d.broker.Publish(events.NewBriefEvent(events.BriefPayload{
			Date:      dateLabel,
			ContentID: ci,
			Bullets:   nil,
			ItemCount: 0,
		}))
		return nil
	}

	enriched := d.enrichItems(ctx, items)
	prompt := buildBriefPrompt(enriched, opts, projectName)
	resp, err := d.llm.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			// Reasoning-capable backends (LM Studio + Nemotron/Qwen) emit
			// their scratch thinking before any visible content. Without
			// guidance they burn the entire token budget in `reasoning`
			// and return `content: null` — the empty-brief regression. The
			// system prompt asks the model to keep reasoning short.
			{Role: "system", Content: "You produce concise summaries. Keep any internal reasoning short. Always return the final answer as Markdown bullets in the user's language."},
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
		ci, _ := d.persistBrief(ctx, briefKey, briefTitle(dateLabel, projectName), fallback, opts)
		d.broker.Publish(events.NewBriefEvent(events.BriefPayload{
			Date:      dateLabel,
			ContentID: ci,
			Bullets:   firstBullets(fallback, 3),
			ItemCount: len(items),
			Error:     true,
		}))
		return nil
	}

	contentID, perr := d.persistBrief(ctx, briefKey, briefTitle(dateLabel, projectName), briefText, opts)
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
		[]string{"email", "note", "file", "pdf", "markdown", "md", "txt"},
		rawLimit,
	)
	if err != nil {
		return nil, "", fmt.Errorf("list items since: %w", err)
	}
	items = d.dropStaleByCanonicalDate(items)
	if len(items) > d.cfg.MaxItems {
		items = items[:d.cfg.MaxItems]
	}
	return items, "", nil
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

func emptyPlaceholder(opts RunOptions, projectName string) string {
	if projectName != "" {
		return "Pas d'activité enregistrée sur le projet « " + projectName + " »."
	}
	hours := opts.LookbackHours
	if hours == 0 {
		hours = 24
	}
	return "Pas d'activité dans les dernières " + strconv.Itoa(hours) + "h."
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
	existing, _ := d.store.GetKnowledgeItem(ctx, contentID)
	if existing != nil {
		_ = d.store.DeleteKnowledgeItem(ctx, contentID)
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
func buildBriefPrompt(items []briefItem, opts RunOptions, projectName string) string {
	var sb strings.Builder
	if projectName != "" {
		sb.WriteString("Voici les éléments liés au projet « ")
		sb.WriteString(projectName)
		sb.WriteString(" » dans la base personnelle de l'utilisateur. ")
		sb.WriteString("Génère un brief opérationnel et exécutif en français, structuré exactement ainsi :\n\n")
		sb.WriteString("## Synthèse exécutive\n")
		sb.WriteString("3-5 puces : où en est le projet, décisions prises / en attente, risques bloquants, échéances proches.\n\n")
		sb.WriteString("## Plan d'action\n")
		sb.WriteString("- **À faire ce matin :** tâches concrètes, courtes, livrables clairs.\n")
		sb.WriteString("- **À faire cet après-midi :** suite logique, suivis, validations.\n")
		sb.WriteString("- **Cette semaine :** échéances et travaux de fond.\n\n")
		sb.WriteString("## Emails à traiter\n")
		sb.WriteString("Liste les emails du contexte qui attendent une réponse, sous la forme « De [expéditeur] — [sujet] → [action recommandée] ».\n\n")
		sb.WriteString("## À ajouter dans la task list\n")
		sb.WriteString("Tâches à créer (verbe d'action en début), avec échéance et montant si présents.\n\n")
		sb.WriteString("## Sources\n")
		sb.WriteString("Tags et projets utilisés pour ce brief, séparés par des virgules.\n")
	} else {
		hours := opts.LookbackHours
		if hours == 0 {
			hours = 168
		}
		days := hours / 24
		if days <= 1 {
			sb.WriteString("Voici l'activité des dernières 24 h ")
		} else {
			sb.WriteString("Voici l'activité des ")
			sb.WriteString(strconv.Itoa(days))
			sb.WriteString(" derniers jours ")
		}
		sb.WriteString("dans la base personnelle de l'utilisateur. ")
		sb.WriteString("Génère un brief opérationnel et exécutif en français, structuré exactement ainsi :\n\n")
		sb.WriteString("## Synthèse exécutive\n")
		sb.WriteString("3-5 puces : priorités du jour, risques, décisions à prendre.\n\n")
		sb.WriteString("## Plan d'action\n")
		sb.WriteString("- **À faire ce matin :** tâches concrètes (paiements urgents, réponses bloquantes).\n")
		sb.WriteString("- **À faire cet après-midi :** suivis, lectures, validations.\n")
		sb.WriteString("- **Cette semaine :** échéances et travaux de fond.\n\n")
		sb.WriteString("## Emails à traiter\n")
		sb.WriteString("Pour chaque email du contexte qui attend une réponse : « De [expéditeur] — [sujet] → [action recommandée] ». Ne mentionne pas les emails purement informatifs.\n\n")
		sb.WriteString("## À ajouter dans la task list\n")
		sb.WriteString("Tâches à créer (verbe d'action en tête), avec échéance et montant quand ils sont présents.\n\n")
		sb.WriteString("## Sources\n")
		sb.WriteString("Tags et projets impliqués dans ce brief, séparés par des virgules.\n")
	}
	sb.WriteString("\nRègles strictes :\n")
	sb.WriteString("- N'invente rien. Chaque puce doit pouvoir s'appuyer sur un élément du contexte ci-dessous.\n")
	sb.WriteString("- Ignore les éléments en dehors de ta fenêtre de pertinence (vieille correspondance, accusés de réception, newsletters non-actionnables).\n")
	sb.WriteString("- Si une section est vide, écris « *Aucun élément* » plutôt que d'inventer du contenu.\n")
	sb.WriteString("- Output : Markdown brut, sans préambule, sans bloc de code.\n\n")
	sb.WriteString("Contexte (")
	sb.WriteString(strconv.Itoa(len(items)))
	sb.WriteString(" items) :\n")

	tagSet := map[string]struct{}{}
	projSet := map[string]struct{}{}
	for i, it := range items {
		sb.WriteString(fmt.Sprintf("- [%s] %s", it.SourceType, it.Title))
		// canonical date helps the model assess freshness and detect stale items.
		if cd := store.GetCanonicalDate(it.KnowledgeItem); !cd.IsZero() {
			sb.WriteString(" (")
			sb.WriteString(cd.Format("2006-01-02"))
			sb.WriteByte(')')
		}
		if from, ok := it.Metadata["mail_from"].(string); ok && from != "" {
			sb.WriteString(" — from ")
			sb.WriteString(from)
		}
		if amount := firstStringFromMetadata(it.Metadata, "extracted_amounts"); amount != "" {
			sb.WriteString(" · montant=")
			sb.WriteString(amount)
		}
		if due := firstStringFromMetadata(it.Metadata, "extracted_due_dates"); due != "" {
			sb.WriteString(" · échéance=")
			sb.WriteString(due)
		}
		if it.projectName != "" {
			sb.WriteString(" · projet=")
			sb.WriteString(it.projectName)
			projSet[it.projectName] = struct{}{}
		}
		if len(it.tags) > 0 {
			sb.WriteString(" · tags=")
			sb.WriteString(strings.Join(it.tags, ","))
			for _, t := range it.tags {
				tagSet[t] = struct{}{}
			}
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

	// Append a deterministic Sources hint the model can echo verbatim.
	if len(tagSet) > 0 || len(projSet) > 0 {
		sb.WriteString("\nIndices Sources (à reprendre/affiner dans la dernière section) :\n")
		if len(projSet) > 0 {
			projs := make([]string, 0, len(projSet))
			for p := range projSet {
				projs = append(projs, p)
			}
			sort.Strings(projs)
			sb.WriteString("- Projets : ")
			sb.WriteString(strings.Join(projs, ", "))
			sb.WriteByte('\n')
		}
		if len(tagSet) > 0 {
			tags := make([]string, 0, len(tagSet))
			for t := range tagSet {
				tags = append(tags, t)
			}
			sort.Strings(tags)
			sb.WriteString("- Tags : ")
			sb.WriteString(strings.Join(tags, ", "))
			sb.WriteByte('\n')
		}
	}
	return sb.String()
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
	sb.WriteString("**Brief indisponible — LLM hors ligne.**\n\nActivité brute :\n")
	for i, it := range items {
		if i >= 10 {
			sb.WriteString(fmt.Sprintf("- … et %d autres\n", len(items)-10))
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
