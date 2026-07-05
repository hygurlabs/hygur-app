package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// Two ingested copies of the SAME content (same content_hash, different
// content_id — e.g. the same attachment sent twice in a thread) with the same
// statement must mint exactly ONE decision, not two.
func TestProposeDedupOnContentHash(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	s := &DecisionScanner{store: db, logger: zerolog.Nop()}
	now := time.Now()

	copyA := &store.KnowledgeItem{ContentID: "file:copyA", Metadata: map[string]any{"content_hash": "abc123"}}
	copyB := &store.KnowledgeItem{ContentID: "file:copyB", Metadata: map[string]any{"content_hash": "abc123"}}
	cand := decisionCandidate{Statement: "Sign the lease by Friday", Quote: "sign the lease by friday"}

	added, err := s.propose(ctx, copyA, cand, now)
	if err != nil || !added {
		t.Fatalf("first propose: added=%v err=%v", added, err)
	}
	added, err = s.propose(ctx, copyB, cand, now)
	if err != nil {
		t.Fatalf("second propose err: %v", err)
	}
	if added {
		t.Error("second copy (same content_hash + statement) minted a duplicate decision")
	}
	all, _ := db.ListDecisions(ctx, "", "")
	if len(all) != 1 {
		t.Fatalf("want exactly 1 decision, got %d", len(all))
	}
}

func TestDecisionDedupRef(t *testing.T) {
	withHash := &store.KnowledgeItem{ContentID: "file:x", Metadata: map[string]any{"content_hash": "h1"}}
	if got := decisionDedupRef(withHash); got != "hash:h1" {
		t.Errorf("content_hash item → %q, want hash:h1", got)
	}
	noHash := &store.KnowledgeItem{ContentID: "note:y", Metadata: map[string]any{}}
	if got := decisionDedupRef(noHash); got != "note:y" {
		t.Errorf("hashless item → %q, want note:y", got)
	}
	blank := &store.KnowledgeItem{ContentID: "note:z", Metadata: map[string]any{"content_hash": "  "}}
	if got := decisionDedupRef(blank); got != "note:z" {
		t.Errorf("blank hash → %q, want fallback note:z", got)
	}
}

// A metadata-only backfill bumps updated_at without changing content; such an
// item (ingested before the scan window) must be skipped, not re-scanned.
func TestScanSkipUnchanged(t *testing.T) {
	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	old := &store.KnowledgeItem{CreatedAt: since.AddDate(0, 0, -10)} // ingested before window
	if !scanSkipUnchanged(old, since) {
		t.Error("metadata-only updated_at bump on an old item must be skipped")
	}
	fresh := &store.KnowledgeItem{CreatedAt: since.AddDate(0, 0, 1)} // ingested inside window
	if scanSkipUnchanged(fresh, since) {
		t.Error("freshly-ingested item must be scanned")
	}
}

func TestParseDecisionsVerbatimGate(t *testing.T) {
	source := "After review we decided to proceed with vendor A and to sign the lease by Friday. We are still weighing the budget."

	raw := "```json\n" + `[
	  {"statement": "Proceed with vendor A", "quote": "decided to proceed with vendor A"},
	  {"statement": "Sign the lease by Friday", "quote": "to sign the lease by Friday"},
	  {"statement": "Hire two engineers next quarter", "quote": "we will hire two engineers next quarter"}
	]` + "\n```"

	got := parseDecisions(raw, source)
	// The third is dropped: its quote is not verbatim in the source (anti-hallucination).
	if len(got) != 2 {
		t.Fatalf("want 2 gated decisions, got %d: %+v", len(got), got)
	}
	if got[0].Statement != "Proceed with vendor A" || got[1].Statement != "Sign the lease by Friday" {
		t.Errorf("unexpected statements: %+v", got)
	}
}

func TestParseDecisionsEmptyAndMalformed(t *testing.T) {
	if got := parseDecisions("no array here", "src"); got != nil {
		t.Errorf("no array → nil, got %+v", got)
	}
	if got := parseDecisions("[]", "src"); len(got) != 0 {
		t.Errorf("empty array → 0, got %+v", got)
	}
	// A candidate missing the quote, or with a blank statement, is dropped.
	raw := `[{"statement": "", "quote": "x"}, {"statement": "ok", "quote": ""}]`
	if got := parseDecisions(raw, "x ok"); len(got) != 0 {
		t.Errorf("incomplete candidates → 0, got %+v", got)
	}
}

func TestParseDecisionsPerItemCap(t *testing.T) {
	source := "a b c d e f g"
	raw := `[
	  {"statement":"s1","quote":"a"},
	  {"statement":"s2","quote":"b"},
	  {"statement":"s3","quote":"c"},
	  {"statement":"s4","quote":"d"},
	  {"statement":"s5","quote":"e"},
	  {"statement":"s6","quote":"f"},
	  {"statement":"s7","quote":"g"}
	]`
	got := parseDecisions(raw, source)
	if len(got) != decisionMaxPerItem {
		t.Fatalf("want cap %d, got %d", decisionMaxPerItem, len(got))
	}
}
