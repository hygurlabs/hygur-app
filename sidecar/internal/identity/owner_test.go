package identity

import "testing"

// Fictional owner "Alex Martin" mirrors the real config shape: two ambiguous bare
// variants (a lone given name and the whole family's surname) plus discriminative
// multi-token names and an email. The matcher must accept only the owner's own variants
// and reject family members who share a surname or a middle name.
func TestMatcher_OwnerVsFamily(t *testing.T) {
	m := NewMatcher([]string{"Alex Martin", "Alex", "Alex M", "Martin", "am@example.com"})

	owner := []string{
		"alex martin",        // exact
		"martin alex",        // reversed order
		"alex pierre martin", // with a middle name (as printed in a document)
		"alex m",             // given + surname initial
		"am example com",     // the email, normalized to a norm
	}
	for _, n := range owner {
		if !m.IsOwnerNorm(n) {
			t.Errorf("IsOwnerNorm(%q) = false, want true (an owner variant)", n)
		}
	}

	notOwner := []string{
		"pierre martin", // father: shares the middle name + surname, but NOT the given name
		"chris martin",  // sibling: same surname only
		"martin",        // bare surname (the whole family) — never sufficient
		"alex",          // bare given name — never sufficient
		"mr martin",     // salutation + surname
		"alexis martin", // a different given name that merely starts the same
	}
	for _, n := range notOwner {
		if m.IsOwnerNorm(n) {
			t.Errorf("IsOwnerNorm(%q) = true, want false (not the owner)", n)
		}
	}
}

func TestMatcher_AccentFold(t *testing.T) {
	m := NewMatcher([]string{"Gérard Léa"})
	if !m.IsOwnerNorm("gerard lea") {
		t.Error("accent-folded owner variant should match")
	}
}

func TestMatcher_Empty(t *testing.T) {
	// Only bare single-token variants → no discriminative signal → matches nobody.
	m := NewMatcher([]string{"Martin", "Alex"})
	if m.IsOwnerNorm("alex martin") {
		t.Error("bare-only config must not recognize any owner")
	}
	var nilM *Matcher
	if nilM.IsOwnerNorm("alex martin") || nilM.Tokens() != nil {
		t.Error("nil matcher must be inert")
	}
}
