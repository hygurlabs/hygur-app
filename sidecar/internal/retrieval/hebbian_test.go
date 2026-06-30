package retrieval

import "testing"

// Regression guard: the Phase D Hebbian expansion must be OFF unless explicitly
// enabled — an empty config leaves retrieval byte-identical to before.
func TestHebbianExpansionDefaultOff(t *testing.T) {
	us := NewUnifiedSearcher(nil, nil)
	if us.useHebbianExpansion {
		t.Fatal("Hebbian expansion must be OFF by default")
	}
	us.SetRetrievalOptions(RetrievalOptions{}) // empty opts → still OFF
	if us.useHebbianExpansion {
		t.Error("empty RetrievalOptions must leave Hebbian expansion OFF")
	}
	us.SetHebbianExpansion(true)
	if !us.useHebbianExpansion {
		t.Error("SetHebbianExpansion(true) should enable it")
	}
	us.SetRetrievalOptions(RetrievalOptions{HebbianExpansion: true})
	if !us.useHebbianExpansion {
		t.Error("RetrievalOptions{HebbianExpansion:true} should enable it")
	}
}
