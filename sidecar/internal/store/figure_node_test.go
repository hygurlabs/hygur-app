package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestFigureNodes_RoundTrip proves figure nodes persist as engram nodes with their context edges
// and read back by entity+label, that a re-run is idempotent (replace), and that unrelated
// entities/labels are excluded. Masked amounts, fictional entity.
func TestFigureNodes_RoundTrip(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "fig.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	mkItem(t, db, "doc-1")
	mkItem(t, db, "doc-2")

	nodes := []FigureNode{
		{ContentID: "doc-1", EntityNorm: "acme srl", Label: "vat", Value: "5100.00", Raw: "5 100,00", Unit: "EUR", Period: "2026-Q1", Direction: "payable", Prox: 1},
		{ContentID: "doc-1", EntityNorm: "acme srl", Label: "vat", Value: "1250.00", Raw: "1 250,00", Unit: "EUR", Period: "2026-Q1", Direction: "refund", Prox: 1},
		{ContentID: "doc-2", EntityNorm: "other org", Label: "vat", Value: "999.00", Raw: "999,00", Unit: "EUR", Period: "2026-Q1", Direction: "payable", Prox: 1},
	}
	if err := db.ReplaceFigureNodes(ctx, "doc-1", nodes[:2]); err != nil {
		t.Fatalf("replace doc-1: %v", err)
	}
	if err := db.ReplaceFigureNodes(ctx, "doc-2", nodes[2:]); err != nil {
		t.Fatalf("replace doc-2: %v", err)
	}

	got, err := db.FigureNodesForEntities(ctx, []string{"acme srl"}, "vat")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 nodes for acme srl, got %d", len(got))
	}
	// The composite PK keeps the two directions distinct; the other org is excluded.
	dirs := map[string]bool{}
	for _, n := range got {
		dirs[n.Direction] = true
		if n.EntityNorm != "acme srl" {
			t.Errorf("leaked entity %q", n.EntityNorm)
		}
	}
	if !dirs["payable"] || !dirs["refund"] {
		t.Errorf("expected both directions, got %+v", dirs)
	}

	// Idempotent replace: re-writing doc-1 with a single node leaves exactly one.
	if err := db.ReplaceFigureNodes(ctx, "doc-1", nodes[:1]); err != nil {
		t.Fatalf("re-replace: %v", err)
	}
	got, _ = db.FigureNodesForEntities(ctx, []string{"acme srl"}, "vat")
	if len(got) != 1 {
		t.Errorf("after idempotent replace expected 1 node, got %d", len(got))
	}

	// A different label returns nothing.
	got, _ = db.FigureNodesForEntities(ctx, []string{"acme srl"}, "revenue")
	if len(got) != 0 {
		t.Errorf("expected no nodes for a different label, got %d", len(got))
	}
}
