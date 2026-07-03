package tools

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

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
	plan := PlanReconcile(mems)

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
	plan2 := PlanReconcile(plan.Kept)
	if len(plan2.Deletions) != 0 {
		t.Fatalf("reconcile not idempotent: %d deletions on second pass", len(plan2.Deletions))
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
