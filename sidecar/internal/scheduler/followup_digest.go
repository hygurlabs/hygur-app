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

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

// FollowUpDigest is the grounded synthesis shown in the Follow-up view: a few
// salient topics with short factual notes, plus contradictions surfaced ONLY
// when the model can cite >=2 genuinely-conflicting items. Every claim is tied
// to real source items (references validated server-side); nothing is invented.
type FollowUpDigest struct {
	Topics         []DigestEntry `json:"topics"`
	Contradictions []DigestEntry `json:"contradictions"`
	Scanned        int           `json:"scanned"`
	Window         string        `json:"window"`
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
const followupSystemPrompt = `Tu es l'assistant d'un indépendant. À partir UNIQUEMENT de la liste numérotée de messages ci-dessous, tu produis une courte synthèse de suivi.

Tu réponds EXCLUSIVEMENT par un objet JSON valide, sans texte autour ni balises de code :
{"topics":[{"title":"...","note":"...","refs":[1,4]}],"contradictions":[{"note":"...","refs":[2,9]}]}

Règles STRICTES :
- "topics" : regroupe les messages liés en quelques sujets actifs (au plus 6). Pour chaque sujet, "note" = 1 à 2 phrases FACTUELLES tirées des messages, et "refs" = les numéros des messages concernés. N'invente AUCUN sujet absent des messages.
- "contradictions" : UNIQUEMENT quand au moins DEUX messages se contredisent réellement sur la MÊME chose (un montant, une date, une décision, un engagement). "refs" doit citer au moins deux numéros DIFFÉRENTS. Si aucune contradiction réelle n'existe, renvoie "contradictions": [].
- N'invente JAMAIS un montant, une date, un nom ni un fait absent des messages. N'utilise que ce qui est écrit.
- Deux factures mensuelles différentes, ou deux événements distincts, NE SONT PAS des contradictions. Ne les signale pas.
- Français. Raisonnement interne minimal.`

// FollowUp returns a grounded, LLM-written digest of recent mail + notes:
// salient topics and any real contradictions, each cited to source items.
// Empty (no topics, no contradictions) when there's nothing factual to report.
// Cached ~1h.
func (d *DailyBrief) FollowUp(ctx context.Context) (FollowUpDigest, error) {
	if d == nil || d.store == nil || d.llm == nil {
		return FollowUpDigest{}, nil
	}
	items, err := d.gatherFollowupItems(ctx)
	if err != nil {
		return FollowUpDigest{}, err
	}
	window := fmt.Sprintf("last %d days", followupWindowDays)
	if len(items) == 0 {
		return FollowUpDigest{Window: window}, nil
	}

	key := followupCacheKey(items)
	followupMu.Lock()
	if followupKey == key && time.Now().Before(followupExpires) {
		v := followupValue
		followupMu.Unlock()
		return v, nil
	}
	followupMu.Unlock()

	digest := d.generateFollowup(ctx, items)
	digest.Scanned = len(items)
	digest.Window = window

	followupMu.Lock()
	followupKey, followupValue, followupExpires = key, digest, time.Now().Add(followupTTL)
	followupMu.Unlock()
	return digest, nil
}

// gatherFollowupItems pulls recent mail + notes within the window, newest first,
// capped to keep the LLM prompt bounded.
func (d *DailyBrief) gatherFollowupItems(ctx context.Context) ([]*store.KnowledgeItem, error) {
	since := time.Now().Add(-followupWindowDays * 24 * time.Hour)
	var all []*store.KnowledgeItem
	for _, src := range []string{"mail", "note"} {
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
		if itemDate(it).After(since) {
			recent = append(recent, it)
		}
	}
	sort.Slice(recent, func(i, j int) bool {
		return itemDate(recent[i]).After(itemDate(recent[j]))
	})
	if len(recent) > followupMaxItems {
		recent = recent[:followupMaxItems]
	}
	return recent, nil
}

// numberedItemsContext renders the items as a numbered list the model can cite
// by index. Shared by the structured digest and the prose report.
func numberedItemsContext(items []*store.KnowledgeItem) string {
	var sb strings.Builder
	sb.WriteString("Messages (numérotés) :\n")
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
		Messages: []llm.Message{
			{Role: "system", Content: followupSystemPrompt},
			{Role: "user", Content: numberedItemsContext(items)},
		},
		Temperature:        0.2,
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
func itemDate(it *store.KnowledgeItem) time.Time {
	if cd := store.GetCanonicalDate(it); !cd.IsZero() {
		return cd
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
const followupReportSystemPrompt = `Tu es l'assistant personnel d'un indépendant. Tu fais le point, comme un vrai assistant qui lui parle, sur ce qui ressort de ses messages récents et sur ce qui mérite son attention pour la suite.

À partir UNIQUEMENT des messages listés ci-dessous (mails et notes récents), écris EXACTEMENT trois paragraphes en français, d'un ton naturel et humain :
1. Une vue d'ensemble : les sujets qui occupent la période.
2. Ce qui mérite attention pour la suite : échéances, demandes en attente, et — UNIQUEMENT si deux messages se contredisent réellement sur la même chose — la contradiction à clarifier.
3. Une suggestion de priorités concrète pour les prochains jours.

Règles STRICTES :
- N'utilise QUE les faits présents dans les messages. N'invente JAMAIS un montant, une date, un nom, une décision ni un événement absent.
- Si une information n'est pas dans les messages, ne la devine pas et ne la mentionne pas.
- Trois paragraphes de prose séparés par une ligne vide. Pas de titres, pas de puces, pas de formule de politesse, pas de préambule du type « Voici ».
- Concis : 2 à 4 phrases par paragraphe.
- Raisonnement interne minimal.`

// StreamFollowUpReport streams a short, grounded natural-language report of
// recent mail + notes to `emit`, paragraph by paragraph as the model writes it.
// Cached ~1h: a fresh cache replays in one emit; a miss streams live from the
// inference model and caches the result. Returns nil (nothing emitted) when the
// brief task or LLM isn't configured.
func (d *DailyBrief) StreamFollowUpReport(ctx context.Context, emit func(string) error) error {
	if d == nil || d.store == nil || d.llm == nil {
		return nil
	}
	items, err := d.gatherFollowupItems(ctx)
	if err != nil {
		return err
	}
	key := followupCacheKey(items)

	reportMu.Lock()
	if reportKey == key && reportValue != "" && time.Now().Before(reportExpires) {
		cached := reportValue
		reportMu.Unlock()
		return emit(cached)
	}
	reportMu.Unlock()

	if len(items) == 0 {
		msg := "Rien de neuf à synthétiser pour l'instant. Dès que de nouveaux mails ou de nouvelles notes arriveront, je ferai le point ici sur ce qui compte pour la suite."
		cacheReport(key, msg)
		return emit(msg)
	}

	var sb strings.Builder
	streamErr := d.llm.StreamChat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: followupReportSystemPrompt},
			{Role: "user", Content: numberedItemsContext(items)},
		},
		Stream:             true,
		Temperature:        0.4,
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
		cacheReport(key, full)
	}
	return nil
}

func cacheReport(key, text string) {
	reportMu.Lock()
	reportKey, reportValue, reportExpires = key, text, time.Now().Add(followupReportTTL)
	reportMu.Unlock()
}

func followupCacheKey(items []*store.KnowledgeItem) string {
	h := sha256.New()
	for _, it := range items {
		fmt.Fprintf(h, "|%s@%d", it.ContentID, itemDate(it).Unix())
	}
	fmt.Fprintf(h, "|%s", time.Now().Format("2006-01-02"))
	return hex.EncodeToString(h.Sum(nil))[:16]
}
