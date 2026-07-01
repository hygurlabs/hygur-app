package store

import (
	"context"
	"testing"
	"time"
)

func TestSearchByIdentifier(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()

	items := []struct{ id, text string }{
		{"a", "Composition de ménage. Numéro national: 12.34.56:789-01 pour la personne."},
		{"b", "Facture numéro 98765432109876, total 199 EUR."},
		{"c", "Aucun identifiant ici, juste de la prose sans le moindre chiffre utile."},
	}
	for _, it := range items {
		if err := db.InsertKnowledgeItem(ctx, &KnowledgeItem{
			ContentID: it.id, SourceType: SourceTypeNote, Title: it.id,
			NormalizedText: it.text, VersionID: "v1", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert %s: %v", it.id, err)
		}
	}

	// A raw-digit query matches the separator-formatted value in doc "a".
	if ids, err := db.SearchByIdentifier(ctx, "12345678901", 10); err != nil {
		t.Fatal(err)
	} else if len(ids) != 1 || ids[0] != "a" {
		t.Errorf("national number: got %v, want [a]", ids)
	}
	// The contiguous receipt number matches doc "b".
	if ids, _ := db.SearchByIdentifier(ctx, "98765432109876", 10); len(ids) != 1 || ids[0] != "b" {
		t.Errorf("receipt: got %v, want [b]", ids)
	}
	// An absent identifier returns nothing — the honest empty result.
	if ids, _ := db.SearchByIdentifier(ctx, "11111111111", 10); len(ids) != 0 {
		t.Errorf("absent: got %v, want []", ids)
	}
}
