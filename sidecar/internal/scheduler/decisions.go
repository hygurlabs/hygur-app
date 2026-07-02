package scheduler

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// Decisions — Hygur detects the decisions and commitments the user makes in their
// own records and proposes them for confirmation, so the user's decisions become
// first-class, revisitable objects (the keystone the other lenses hang off).
//
// The scanner mirrors the W6 claims extractor: a deliberately generic, bounded
// prompt with a verbatim-quote anti-hallucination gate, so a fabricated decision
// can't enter the pipeline. Proposed decisions await the user's confirmation.

const (
	decisionScanWatermarkKey = "decision_scan_watermark"
	decisionScanFirstRunDays = 7    // first pass looks at the last week of ingestion
	decisionScanMaxItems     = 40   // items processed per pass (cost bound)
	decisionMaxRunes         = 4000 // how much of an item body to send
	decisionMaxTokens        = 500
	decisionMaxPerItem       = 5 // bound proposals kept per item
)

// decisionSystemPrompt is generic and bounded — no domain enumerations or profile
// assumptions — so it generalises across any document. The verbatim quote is the
// anti-hallucination gate enforced in code afterwards.
const decisionSystemPrompt = `These records belong to one person — the owner. You extract only the decisions and commitments the OWNER has themselves made: a choice they have settled, or a commitment they have personally agreed to (to do something, to proceed a certain way, to stop or change course, to agree to a date, an amount, or an arrangement). Ignore decisions, plans, policies, or arrangements made by other people or organisations, even when they concern the owner or are addressed to them.

Return ONLY a JSON array of the few clearest such decisions actually present, each stated ONCE — never restate the same commitment in different words. Each element:
{"statement": "...", "quote": "..."}
- statement: one short, self-contained sentence in the owner's own first person, as they would recognise their own decision.
- quote: the exact span of the document that states or commits to it, copied character-for-character.

Extract only decisions explicitly made; never infer intent; ignore mere discussion, questions, or options not chosen. Ignore presentational and boilerplate text. Omit any decision you cannot quote verbatim. If there are none, return []. Output the JSON array and nothing else. The document may be in any language.`

type decisionCandidate struct {
	Statement string `json:"statement"`
	Quote     string `json:"quote"`
}

// DecisionScanner proposes decisions from newly-ingested items.
type DecisionScanner struct {
	store  *store.DB
	llm    *llm.Client
	logger zerolog.Logger
}

// NewDecisionScanner builds the scanner; nil when a dependency is missing.
func NewDecisionScanner(db *store.DB, client *llm.Client, logger zerolog.Logger) *DecisionScanner {
	if db == nil || client == nil {
		return nil
	}
	return &DecisionScanner{store: db, llm: client, logger: logger.With().Str("component", "decisions").Logger()}
}

// Run scans items ingested since the last watermark, proposes the new decisions it
// finds (idempotent via the dedup key), and advances the watermark. force ignores
// the watermark and re-scans the last week (the manual "Scan now" trigger).
// Returns the number of decisions proposed.
func (s *DecisionScanner) Run(ctx context.Context, now time.Time, force bool) (int, error) {
	if s == nil || s.store == nil || s.llm == nil {
		return 0, nil
	}
	since := now.AddDate(0, 0, -decisionScanFirstRunDays)
	if !force {
		if wm, _ := s.store.GetAppSetting(ctx, decisionScanWatermarkKey); wm != "" {
			if t, err := time.Parse(time.RFC3339, wm); err == nil {
				since = t
			}
		}
	}
	items, err := s.store.ListKnowledgeItemsSince(ctx, since,
		store.MailAndSourceTypes(store.SourceTypeNote, store.SourceTypeFile), decisionScanMaxItems)
	if err != nil {
		return 0, err
	}

	proposed := 0
	for _, item := range items {
		if ctx.Err() != nil {
			break
		}
		for _, c := range s.extract(ctx, item.NormalizedText) {
			added, perr := s.propose(ctx, item, c, now)
			if perr != nil {
				s.logger.Warn().Err(perr).Str("source", item.ContentID).Msg("propose decision")
				continue
			}
			if added {
				proposed++
			}
		}
	}

	// Advance the watermark past this pass. Dedup makes any future re-scan of the
	// same items a no-op, so moving forward only avoids redundant LLM calls.
	if err := s.store.SetAppSetting(ctx, decisionScanWatermarkKey, now.UTC().Format(time.RFC3339)); err != nil {
		s.logger.Warn().Err(err).Msg("advance decision watermark")
	}
	if proposed > 0 {
		s.logger.Info().Int("proposed", proposed).Int("scanned", len(items)).Msg("decision scan complete")
	}
	return proposed, nil
}

// propose stores one detected decision as a "proposed" decision linked to its
// source. Returns false (no error) when an identical decision already exists.
func (s *DecisionScanner) propose(ctx context.Context, item *store.KnowledgeItem, c decisionCandidate, now time.Time) (bool, error) {
	statement := strings.TrimSpace(c.Statement)
	if statement == "" {
		return false, nil
	}
	dedup := store.DecisionDedupKey(item.ContentID, statement)
	exists, err := s.store.DecisionDedupExists(ctx, dedup)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	contentID := "decision:" + uuid.New().String()
	decidedOn := store.GetCanonicalDate(item).UTC().Format(time.RFC3339)
	ki := &store.KnowledgeItem{
		ContentID:      contentID,
		SourceType:     store.SourceTypeDecision,
		Title:          statement,
		NormalizedText: "", // rationale is the user's to add; the source carries the evidence
		Metadata:       map[string]any{"created_from": "scan", "canonical_date": decidedOn},
		VersionID:      uuid.New().String(),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.store.InsertKnowledgeItem(ctx, ki); err != nil {
		return false, err
	}
	if err := s.store.UpsertDecisionAttrs(ctx, contentID, "proposed", decidedOn, []string{item.ContentID}, dedup); err != nil {
		return false, err
	}
	// Inherit the source item's project so a proposed decision sits with its work.
	if pid, perr := s.store.GetProjectIDForItem(ctx, item.ContentID); perr == nil && pid != nil && *pid != "" {
		_ = s.store.LinkToProject(ctx, contentID, *pid)
	}
	return true, nil
}

// extract asks the LLM for the decisions asserted in text, then keeps only those
// whose quote is a real substring of text (the anti-hallucination gate). Constrained
// decoding (temp 0, no thinking) keeps it fast and stable on a small model.
func (s *DecisionScanner) extract(ctx context.Context, text string) []decisionCandidate {
	body := text
	if r := []rune(body); len(r) > decisionMaxRunes {
		body = string(r[:decisionMaxRunes])
	}
	if strings.TrimSpace(body) == "" {
		return nil
	}
	resp, err := s.llm.Chat(ctx, llm.ChatRequest{
		Category: "background",
		Pass:     "decisions",
		Messages: []llm.Message{
			{Role: "system", Content: decisionSystemPrompt},
			{Role: "user", Content: body},
		},
		Temperature:        llm.Temp(0),
		TopP:               llm.Temp(1),
		Seed:               llm.SeedOf(42),
		MaxTokens:          decisionMaxTokens,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	})
	if err != nil {
		s.logger.Debug().Err(err).Msg("decision extraction failed")
		return nil
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return nil
	}
	raw := resp.Choices[0].Message.Content
	if strings.TrimSpace(raw) == "" {
		raw = resp.Choices[0].Message.Reasoning
	}
	return parseDecisions(raw, text)
}

// parseDecisions extracts the JSON array from a (possibly fenced / chatty) reply
// and returns only well-formed candidates whose quote is verbatim-present in the
// source. Pure + deterministic.
func parseDecisions(raw, sourceText string) []decisionCandidate {
	arr := firstJSONArrayDecisions(raw)
	if arr == "" {
		return nil
	}
	var parsed []decisionCandidate
	if err := json.Unmarshal([]byte(arr), &parsed); err != nil {
		return nil
	}
	out := make([]decisionCandidate, 0, len(parsed))
	for _, c := range parsed {
		c.Statement = strings.TrimSpace(c.Statement)
		c.Quote = strings.TrimSpace(c.Quote)
		if c.Statement == "" || c.Quote == "" {
			continue
		}
		if !quoteInTextDecisions(sourceText, c.Quote) {
			continue // anti-hallucination gate: drop fabricated quotes
		}
		out = append(out, c)
		if len(out) >= decisionMaxPerItem {
			break
		}
	}
	return out
}

func firstJSONArrayDecisions(s string) string {
	start := strings.IndexByte(s, '[')
	end := strings.LastIndexByte(s, ']')
	if start < 0 || end < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

func quoteInTextDecisions(text, quote string) bool {
	q := normalizeWSDecisions(quote)
	if q == "" {
		return false
	}
	return strings.Contains(normalizeWSDecisions(text), q)
}

func normalizeWSDecisions(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// DecisionScheduler runs the nightly decision scan once a day at the night hour.
type DecisionScheduler struct {
	scanner *DecisionScanner
	hour    int
	logger  zerolog.Logger
}

// NewDecisionScheduler builds the loop; nil when the scanner is missing.
func NewDecisionScheduler(scanner *DecisionScanner, nightHour int, logger zerolog.Logger) *DecisionScheduler {
	if scanner == nil {
		return nil
	}
	if nightHour < 0 || nightHour > 23 {
		nightHour = 23
	}
	return &DecisionScheduler{scanner: scanner, hour: nightHour, logger: logger.With().Str("component", "decision_scheduler").Logger()}
}

// Start launches the loop. Hourly tick; scans once at the night hour (the dedup
// key makes a same-hour double-fire a no-op anyway).
func (s *DecisionScheduler) Start(ctx context.Context) {
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
				if _, err := s.scanner.Run(ctx, now, false); err != nil {
					s.logger.Debug().Err(err).Msg("nightly decision scan failed")
				}
			}
		}
	}()
}
