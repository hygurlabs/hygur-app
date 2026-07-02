package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/prose"
	"github.com/hygur/sidecar/internal/store"
)

// FollowUpDigest is the grounded synthesis shown in the Follow-up view: a few
// salient topics with short factual notes, plus contradictions surfaced ONLY
// when the model can cite >=2 genuinely-conflicting items. Every claim is tied
// to real source items (references validated server-side); nothing is invented.
type FollowUpDigest struct {
	Topics         []DigestEntry `json:"topics"`
	Contradictions []DigestEntry `json:"contradictions"`
	DueTasks       []DueTask     `json:"due_tasks"`
	Scanned        int           `json:"scanned"`
	Window         string        `json:"window"`
}

// DueTask is an open task with a deadline, surfaced proactively as an attention
// item (overdue or due soon). Deterministic — no LLM.
type DueTask struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	DueDate string `json:"due_date"`
	Status  string `json:"status"`
}

// followupDueHorizonDays bounds how far ahead a deadline is surfaced as
// "attention"; overdue tasks (due_date in the past) are always included.
const followupDueHorizonDays = 14

// upcomingDueTasks lists open tasks due within the horizon (overdue included),
// soonest first. Fresh each call so a just-edited due date shows immediately.
func (d *DailyBrief) upcomingDueTasks(ctx context.Context) []DueTask {
	if d.store == nil {
		return nil
	}
	cutoff := time.Now().AddDate(0, 0, followupDueHorizonDays).UTC().Format(time.RFC3339)
	tasks, err := d.store.TasksDueBefore(ctx, cutoff)
	if err != nil {
		return nil
	}
	out := make([]DueTask, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, DueTask{ID: t.ID, Title: t.Title, DueDate: t.DueDate, Status: t.Status})
	}
	return out
}

// DigestEntry is one topic or contradiction with its cited sources. Title is
// used for topics only.
type DigestEntry struct {
	Title   string         `json:"title,omitempty"`
	Note    string         `json:"note"`
	Sources []DigestSource `json:"sources"`
}

// DigestSource is a validated citation back to a real knowledge item.
type DigestSource struct {
	ContentID string `json:"content_id"`
	Title     string `json:"title"`
	From      string `json:"from"`
	Date      string `json:"date"` // RFC3339
}

const (
	followupTTL        = time.Hour
	followupWindowDays = 60
	followupMaxItems   = 60
	followupSnippetLen = 200
	followupMaxTopics  = 6
	followupMaxConflic = 6
)

// Single-tenant process → a package-level cache is enough.
var (
	followupMu      sync.Mutex
	followupKey     string
	followupValue   FollowUpDigest
	followupExpires time.Time
)

// followupSystemPrompt is strict by design ("facts before reply"): the model may
// only use the listed messages, and the last rule kills the monthly-invoice
// false-positive class that made the old deterministic surface useless.
const followupSystemPrompt = `You are a personal assistant. From ONLY the numbered messages below, produce a short follow-up.

Reply with a valid JSON object only — no surrounding text, no code fences:
{"topics":[{"title":"...","note":"...","refs":[1,4]}],"contradictions":[{"note":"...","refs":[2,9]}]}

Rules:
- "topics": a few active topics (max 6); each a factual note plus the message numbers it draws from.
- "contradictions": only when >=2 messages genuinely conflict about the same thing; cite >=2 different numbers. None -> return [].
- Use only what the messages say; never invent or distort. Ignore spam, marketing and phishing.
- English. Minimal reasoning.`

// FollowUp returns a grounded, LLM-written digest of recent mail + notes:
// salient topics and any real contradictions, each cited to source items.
// Empty (no topics, no contradictions) when there's nothing factual to report.
// Cached ~1h.
func (d *DailyBrief) FollowUp(ctx context.Context, projectID string) (FollowUpDigest, error) {
	if d == nil || d.store == nil || d.llm == nil {
		return FollowUpDigest{}, nil
	}
	items, err := d.gatherFollowupItems(ctx, projectID)
	if err != nil {
		return FollowUpDigest{}, err
	}
	window := fmt.Sprintf("last %d days", followupWindowDays)
	if projectID != "" {
		window = "project"
	}
	// Deadlines are a global, deterministic attention surface — computed fresh
	// each call (independent of the LLM digest cache) so a just-set due date
	// shows at once. Project-scoped views keep their own focus.
	var due []DueTask
	if projectID == "" {
		due = d.upcomingDueTasks(ctx)
	}
	if len(items) == 0 {
		return FollowUpDigest{Window: window, DueTasks: due}, nil
	}

	key := followupCacheKey(items, projectID)
	followupMu.Lock()
	if followupKey == key && time.Now().Before(followupExpires) {
		v := followupValue
		followupMu.Unlock()
		v.DueTasks = due
		return v, nil
	}
	followupMu.Unlock()

	digest := d.generateFollowup(ctx, items)
	digest.Scanned = len(items)
	digest.Window = window

	followupMu.Lock()
	followupKey, followupValue, followupExpires = key, digest, time.Now().Add(followupTTL)
	followupMu.Unlock()
	digest.DueTasks = due
	return digest, nil
}

// gatherFollowupItems pulls the items to summarize, newest first, capped to keep
// the LLM prompt bounded. With a projectID it returns that project's own items
// (its full state, any age); otherwise recent mail + notes within the window.
func (d *DailyBrief) gatherFollowupItems(ctx context.Context, projectID string) ([]*store.KnowledgeItem, error) {
	if projectID != "" {
		items, err := d.store.GetItemsForProject(ctx, projectID)
		if err != nil {
			return nil, err
		}
		sort.Slice(items, func(i, j int) bool {
			return itemDate(items[i]).After(itemDate(items[j]))
		})
		return d.gateAndCap(ctx, items)
	}
	since := time.Now().Add(-followupWindowDays * 24 * time.Hour)
	var all []*store.KnowledgeItem
	for _, src := range store.MailAndSourceTypes(store.SourceTypeNote) {
		const batch = 500
		for offset := 0; ; offset += batch {
			page, err := d.store.ListKnowledgeItemsBySourceType(ctx, src, batch, offset)
			if err != nil {
				return nil, err
			}
			all = append(all, page...)
			if len(page) < batch {
				break
			}
		}
	}
	var recent []*store.KnowledgeItem
	for _, it := range all {
		if d := recencyDate(it); !d.IsZero() && d.After(since) {
			recent = append(recent, it)
		}
	}
	sort.Slice(recent, func(i, j int) bool {
		return recencyDate(recent[i]).After(recencyDate(recent[j]))
	})
	return d.gateAndCap(ctx, recent)
}

// gateAndCap applies the deterministic live/dead gate, then caps the list. The gate
// uses the same signals as the Engram compartment — a superseded decision or an item
// caught in an open contradiction — so a matter that is already closed or in conflict
// (e.g. a thread that ended in a refusal) never resurfaces as a "follow up / chase
// this" item. Filtering before the cap means the cap keeps live items, not dead ones.
func (d *DailyBrief) gateAndCap(ctx context.Context, items []*store.KnowledgeItem) ([]*store.KnowledgeItem, error) {
	items = d.dropClosedItems(ctx, items)
	if len(items) > followupMaxItems {
		items = items[:followupMaxItems]
	}
	return items, nil
}

// dropClosedItems removes items the deterministic layer marks closed or in conflict
// (superseded decision, or member of an open contradiction). Best-effort: a status
// lookup error leaves the items unfiltered rather than losing the whole read. The LLM
// only narrates what survives the gate — it never judges "is this closed" itself.
func (d *DailyBrief) dropClosedItems(ctx context.Context, items []*store.KnowledgeItem) []*store.KnowledgeItem {
	if len(items) == 0 {
		return items
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ContentID)
	}
	decStatus, err := d.store.DecisionStatuses(ctx, ids)
	if err != nil {
		return items
	}
	contra, err := d.store.OpenContradictionContentIDs(ctx)
	if err != nil {
		return items
	}
	// A3 (thread-level): group messages by normalized subject and close a whole thread
	// when its LATEST status claim (across all its messages) is terminal-negative — so a
	// rejected thread is excluded entirely, not just the message that carried the refusal.
	byThread := map[string][]contradict.Claim{}
	for _, it := range items {
		k := contradict.ThreadKey(it.Title)
		byThread[k] = append(byThread[k], contradict.ClaimsFromMetadata(it.Metadata)...)
	}
	closedThreads := make(map[string]bool, len(byThread))
	for k, claims := range byThread {
		if closed, _ := contradict.ClosedNegative(claims); closed {
			closedThreads[k] = true
		}
	}
	out := items[:0]
	for _, it := range items {
		if decStatus[it.ContentID] == store.DecisionSuperseded {
			continue
		}
		if _, closed := contra[it.ContentID]; closed {
			continue
		}
		if closedThreads[contradict.ThreadKey(it.Title)] {
			continue
		}
		out = append(out, it)
	}
	return out
}

// numberedItemsContext renders the items as a numbered list the model can cite
// by index. Shared by the structured digest and the prose report.
func numberedItemsContext(items []*store.KnowledgeItem) string {
	var sb strings.Builder
	sb.WriteString("Numbered messages:\n")
	for i, it := range items {
		fmt.Fprintf(&sb, "[%d] %s · %s · %s", i+1, itemDate(it).Format("2006-01-02"), senderOf(it), strings.TrimSpace(it.Title))
		if snip := snippet(it.NormalizedText, followupSnippetLen); snip != "" {
			fmt.Fprintf(&sb, " — %s", snip)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func (d *DailyBrief) generateFollowup(ctx context.Context, items []*store.KnowledgeItem) FollowUpDigest {
	resp, err := d.llm.Chat(ctx, llm.ChatRequest{
		Category: "background",
		Pass:     "followup_digest",
		Messages: []llm.Message{
			{Role: "system", Content: followupSystemPrompt},
			{Role: "user", Content: numberedItemsContext(items)},
		},
		Temperature:        llm.Temp(0.2),
		MaxTokens:          1200,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	})
	if err != nil || resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return FollowUpDigest{}
	}
	raw := stripReasoningTags(resp.Choices[0].Message.Content)
	if strings.TrimSpace(raw) == "" {
		raw = stripReasoningTags(resp.Choices[0].Message.Reasoning)
	}
	parsed, ok := parseFollowupJSON(raw)
	if !ok {
		return FollowUpDigest{}
	}
	return gateDigest(parsed, items)
}

// rawDigest mirrors the model's JSON, with item references as 1-based indices.
type rawDigest struct {
	Topics []struct {
		Title string `json:"title"`
		Note  string `json:"note"`
		Refs  []int  `json:"refs"`
	} `json:"topics"`
	Contradictions []struct {
		Note string `json:"note"`
		Refs []int  `json:"refs"`
	} `json:"contradictions"`
}

func parseFollowupJSON(raw string) (rawDigest, bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	// Dig out the outermost object if the model wrapped it in prose.
	if i := strings.Index(raw, "{"); i > 0 {
		raw = raw[i:]
	}
	if j := strings.LastIndex(raw, "}"); j >= 0 && j+1 <= len(raw) {
		raw = raw[:j+1]
	}
	var rd rawDigest
	if err := json.Unmarshal([]byte(raw), &rd); err != nil {
		return rawDigest{}, false
	}
	return rd, true
}

// gateDigest is the anti-hallucination guard: it resolves every reference to a
// real item, drops topics with no valid source, and drops contradictions that
// can't cite >=2 distinct real sources (B.6 "facts before reply"). The model's
// prose is kept, but only when it's grounded.
func gateDigest(rd rawDigest, items []*store.KnowledgeItem) FollowUpDigest {
	resolve := func(refs []int) []DigestSource {
		seen := map[string]bool{}
		var out []DigestSource
		for _, r := range refs {
			if r < 1 || r > len(items) {
				continue
			}
			it := items[r-1]
			if seen[it.ContentID] {
				continue
			}
			seen[it.ContentID] = true
			out = append(out, DigestSource{
				ContentID: it.ContentID,
				Title:     strings.TrimSpace(it.Title),
				From:      senderOf(it),
				Date:      itemDate(it).Format(time.RFC3339),
			})
		}
		return out
	}

	var d FollowUpDigest
	for _, t := range rd.Topics {
		note := strings.TrimSpace(t.Note)
		s := resolve(t.Refs)
		if note == "" || len(s) == 0 {
			continue // a topic must be grounded in >=1 real item
		}
		d.Topics = append(d.Topics, DigestEntry{Title: strings.TrimSpace(t.Title), Note: note, Sources: s})
		if len(d.Topics) >= followupMaxTopics {
			break
		}
	}
	for _, c := range rd.Contradictions {
		note := strings.TrimSpace(c.Note)
		s := resolve(c.Refs)
		if note == "" || len(s) < 2 {
			continue // a contradiction must cite >=2 distinct real sources
		}
		d.Contradictions = append(d.Contradictions, DigestEntry{Note: note, Sources: s})
		if len(d.Contradictions) >= followupMaxConflic {
			break
		}
	}
	return d
}

// itemDate returns the canonical date (mail/note date) falling back to created_at.
// Used for display/sort of items already admitted to the window.
func itemDate(it *store.KnowledgeItem) time.Time {
	if cd := store.GetCanonicalDate(it); !cd.IsZero() {
		return cd
	}
	return it.CreatedAt
}

// recencyDate is the date used to decide whether an item belongs in the recent
// window. For mail it is ONLY the real sent date (canonical): the ingestion
// timestamp must never stand in, or a years-old mail that arrived without a
// parseable Date header would look recent (it was stamped with the backfill
// time). For notes/docs the creation time IS the real date, so fall back to
// created_at. Returns zero when the item can't be placed in time (→ excluded).
func recencyDate(it *store.KnowledgeItem) time.Time {
	if cd := store.GetCanonicalDate(it); !cd.IsZero() {
		return cd
	}
	if store.IsMailSourceType(it.SourceType) {
		return time.Time{}
	}
	return it.CreatedAt
}

// senderOf returns the interlocutor: "mail_from" (direct IMAP) or "from" (edge).
func senderOf(it *store.KnowledgeItem) string {
	if s, _ := it.Metadata["mail_from"].(string); strings.TrimSpace(s) != "" {
		return s
	}
	if s, _ := it.Metadata["from"].(string); strings.TrimSpace(s) != "" {
		return s
	}
	return ""
}

// snippet collapses whitespace and truncates to n runes (with an ellipsis).
func snippet(text string, n int) string {
	s := strings.Join(strings.Fields(text), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// --- prose report (streamed, human-readable) -------------------------------

const followupReportTTL = time.Hour

var (
	reportMu      sync.Mutex
	reportKey     string
	reportValue   string
	reportExpires time.Time
)

// followupReportSystemPrompt asks for a short, human, grounded report — the same
// "facts before reply" discipline as the digest, but in natural prose.
const followupReportPromptBase = `You are a personal assistant. From ONLY the messages below (recent mail and notes), give the user a short, natural read of what's going on and what to focus on next.

Write three short paragraphs separated by a blank line: an overview of the active topics; what needs attention (deadlines, pending replies, and any genuine contradiction); then a concrete priority for the next few days.

Rules:
- Use only what the messages say; never invent or distort.
- Ignore spam, marketing and phishing — never present them as actions.
- Plain prose, no headings or bullets, 2-4 sentences per paragraph. Minimal reasoning.`

// followupReportSystemPrompt = base + the shared prose-voice block.
var followupReportSystemPrompt = llm.WithVoice(followupReportPromptBase)

// StreamFollowUpReport streams a short, grounded natural-language report of
// recent mail + notes to `emit`, paragraph by paragraph as the model writes it.
// Cached ~1h: a fresh cache replays in one emit; a miss streams live from the
// inference model and caches the result. Returns nil (nothing emitted) when the
// brief task or LLM isn't configured.
func (d *DailyBrief) StreamFollowUpReport(ctx context.Context, projectID string, emit func(string) error) error {
	if d == nil || d.store == nil || d.llm == nil {
		return nil
	}
	items, err := d.gatherFollowupItems(ctx, projectID)
	if err != nil {
		return err
	}
	key := followupCacheKey(items, projectID)

	reportMu.Lock()
	if reportKey == key && reportValue != "" && time.Now().Before(reportExpires) {
		cached := reportValue
		reportMu.Unlock()
		return emit(cached)
	}
	reportMu.Unlock()

	if len(items) == 0 {
		msg := "Nothing new to synthesize right now. As soon as new mail or notes arrive, I'll summarize here what matters next."
		cacheReport(key, msg)
		return emit(msg)
	}

	var sb strings.Builder
	streamErr := d.llm.StreamChat(ctx, llm.ChatRequest{
		Category: "background",
		Pass:     "followup_report",
		Messages: []llm.Message{
			{Role: "system", Content: followupReportSystemPrompt},
			{Role: "user", Content: numberedItemsContext(items)},
		},
		Stream:             true,
		Temperature:        llm.Temp(0.4),
		MaxTokens:          700,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	}, func(delta string, done bool, _ *llm.Usage) error {
		if done || delta == "" {
			return nil
		}
		sb.WriteString(delta)
		return emit(delta)
	})
	if streamErr != nil {
		return streamErr // partial output left uncached → regenerated next time
	}
	if full := strings.TrimSpace(stripReasoningTags(sb.String())); full != "" {
		// Couche B: tidy the cached copy (replayed on every hit within the TTL).
		cacheReport(key, prose.Tidy(full, ""))
	}
	return nil
}

func cacheReport(key, text string) {
	reportMu.Lock()
	reportKey, reportValue, reportExpires = key, text, time.Now().Add(followupReportTTL)
	reportMu.Unlock()
}

func followupCacheKey(items []*store.KnowledgeItem, projectID string) string {
	h := sha256.New()
	fmt.Fprintf(h, "proj=%s|", projectID)
	for _, it := range items {
		fmt.Fprintf(h, "|%s@%d", it.ContentID, itemDate(it).Unix())
	}
	fmt.Fprintf(h, "|%s", time.Now().Format("2006-01-02"))
	return hex.EncodeToString(h.Sum(nil))[:16]
}
