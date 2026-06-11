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
	candidates := contradict.DetectClaimConflicts(items, contradictionSince())
	reconciled := contradict.Reconcile(ctx, d.llm, candidates)
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
