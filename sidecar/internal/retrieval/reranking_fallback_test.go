package retrieval

import (
	"context"
	"testing"
)

// TestLLMRerankFallbackDefaultOff is a cost/DoS regression guard: the uncapped
// LLM-as-judge rerank fallback must be OFF unless explicitly enabled, and the
// empty config must leave it OFF.
func TestLLMRerankFallbackDefaultOff(t *testing.T) {
	us := NewUnifiedSearcher(nil, nil)
	if us.useLLMRerankFallback {
		t.Fatal("LLM rerank fallback must be OFF by default")
	}
	us.SetRetrievalOptions(RetrievalOptions{}) // empty opts → still OFF
	if us.useLLMRerankFallback {
		t.Error("empty RetrievalOptions must leave the LLM rerank fallback OFF")
	}
	us.SetLLMRerankFallback(true)
	if !us.useLLMRerankFallback {
		t.Error("SetLLMRerankFallback(true) should enable it")
	}
	us.SetRetrievalOptions(RetrievalOptions{LLMRerankFallback: true})
	if !us.useLLMRerankFallback {
		t.Error("RetrievalOptions{LLMRerankFallback:true} should enable it")
	}
}

// TestRerankFallbackNotTakenWhenDisabled verifies that with the fallback OFF,
// Rerank never fires an LLM call and returns the documents in their original
// (relevance) order. The searcher is built with a nil LLM client: any attempt
// to take the fallback path would dereference it and panic — so a clean return
// proves the path was skipped.
func TestRerankFallbackNotTakenWhenDisabled(t *testing.T) {
	us := NewUnifiedSearcher(nil, nil) // nil llm ⇒ a fallback LLM call would panic
	// useLLMRerankFallback defaults to false — do not enable it.

	results := []UnifiedResult{
		{ChunkID: "c1", ContentID: "doc-a", Title: "A", Excerpt: "alpha", Score: 0.9},
		{ChunkID: "c2", ContentID: "doc-b", Title: "B", Excerpt: "beta", Score: 0.8},
		{ChunkID: "c3", ContentID: "doc-a", Title: "A", Excerpt: "alpha-2", Score: 0.7},
		{ChunkID: "c4", ContentID: "doc-c", Title: "C", Excerpt: "gamma", Score: 0.6},
	}

	order, err := us.Rerank(context.Background(), "some query", results)
	if err != nil {
		t.Fatalf("Rerank with fallback disabled should not error; got %v", err)
	}

	// Original first-seen content-id order, deduplicated.
	want := []string{"doc-a", "doc-b", "doc-c"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q (full: %v)", i, order[i], want[i], order)
		}
	}
}

// TestRerankEmptyResults confirms the trivial no-op path is unaffected.
func TestRerankEmptyResults(t *testing.T) {
	us := NewUnifiedSearcher(nil, nil)
	order, err := us.Rerank(context.Background(), "q", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 0 {
		t.Errorf("expected empty order, got %v", order)
	}
}
