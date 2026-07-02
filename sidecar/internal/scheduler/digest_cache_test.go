package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/config"
	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/llm"
)

// WP20 — the contradictions surface is served stale-while-revalidate on the request
// path: an expired cache entry is returned IMMEDIATELY (no serial LLM reconcile) and a
// background refresh is scheduled so the next request converges.
func TestSemanticContradictionsCached_StaleWhileRevalidate(t *testing.T) {
	db := newTestDB(t)
	// A reachable (but here unused) LLM: with an empty corpus there are no candidates,
	// so the background refresh completes without ever calling it.
	server := fakeLLMServer(t, "ok", false)
	defer server.Close()
	llmClient := llm.NewClientWithHTTP(server.URL, 5*time.Second, 0, server.Client())
	cfg := config.DailyBriefConfig{Enabled: true, LookbackHours: 24, MaxItems: 50}
	d := NewDailyBrief(db, llmClient, events.NewBroker(), cfg, newTestLogger())

	const scope = "swr-scope" // isolated key so sibling tests don't collide on the global cache
	key := "proj=" + scope

	// Seed an EXPIRED in-memory entry carrying a recognizable stale conflict.
	stale := []contradict.ReconciledConflict{{
		ClaimConflict: contradict.ClaimConflict{Key: "stale-key"},
		Verdict:       contradict.Verdict{Kind: "conflict"},
	}}
	semContraMu.Lock()
	semContraCache[key] = semContraEntry{conflicts: stale, scanned: 42, expires: time.Now().Add(-time.Hour)}
	semContraMu.Unlock()
	t.Cleanup(func() {
		semContraMu.Lock()
		delete(semContraCache, key)
		semContraMu.Unlock()
	})

	// Request path: the STALE value is served immediately, without blocking on the LLM.
	conflicts, scanned := d.SemanticContradictionsCached(context.Background(), scope)
	if scanned != 42 || len(conflicts) != 1 || conflicts[0].Key != "stale-key" {
		t.Fatalf("expected the stale value served immediately, got scanned=%d conflicts=%+v", scanned, conflicts)
	}

	// And a refresh was scheduled: SemanticContradictions write-throughs the durable
	// cache, so the row appears shortly after.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, _, _, found, _ := db.GetContradictionCache(context.Background(), scope); found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stale-while-revalidate did not schedule a background refresh (durable cache never written)")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// WP20 — UpcomingItems is process-cached (15m TTL, invalidated by a knowledge_items
// count change). An unchanged corpus with a fresh TTL must serve the cache verbatim
// (no corpus reload); a count change or an expired TTL must recompute.
func TestUpcomingItems_ProcessCache(t *testing.T) {
	db := newTestDB(t)
	server := fakeLLMServer(t, "ok", false)
	defer server.Close()
	llmClient := llm.NewClientWithHTTP(server.URL, 5*time.Second, 0, server.Client())
	d := NewDailyBrief(db, llmClient, events.NewBroker(), config.DailyBriefConfig{Enabled: true}, newTestLogger())

	ctx := context.Background()
	// One benign item: non-empty corpus, but no recurrence/decision → empty result.
	insertItem(t, db, "note:1", "note", "hello", "world", time.Now(), nil)

	if got := d.UpcomingItems(ctx, 45); len(got) != 0 {
		t.Fatalf("expected empty upcoming for a benign corpus, got %+v", got)
	}

	// Inject a sentinel with the SAME count and a fresh TTL. A second call must return
	// it verbatim — proving the corpus was NOT reloaded/recomputed.
	count, _ := db.CountKnowledgeItems(ctx)
	sentinel := []Upcoming{{Kind: "recurrence", Title: "SENTINEL", At: "2999-01-01T00:00:00Z", Detail: "cached"}}
	d.upcomingMu.Lock()
	d.upcomingCache[45] = upcomingCacheEntry{items: sentinel, count: count, expires: time.Now().Add(time.Minute)}
	d.upcomingMu.Unlock()
	if got := d.UpcomingItems(ctx, 45); len(got) != 1 || got[0].Title != "SENTINEL" {
		t.Fatalf("unchanged corpus + fresh TTL must serve the cache (no reload), got %+v", got)
	}

	// A corpus change (count moves) invalidates the cache → recompute drops the sentinel.
	insertItem(t, db, "note:2", "note", "second", "item", time.Now(), nil)
	if got := d.UpcomingItems(ctx, 45); len(got) != 0 {
		t.Fatalf("a changed corpus must invalidate the cache (recompute), got %+v", got)
	}

	// An expired TTL also invalidates, even with an unchanged count.
	count, _ = db.CountKnowledgeItems(ctx)
	d.upcomingMu.Lock()
	d.upcomingCache[45] = upcomingCacheEntry{items: sentinel, count: count, expires: time.Now().Add(-time.Minute)}
	d.upcomingMu.Unlock()
	if got := d.UpcomingItems(ctx, 45); len(got) != 0 {
		t.Fatalf("an expired TTL must invalidate the cache (recompute), got %+v", got)
	}
}
