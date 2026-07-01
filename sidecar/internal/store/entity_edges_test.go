package store

import (
	"context"
	"testing"
	"time"
)

func TestEntityEdges(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	at := now.Format(time.RFC3339)

	// Item 1: a,b,c co-occur → 3 pairs; dedup (b twice) + blank ignored.
	if err := db.UpsertCoOccurrences(ctx, []string{"a", "b", "c", "b", ""}, at); err != nil {
		t.Fatalf("upsert1: %v", err)
	}
	// Item 2: a,b again (unsorted) → (a,b) co_count = 2.
	if err := db.UpsertCoOccurrences(ctx, []string{"b", "a"}, at); err != nil {
		t.Fatalf("upsert2: %v", err)
	}
	if n, _ := db.EntityEdgeCount(ctx); n != 3 {
		t.Errorf("edge count = %d, want 3 (a-b, a-c, b-c)", n)
	}

	// K_MAX cap: 15 entities → only the first 12 (sorted) form pairs = C(12,2)=66.
	if err := db.ClearEntityEdges(ctx); err != nil {
		t.Fatalf("clear: %v", err)
	}
	many := make([]string, 0, 15)
	for i := 0; i < 15; i++ {
		many = append(many, string(rune('A'+i)))
	}
	db.UpsertCoOccurrences(ctx, many, at)
	if cnt, _ := db.EntityEdgeCount(ctx); cnt != 66 {
		t.Errorf("K_MAX cap: edges = %d, want 66 (12 choose 2)", cnt)
	}

	// <2 distinct norms → no-op.
	db.ClearEntityEdges(ctx)
	db.UpsertCoOccurrences(ctx, []string{"solo", "solo"}, at)
	if cnt, _ := db.EntityEdgeCount(ctx); cnt != 0 {
		t.Errorf("single distinct entity must create no edge, got %d", cnt)
	}
}

// HebbianNeighbors weights edges by NPMI (using entity_mentions marginals), so a
// specific partner outranks a hub even at a comparable raw co-occurrence count, and a
// pair that co-occurs only by chance (given both are frequent) gets a negative weight.
func TestHebbianNeighborsNPMI(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	at := now.Format(time.RFC3339)

	add := func(cid, when string, norms ...string) {
		if err := db.InsertKnowledgeItem(ctx, &KnowledgeItem{
			ContentID: cid, SourceType: SourceTypeNote, Title: cid,
			NormalizedText: "x", VersionID: "v1", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("item %s: %v", cid, err)
		}
		ms := make([]EntityMention, len(norms))
		for i, n := range norms {
			ms[i] = EntityMention{EntityNorm: n}
		}
		if err := db.ReplaceEntityMentions(ctx, cid, ms); err != nil {
			t.Fatalf("mentions %s: %v", cid, err)
		}
		if len(norms) >= 2 {
			if err := db.UpsertCoOccurrences(ctx, norms, when); err != nil {
				t.Fatalf("cooc %s: %v", cid, err)
			}
		}
	}

	// s co-occurs with the specific partner p (both of p's items) and once with hub h.
	// h is a hub: present in many items, mostly alone.
	add("i1", at, "s", "p")
	add("i2", at, "s", "p")
	add("i3", at, "s", "h")
	add("i4", at, "h")
	add("i5", at, "h")
	add("i6", at, "h")
	add("i7", at, "h")

	// minWeight 0 keeps only positively-associated neighbours: p (NPMI>0), not hub h (<0).
	pos, err := db.HebbianNeighbors(ctx, "s", now, 0, 10)
	if err != nil {
		t.Fatalf("neighbors: %v", err)
	}
	if len(pos) != 1 || pos[0] != "p" {
		t.Errorf("positive neighbours of s = %v, want [p] (hub h demoted below 0)", pos)
	}
	// Lowering the floor surfaces the hub too, but always ranked below the specific pair.
	w, _ := db.HebbianNeighborsWeighted(ctx, "s", now, -1, 10)
	if len(w) != 2 || w[0].Norm != "p" || w[0].Weight <= 0 || w[1].Weight >= 0 {
		t.Errorf("weighted neighbours of s = %+v, want p (>0) before h (<0)", w)
	}

	// Recency: a perfectly-co-occurring pair (NPMI=1) 240 d old (2 half-lives) → ~0.25.
	oldAt := now.AddDate(0, 0, -240).Format(time.RFC3339)
	add("ixy", oldAt, "x", "y")
	if dn, _ := db.HebbianNeighbors(ctx, "x", now, 0.5, 10); len(dn) != 0 {
		t.Errorf("decayed (~0.25) should fail minWeight 0.5: %v", dn)
	}
	if dn, _ := db.HebbianNeighbors(ctx, "x", now, 0.2, 10); len(dn) != 1 || dn[0] != "y" {
		t.Errorf("decayed (~0.25) should pass minWeight 0.2: %v", dn)
	}
}
