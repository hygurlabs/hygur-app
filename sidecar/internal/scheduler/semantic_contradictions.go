package scheduler

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/store"
)

// W6 stage 3c — the REDUCE surface. Ties the deterministic candidate detection
// (DetectClaimConflicts) to the LLM reconciliation (Reconcile), scoped to a project
// (else all mail + notes). The reconciliation is LLM-backed, so results are cached
// ~1h per scope; claims change rarely (one-time backfill + incremental), so a stale
// hour is fine.

const semanticContradictionsTTL = time.Hour

// contradictionRecencyDays bounds how far back claims are considered for
// contradictions: older ones are stale (a year-old "available this week" is
// meaningless now) and only add noise. Env-overridable; 0/unset = the default.
const contradictionRecencyDays = 120

// contradictionSince returns the RFC3339/UTC cutoff for the recency window.
func contradictionSince() string {
	days := contradictionRecencyDays
	if v := strings.TrimSpace(os.Getenv("HYGUR_CONTRADICT_RECENCY_DAYS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			days = n
		}
	}
	return time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
}

type semContraEntry struct {
	conflicts []contradict.ReconciledConflict
	scanned   int
	expires   time.Time
}

var (
	semContraMu    sync.Mutex
	semContraCache = map[string]semContraEntry{}
)

// SemanticContradictions returns the W6 reconciled conflicts for a scope: cross-
// source claim divergences classified by the LLM into conflict / supersedes (the
// "none" verdicts are already dropped by Reconcile). projectID "" = all mail+notes.
func (d *DailyBrief) SemanticContradictions(ctx context.Context, projectID string) ([]contradict.ReconciledConflict, int, error) {
	if d == nil || d.store == nil {
		return nil, 0, nil
	}
	key := "proj=" + projectID
	semContraMu.Lock()
	if e, ok := semContraCache[key]; ok && time.Now().Before(e.expires) {
		semContraMu.Unlock()
		return e.conflicts, e.scanned, nil
	}
	semContraMu.Unlock()

	// Cold in-memory cache (e.g. after a restart): fall back to the durable cache
	// before recomputing — the reconcile is LLM-backed, so a fresh-enough row
	// saves the cost. Repopulates the in-memory cache.
	if js, scanned, age, found, err := d.store.GetContradictionCache(ctx, projectID); err == nil && found && age < semanticContradictionsTTL {
		var cached []contradict.ReconciledConflict
		if json.Unmarshal([]byte(js), &cached) == nil {
			if cached == nil {
				cached = []contradict.ReconciledConflict{}
			}
			semContraMu.Lock()
			semContraCache[key] = semContraEntry{conflicts: cached, scanned: scanned, expires: time.Now().Add(semanticContradictionsTTL - age)}
			semContraMu.Unlock()
			return cached, scanned, nil
		}
	}

	items, err := d.contradictionItems(ctx, projectID)
	if err != nil {
		return nil, 0, err
	}
	since := contradictionSince()
	captureCandidates := contradict.DetectClaimConflicts(items, since)
	// G4: anchor standing decisions as cross-thread (entity, attribute) candidates so
	// a fresh capture contradicting a confirmed decision surfaces through the SAME
	// reconcile pipeline (and a "supersedes" verdict means the decision is overtaken).
	decItems, decidedAt := d.standingDecisionItems(ctx, projectID)
	decisionConflicts := contradict.DetectDecisionConflicts(decItems, decidedAt, items, since)
	candidates := append(captureCandidates, decisionConflicts...)
	reconciled := d.reconcileCached(ctx, candidates)
	// Observability: one structured line per scan — greppable to see if the scan ran,
	// what it found (incl. G4 decision candidates), and how many survived reconcile.
	d.logger.Info().Str("scope", projectID).Int("items", len(items)).
		Int("capture_candidates", len(captureCandidates)).Int("decision_candidates", len(decisionConflicts)).
		Int("reconciled", len(reconciled)).Msg("contradiction scan")
	if reconciled == nil {
		reconciled = []contradict.ReconciledConflict{}
	}

	semContraMu.Lock()
	semContraCache[key] = semContraEntry{conflicts: reconciled, scanned: len(items), expires: time.Now().Add(semanticContradictionsTTL)}
	semContraMu.Unlock()
	// Write-through to the durable cache so Ask + the digest can read it cheaply,
	// and a restart doesn't force a recompute. Best-effort.
	if blob, mErr := json.Marshal(reconciled); mErr == nil {
		_ = d.store.PutContradictionCache(ctx, projectID, string(blob), len(items))
	}
	return reconciled, len(items), nil
}

// reconcileCached judges the candidate clusters using the durable per-cluster
// verdict cache: a cluster Key encodes its exact claim set, so a verdict holds for
// that Key forever. Only clusters with no cached verdict (new or claim-changed)
// hit the LLM; every fresh verdict (including "none") is stored so it is never
// re-judged. Returns only the real conflicts/supersedes (drops "none").
func (d *DailyBrief) reconcileCached(ctx context.Context, candidates []contradict.ClaimConflict) []contradict.ReconciledConflict {
	cache, err := d.store.GetReconcileVerdicts(ctx)
	if err != nil {
		d.logger.Debug().Err(err).Msg("reconcile verdict cache read; judging all")
		cache = map[string]store.CachedVerdict{}
	}
	out := make([]contradict.ReconciledConflict, 0, len(candidates))
	for _, c := range candidates {
		v, ok := cache[c.Key]
		if !ok {
			ver, _ := contradict.ReconcileClaimConflict(ctx, d.llm, c) // "none" on error (fail-closed)
			v = store.CachedVerdict{Kind: ver.Kind, Reason: ver.Reason}
			if v.Kind == "" {
				v.Kind = "none"
			}
			if perr := d.store.PutReconcileVerdict(ctx, c.Key, v.Kind, v.Reason); perr != nil {
				d.logger.Debug().Err(perr).Msg("cache reconcile verdict")
			}
		}
		if v.Kind == "conflict" || v.Kind == "supersedes" {
			out = append(out, contradict.ReconciledConflict{ClaimConflict: c, Verdict: contradict.Verdict{Kind: v.Kind, Reason: v.Reason}})
		}
	}
	return out
}

// contradictionItems gathers the corpus for detection: a project's items (complete,
// so threads aren't split), else all mail + notes.
func (d *DailyBrief) contradictionItems(ctx context.Context, projectID string) ([]*store.KnowledgeItem, error) {
	if projectID != "" {
		return d.store.GetItemsForProject(ctx, projectID)
	}
	var items []*store.KnowledgeItem
	for _, src := range store.MailAndSourceTypes(store.SourceTypeNote) {
		const batch = 500
		for offset := 0; ; offset += batch {
			page, err := d.store.ListKnowledgeItemsBySourceType(ctx, src, batch, offset)
			if err != nil {
				return nil, err
			}
			items = append(items, page...)
			if len(page) < batch {
				break
			}
		}
	}
	return items, nil
}

// standingDecisionItems loads the standing decisions in scope as knowledge_items,
// ensuring each carries extracted claims (extracted once on the small indexing
// model, then cached in metadata), plus a content_id → decided-on map. These feed
// DetectDecisionConflicts (G4). Fail-open: a decision that can't be loaded or
// extracted is skipped. projectID "" = all standing decisions.
func (d *DailyBrief) standingDecisionItems(ctx context.Context, projectID string) ([]*store.KnowledgeItem, map[string]string) {
	decs, err := d.store.ListDecisions(ctx, projectID, store.DecisionStanding)
	if err != nil || len(decs) == 0 {
		return nil, nil
	}
	idx := d.indexing
	if idx == nil {
		idx = d.llm // the small model is preferred but not required
	}
	items := make([]*store.KnowledgeItem, 0, len(decs))
	decidedAt := make(map[string]string, len(decs))
	var extracted, failed int
	for _, dec := range decs {
		it, gerr := d.store.GetKnowledgeItem(ctx, dec.ID)
		if gerr != nil || it == nil {
			continue
		}
		decidedAt[it.ContentID] = dec.DecidedOn
		// Ensure the decision's own claim is extracted + cached (off the chat budget).
		if !hasExtractedClaims(it.Metadata) && idx != nil {
			text := strings.TrimSpace(it.Title + "\n" + it.NormalizedText)
			claims, eerr := contradict.ExtractClaims(ctx, idx, text)
			switch {
			case eerr != nil:
				failed++
				d.logger.Warn().Err(eerr).Str("decision", it.ContentID).Msg("G4 decision-claim extraction failed (fail-open)")
			case len(claims) > 0:
				if it.Metadata == nil {
					it.Metadata = map[string]any{}
				}
				it.Metadata["extracted_claims"] = claims
				if uerr := d.store.UpdateKnowledgeItem(ctx, it); uerr != nil {
					d.logger.Debug().Err(uerr).Str("decision", it.ContentID).Msg("persist decision claims")
				}
				extracted++
			}
		}
		items = append(items, it)
	}
	if extracted+failed > 0 {
		d.logger.Info().Int("standing", len(decs)).Int("extracted", extracted).Int("failed", failed).Msg("G4 decision-claim extraction")
	}
	return items, decidedAt
}

func hasExtractedClaims(m map[string]any) bool {
	if m == nil {
		return false
	}
	_, ok := m["extracted_claims"]
	return ok
}

// UpcomingRecurrences is the prospection surface (Conséquence P-1): recurring
// subjects whose predicted next occurrence falls within the next withinDays (and
// not more than a week overdue). Deterministic (DetectRecurrence over mail+notes);
// the digest renders it as "what's coming". Nil-safe.
func (d *DailyBrief) UpcomingRecurrences(ctx context.Context, withinDays int) []contradict.Recurrence {
	if d == nil || d.store == nil {
		return nil
	}
	items, err := d.contradictionItems(ctx, "")
	if err != nil {
		return nil
	}
	now := time.Now().UTC()
	from, horizon := now.AddDate(0, 0, -7), now.AddDate(0, 0, withinDays)
	var out []contradict.Recurrence
	for _, r := range contradict.DetectRecurrence(items, 3) {
		t, perr := time.Parse(time.RFC3339, r.NextAt)
		if perr != nil || t.Before(from) || t.After(horizon) {
			continue
		}
		out = append(out, r)
	}
	return out
}
