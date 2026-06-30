package store

import (
	"context"
	"testing"
	"time"
)

// TestItemLinkSignalsConnectivity verifies the Phase D ⇄ Phase 1 wiring: an item
// that mentions entities present in the Hebbian co-occurrence graph (entity_edges)
// is structurally connected and earns link_count, even when never accessed —
// without becoming hard-exempt (connectivity is a soft salience signal).
func TestItemLinkSignalsConnectivity(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()

	mk := func(cid string) {
		if err := db.InsertKnowledgeItem(ctx, &KnowledgeItem{
			ContentID: cid, SourceType: SourceTypeNote, Title: cid,
			NormalizedText: "x", VersionID: "v1", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert %s: %v", cid, err)
		}
	}
	mk("item:hub")
	mk("item:lonely")

	// hub mentions alice, bob, zeta; lonely mentions only zeta.
	if err := db.ReplaceEntityMentions(ctx, "item:hub", []EntityMention{
		{EntityNorm: "alice"}, {EntityNorm: "bob"}, {EntityNorm: "zeta"},
	}); err != nil {
		t.Fatalf("mentions hub: %v", err)
	}
	if err := db.ReplaceEntityMentions(ctx, "item:lonely", []EntityMention{
		{EntityNorm: "zeta"},
	}); err != nil {
		t.Fatalf("mentions lonely: %v", err)
	}

	// alice & bob co-occur → both enter entity_edges (connected). zeta never does.
	if err := db.UpsertCoOccurrences(ctx, []string{"alice", "bob"}, now.Format(time.RFC3339)); err != nil {
		t.Fatalf("edges: %v", err)
	}

	sig, err := db.ItemLinkSignals(ctx, []string{"item:hub", "item:lonely"})
	if err != nil {
		t.Fatalf("ItemLinkSignals: %v", err)
	}
	// hub: alice + bob are graph-connected (2), zeta is not → folded into link_count.
	if got := sig["item:hub"]; got.ConnectedEntities != 2 || got.LinkCount != 2 {
		t.Errorf("hub: ConnectedEntities=%d LinkCount=%d, want 2 and 2", got.ConnectedEntities, got.LinkCount)
	}
	// lonely: only mentions zeta (not in the graph) → no connectivity (zero value).
	if got := sig["item:lonely"]; got.ConnectedEntities != 0 || got.LinkCount != 0 {
		t.Errorf("lonely: ConnectedEntities=%d LinkCount=%d, want 0 and 0", got.ConnectedEntities, got.LinkCount)
	}
	// Connectivity is a soft signal — it must NOT make the item hard-exempt.
	if sig["item:hub"].Exempt() {
		t.Error("connectivity must not flag an item hard-exempt")
	}
}
