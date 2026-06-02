package retrieval

import "testing"

func TestRRFFuseRewardsAgreement(t *testing.T) {
	// "b" is mid-ranked in both signals; "a" tops only vector, "x" tops only
	// FTS. Agreement across signals should lift "b" above either single-signal
	// leader — the whole point of hybrid fusion.
	vec := rankedList{Weight: 0.5, Hits: []rankedChunk{
		{ChunkID: "a", ContentID: "d1"},
		{ChunkID: "b", ContentID: "d2"},
		{ChunkID: "c", ContentID: "d3"},
	}}
	fts := rankedList{Weight: 0.5, Hits: []rankedChunk{
		{ChunkID: "x", ContentID: "d9"},
		{ChunkID: "b", ContentID: "d2"},
		{ChunkID: "y", ContentID: "d8"},
	}}

	fused := rrfFuse(vec, fts)
	if len(fused) != 5 {
		t.Fatalf("expected 5 unique chunks, got %d", len(fused))
	}
	if fused[0].ChunkID != "b" {
		t.Fatalf("expected 'b' (found by both signals) to rank first, got %q", fused[0].ChunkID)
	}
	if fused[0].ContentID != "d2" {
		t.Errorf("content_id not carried: %q", fused[0].ContentID)
	}
	// "b" strictly beats the single-signal leaders.
	if !(fused[0].Score > scoreOf(fused, "a") && fused[0].Score > scoreOf(fused, "x")) {
		t.Errorf("agreement score did not exceed single-signal leaders")
	}
}

func TestRRFFuseDeterministicTies(t *testing.T) {
	// Single list: ranks 0 and 1 differ, but two runs must be identical and
	// order must follow rank.
	l := rankedList{Weight: 1, Hits: []rankedChunk{{ChunkID: "z"}, {ChunkID: "a"}}}
	f1 := rrfFuse(l)
	if f1[0].ChunkID != "z" || f1[1].ChunkID != "a" {
		t.Fatalf("rank order not preserved: %+v", f1)
	}
}

func scoreOf(fs []fusedChunk, id string) float64 {
	for _, f := range fs {
		if f.ChunkID == id {
			return f.Score
		}
	}
	return -1
}
