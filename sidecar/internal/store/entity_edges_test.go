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

	// Neighbours of "a": b (weight 2) before c (weight 1); minWeight 0.5 → both.
	neigh, err := db.HebbianNeighbors(ctx, "a", now, 0.5, 10)
	if err != nil {
		t.Fatalf("neighbors: %v", err)
	}
	if len(neigh) != 2 || neigh[0] != "b" {
		t.Errorf("neighbors of a = %v, want b first then c", neigh)
	}
	// minWeight 1.5 → only b (weight 2); c (weight 1) excluded.
	if hi, _ := db.HebbianNeighbors(ctx, "a", now, 1.5, 10); len(hi) != 1 || hi[0] != "b" {
		t.Errorf("neighbors of a (w≥1.5) = %v, want [b]", hi)
	}

	// Time decay: 240 d = 2 half-lives → weight ~0.25.
	if err := db.ClearEntityEdges(ctx); err != nil {
		t.Fatalf("clear: %v", err)
	}
	oldAt := now.AddDate(0, 0, -240).Format(time.RFC3339)
	db.UpsertCoOccurrences(ctx, []string{"x", "y"}, oldAt)
	if dn, _ := db.HebbianNeighbors(ctx, "x", now, 0.5, 10); len(dn) != 0 {
		t.Errorf("decayed (0.25) should fail minWeight 0.5: %v", dn)
	}
	if dn, _ := db.HebbianNeighbors(ctx, "x", now, 0.2, 10); len(dn) != 1 {
		t.Errorf("decayed (0.25) should pass minWeight 0.2: %v", dn)
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
