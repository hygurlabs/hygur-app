package retrieval

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/store"
)

// detectQuerySubject finds the most-anchoring known entity named in a query, purely by
// matching normalized n-grams against the entity index — no LLM. Most specific (longest
// n-gram) wins, then most central (most mentions); unknown subjects return "".
func TestDetectQuerySubject(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()

	// Store norms the way ingestion does (via NormKey) so detection (also NormKey) matches.
	mk := func(cid string, entities ...string) {
		if err := db.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
			ContentID: cid, SourceType: store.SourceTypeNote, Title: cid,
			NormalizedText: "x", VersionID: "v1", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert %s: %v", cid, err)
		}
		ms := make([]store.EntityMention, len(entities))
		for i, e := range entities {
			ms[i] = store.EntityMention{EntityNorm: contradict.NormKey(e)}
		}
		if err := db.ReplaceEntityMentions(ctx, cid, ms); err != nil {
			t.Fatalf("mentions %s: %v", cid, err)
		}
	}
	mk("i1", "Acme", "report")
	mk("i2", "Acme")
	mk("i3", "Acme", "Alice Bernard")
	mk("i4", "Alice Bernard")

	cases := []struct {
		q    string
		want string
	}{
		{"What is linked to Acme ?", contradict.NormKey("Acme")},
		{"My last meeting about Alice Bernard", contradict.NormKey("Alice Bernard")},
		{"Any news this week ?", ""},  // no known entity named
		{"where is the report ?", ""}, // "report" is indexed but lowercase → guarded out
		{"", ""},
	}
	for _, c := range cases {
		got, err := detectQuerySubject(ctx, db, c.q)
		if err != nil {
			t.Fatalf("detect %q: %v", c.q, err)
		}
		if got != c.want {
			t.Errorf("detect %q = %q, want %q", c.q, got, c.want)
		}
	}
}
