package retrieval

import "sort"

// rrfK is the Reciprocal Rank Fusion damping constant. 60 is the value from the
// original RRF paper and the de-facto default; a larger k flattens the
// influence of the very top ranks.
const rrfK = 60

// ftsWeight is the blend weight of the lexical (BM25/FTS) signal in the hybrid
// fusion. Kept on par with a single vector list (knowledge/mail default 0.5)
// so a chunk found by both lexical and semantic search clearly outranks one
// found by a single signal.
const ftsWeight = 0.5

// rankedChunk is one entry in a ranked candidate list, ordered best-first.
type rankedChunk struct {
	ChunkID   string
	ContentID string
}

// rankedList is a single ranked retrieval signal (e.g. vector or BM25) with a
// blend weight.
type rankedList struct {
	Weight float64
	Hits   []rankedChunk
}

// fusedChunk is a chunk with its combined RRF score (higher = better).
type fusedChunk struct {
	ChunkID   string
	ContentID string
	Score     float64
}

// rrfFuse blends ranked lists with Reciprocal Rank Fusion: a chunk at 0-based
// rank r in a list of weight w contributes w/(rrfK+r+1). Contributions sum
// across lists, so a chunk surfaced by several signals outranks one found by a
// single signal — which is exactly why hybrid lexical+semantic beats either
// alone. RRF is rank-based, so it sidesteps the incompatible score scales of
// cosine vs bm25 without any per-signal calibration.
//
// Output is sorted best-first; ties break on ChunkID for deterministic order.
func rrfFuse(lists ...rankedList) []fusedChunk {
	scores := make(map[string]*fusedChunk)
	for _, l := range lists {
		w := l.Weight
		if w == 0 {
			w = 1
		}
		for r, h := range l.Hits {
			e, ok := scores[h.ChunkID]
			if !ok {
				e = &fusedChunk{ChunkID: h.ChunkID, ContentID: h.ContentID}
				scores[h.ChunkID] = e
			}
			if e.ContentID == "" {
				e.ContentID = h.ContentID
			}
			e.Score += w / float64(rrfK+r+1)
		}
	}
	out := make([]fusedChunk, 0, len(scores))
	for _, e := range scores {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ChunkID < out[j].ChunkID
	})
	return out
}
