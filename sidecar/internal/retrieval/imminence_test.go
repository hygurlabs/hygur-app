package retrieval

import (
	"context"
	"testing"
)

func ids(rs []UnifiedResult) []string {
	out := make([]string, len(rs))
	for i := range rs {
		out[i] = rs[i].ContentID
	}
	return out
}

func TestApplyImminenceRescore(t *testing.T) {
	ctx := context.Background()
	imminent := func(context.Context) map[string]struct{} { return map[string]struct{}{"b": {}} }

	t.Run("boosts and reorders the imminent item", func(t *testing.T) {
		us := &UnifiedSearcher{}
		us.SetImminenceRerank(true)
		us.SetImminentIDsFunc(imminent)
		results := []UnifiedResult{
			{ContentID: "a", Score: 1.00},
			{ContentID: "b", Score: 0.95}, // imminent → ×1.15 = 1.0925 → moves to top
			{ContentID: "c", Score: 0.90},
		}
		us.applyImminenceRescore(ctx, results)
		if results[0].ContentID != "b" {
			t.Fatalf("imminent item should rank first; got %v", ids(results))
		}
		// Boost is bounded (+15%): b cannot leapfrog an item more than 15% above it.
		if results[0].Score <= 1.00 || results[0].Score > 0.95*1.15+1e-9 {
			t.Fatalf("b score out of expected band: %v", results[0].Score)
		}
	})

	t.Run("no-op when disabled", func(t *testing.T) {
		us := &UnifiedSearcher{}
		us.SetImminentIDsFunc(imminent) // provider set but rerank off
		results := []UnifiedResult{{ContentID: "a", Score: 1.0}, {ContentID: "b", Score: 0.95}}
		us.applyImminenceRescore(ctx, results)
		if ids(results)[0] != "a" {
			t.Fatalf("disabled rerank must leave order intact; got %v", ids(results))
		}
	})

	t.Run("no-op on empty imminent set", func(t *testing.T) {
		us := &UnifiedSearcher{}
		us.SetImminenceRerank(true)
		us.SetImminentIDsFunc(func(context.Context) map[string]struct{} { return nil })
		results := []UnifiedResult{{ContentID: "a", Score: 1.0}, {ContentID: "b", Score: 0.95}}
		us.applyImminenceRescore(ctx, results)
		if ids(results)[0] != "a" {
			t.Fatalf("empty set must leave order intact; got %v", ids(results))
		}
	})
}
