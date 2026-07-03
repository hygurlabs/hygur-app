package tools

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/identity"
	"github.com/hygur/sidecar/internal/store"
)

// mkNISS builds a checksum-VALID Belgian national number from a 9-digit base
// (fictional). Mirrors the helper in the recognize package tests.
func mkNISS(base9 string) string {
	b, _ := strconv.ParseInt(base9, 10, 64)
	return base9 + fmt.Sprintf("%02d", 97-(b%97))
}

func TestSignContent_CollapsesVariants(t *testing.T) {
	a := SignContent("User's name is Denis")
	b := SignContent("  user's   name is   denis. ")
	c := SignContent("User’s name is Dénis")
	if a != b || a != c {
		t.Fatalf("signatures differ: %q / %q / %q", a, b, c)
	}
	if a != "user s name is denis" {
		t.Fatalf("unexpected signature: %q", a)
	}
	if SignContent("User's name is Denis") == SignContent("User's name is Marie") {
		t.Fatal("distinct facts collapsed to same signature")
	}
}

func TestIsTypedIdentifierAssertion(t *testing.T) {
	niss := mkNISS("850701123") // fictional, checksum-valid
	iban := "GB82WEST12345698765432"

	drop := []string{
		"Le numéro national de Denis est " + niss,      // recognize (checksum)
		"Son NN: " + niss,                              // recognize (checksum)
		"IBAN du compte pro: " + iban,                  // recognize (IBAN)
		"Numéro national de Marie: le 00 00 00 000 00", // label + 11-digit value (checksum-invalid)
		"Numéro de TVA de la société: BE 9999.999.999", // label + value
	}
	for _, c := range drop {
		if !IsTypedIdentifierAssertion(c) {
			t.Errorf("expected identifier assertion (drop): %q", RedactContent(c))
		}
	}

	keep := []string{
		"Travaille avec la Fiduciaire de la Cense",    // soft fact: accounting firm
		"Utilise Falco pour la gestion",               // soft fact: tool
		"Déclaration TVA Q1: montant 4500 EUR",        // label word but only a 4-digit amount
		"Préfère les réponses concises",               // preference
		"Rendez-vous le 2026-04-30 avec le comptable", // a date, not an identifier
	}
	for _, c := range keep {
		if IsTypedIdentifierAssertion(c) {
			t.Errorf("soft fact wrongly flagged as identifier: %q", RedactContent(c))
		}
	}
}

// TestIsOwnerIdentityAssertion — fictional identity, no real PII. A pure
// self-identity assertion ("User is Jordan Vance") is dropped; a soft fact that
// merely mentions the owner ("User works with…", "User uses…") is never touched,
// and a bare surname/given name never matches (the strict matcher already guards
// that — it must never capture a family member).
func TestIsOwnerIdentityAssertion(t *testing.T) {
	owner := identity.NewMatcher([]string{"Jordan Vance", "Jordan V"})

	drop := []string{
		"User is Jordan Vance",
		"User is Jordan Vance.",
		"User's name is Jordan Vance",
		"The user's name is Jordan Vance",
		"My name is Jordan Vance",
		"I am Jordan Vance",
	}
	for _, c := range drop {
		if !IsOwnerIdentityAssertion(c, owner) {
			t.Errorf("expected owner-identity assertion (drop): %q", RedactContent(c))
		}
	}

	keep := []string{
		"User works with Fiduciaire de la Cense", // soft fact: merely mentions the owner
		"User uses Falco pour la gestion",        // soft fact: tool
		"User is Jordan Vance's accountant",      // an appended clause, not a pure identity assertion
		"Jordan Vance travaille avec Falco",      // doesn't open with a self-identity prefix
		"User is Vance",                          // bare surname — never sufficient (family guard)
		"User is Jordan",                         // bare given name — never sufficient (family guard)
		"User's name is Jordan",                  // bare given name after the prefix — same guard
	}
	for _, c := range keep {
		if IsOwnerIdentityAssertion(c, owner) {
			t.Errorf("soft/non-identity fact wrongly flagged as owner-identity: %q", RedactContent(c))
		}
	}

	// Nil owner disables the rule entirely (e.g. a caller with no owner config).
	if IsOwnerIdentityAssertion("User is Jordan Vance", nil) {
		t.Error("nil owner should never match")
	}
}

func TestPlanReconcile_DedupAcceptedWins(t *testing.T) {
	now := time.Now()
	accepted := now
	mems := []store.Memory{
		// Two identical facts: one pending (older), one accepted (newer).
		{MemoryID: "a", Type: store.MemoryFact, Content: "User's name is Denis", CreatedAt: now.Add(-2 * time.Hour)},
		{MemoryID: "b", Type: store.MemoryFact, Content: "user's name is denis.", CreatedAt: now.Add(-1 * time.Hour), AcceptedAt: &accepted},
		// A unique soft fact — must be kept.
		{MemoryID: "c", Type: store.MemoryFact, Content: "Travaille avec la Fiduciaire de la Cense", CreatedAt: now},
		// A typed identifier — must be dropped (deferred to the graph).
		{MemoryID: "d", Type: store.MemoryFact, Content: "Numéro national: " + mkNISS("850701123"), CreatedAt: now},
	}
	plan := PlanReconcile(mems, nil)

	if plan.DuplicateCount() != 1 {
		t.Fatalf("want 1 duplicate removed, got %d", plan.DuplicateCount())
	}
	if plan.IdentifierCount() != 1 {
		t.Fatalf("want 1 identifier removed, got %d", plan.IdentifierCount())
	}
	if len(plan.Kept) != 2 {
		t.Fatalf("want 2 kept (accepted survivor + soft fact), got %d", len(plan.Kept))
	}
	// The surviving duplicate must be the accepted one ("b").
	var survivedName bool
	for _, k := range plan.Kept {
		if SignContent(k.Content) == SignContent("User's name is Denis") {
			survivedName = true
			if k.MemoryID != "b" {
				t.Fatalf("accepted row should win, survivor is %q", k.MemoryID)
			}
		}
	}
	if !survivedName {
		t.Fatal("name fact not kept at all")
	}

	// Idempotent: re-planning over the kept set removes nothing.
	plan2 := PlanReconcile(plan.Kept, nil)
	if len(plan2.Deletions) != 0 {
		t.Fatalf("reconcile not idempotent: %d deletions on second pass", len(plan2.Deletions))
	}
}

// TestPlanReconcile_OwnerIdentity — Lot 3's exact shape: a memory set with two
// pure owner-identity rows and several unrelated soft facts. With the owner
// matcher wired, only the two identity rows are removed (6→4); every soft fact
// — including one that merely mentions the owner — is kept. Fictional identity.
func TestPlanReconcile_OwnerIdentity(t *testing.T) {
	owner := identity.NewMatcher([]string{"Jordan Vance"})
	now := time.Now()
	mems := []store.Memory{
		{MemoryID: "1", Type: store.MemoryFact, Content: "User is Jordan Vance", CreatedAt: now},
		{MemoryID: "2", Type: store.MemoryFact, Content: "User's name is Jordan Vance", CreatedAt: now},
		{MemoryID: "3", Type: store.MemoryFact, Content: "VAT declaration Q4 filed", CreatedAt: now},
		{MemoryID: "4", Type: store.MemoryFact, Content: "Q4 turnover and purchases reported", CreatedAt: now},
		{MemoryID: "5", Type: store.MemoryFact, Content: "Works with Fiduciaire de la Cense", CreatedAt: now},
		{MemoryID: "6", Type: store.MemoryFact, Content: "Uses Falco for accounting", CreatedAt: now},
	}
	plan := PlanReconcile(mems, owner)

	if got := plan.OwnerIdentityCount(); got != 2 {
		t.Fatalf("want 2 owner-identity rows removed, got %d: %+v", got, plan.Deletions)
	}
	if len(plan.Deletions) != 2 {
		t.Fatalf("want exactly 2 deletions total (no duplicate/identifier noise), got %d: %+v", len(plan.Deletions), plan.Deletions)
	}
	if len(plan.Kept) != 4 {
		t.Fatalf("want 4 soft facts kept, got %d: %+v", len(plan.Kept), plan.Kept)
	}
	keptIDs := map[string]bool{}
	for _, k := range plan.Kept {
		keptIDs[k.MemoryID] = true
	}
	for _, want := range []string{"3", "4", "5", "6"} {
		if !keptIDs[want] {
			t.Errorf("soft fact %q should be kept, got kept=%v", want, keptIDs)
		}
	}
}

func TestPersistExtracted_DedupAndIdentifierDefer(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close()
	tool := &MemoryStoreTool{store: db}

	// Pre-existing accepted fact so the extracted duplicate has something to hit.
	seedAccepted := time.Now()
	if err := db.InsertMemory(&store.Memory{
		MemoryID:   "seed",
		Type:       store.MemoryFact,
		Content:    "User's name is Denis",
		Source:     store.MemorySourceManual,
		CreatedAt:  time.Now(),
		AcceptedAt: &seedAccepted,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	in := []ExtractedMemory{
		{Type: "fact", Content: "user's name is denis"},                                   // dup of seed → skip
		{Type: "fact", Content: "Le numéro national de Denis est " + mkNISS("850701123")}, // identifier → drop
		{Type: "fact", Content: "Travaille avec la Fiduciaire de la Cense"},               // soft fact → keep
		{Type: "fact", Content: "Travaille avec la Fiduciaire de la Cense"},               // in-batch dup → skip
	}
	stored, err := tool.PersistExtracted(in, "session-1")
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if stored != 1 {
		t.Fatalf("want exactly 1 stored (the soft fact), got %d", stored)
	}

	all, err := db.ListMemoriesAfter(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// seed + 1 soft fact = 2 total; no duplicate name, no identifier row.
	if len(all) != 2 {
		t.Fatalf("want 2 total rows, got %d", len(all))
	}
	for _, m := range all {
		if IsTypedIdentifierAssertion(m.Content) {
			t.Fatalf("identifier row leaked into store: %q", RedactContent(m.Content))
		}
	}
}

// TestPersistExtracted_DefersOwnerIdentity — Lot 2's write-time gate. A candidate
// that merely re-asserts the owner's own name/identity is dropped BEFORE insert;
// a soft fact that mentions the owner in passing is stored unchanged. Fictional
// identity.
func TestPersistExtracted_DefersOwnerIdentity(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close()
	owner := identity.NewMatcher([]string{"Jordan Vance"})
	tool := &MemoryStoreTool{store: db, owner: owner}

	in := []ExtractedMemory{
		{Type: "fact", Content: "User is Jordan Vance"},                   // pure owner-identity → drop
		{Type: "fact", Content: "User's name is Jordan Vance"},            // pure owner-identity → drop
		{Type: "fact", Content: "User works with Fiduciaire de la Cense"}, // soft fact → keep
		{Type: "fact", Content: "User uses Falco pour la gestion"},        // soft fact → keep
	}
	stored, err := tool.PersistExtracted(in, "session-1")
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if stored != 2 {
		t.Fatalf("want exactly 2 stored (the two soft facts), got %d", stored)
	}

	all, err := db.ListMemoriesAfter(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, m := range all {
		if IsOwnerIdentityAssertion(m.Content, owner) {
			t.Fatalf("owner-identity row leaked into store: %q", RedactContent(m.Content))
		}
	}
}
