package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

// TestRecallMemory_Execute checks the recall_memory tool surfaces an accepted
// memory and rejects an empty query. With a nil llm client SearchAccepted takes
// the recency fallback (no embeddings), which is enough to prove the wiring.
func TestRecallMemory_Execute(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close()

	st := &MemoryStoreTool{store: db}
	if _, err := st.Store("Accountant is Pierre Dupont at Acme Compta", "fact", "manual-1"); err != nil {
		t.Fatalf("store: %v", err)
	}

	tool := NewMemorySearchTool(db, nil)
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"accountant"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var out struct {
		Memories []struct {
			Content string `json:"content"`
			Type    string `json:"type"`
		} `json:"memories"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Memories) != 1 || out.Memories[0].Type != "fact" {
		t.Fatalf("want 1 fact memory, got %+v", out.Memories)
	}

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Error("empty query should error")
	}
}

// TestFindDecisions_Execute checks the find_decisions tool lists decisions,
// filters by query substring and by status, and reads with no arguments.
func TestFindDecisions_Execute(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	mk := func(id, statement, status string) {
		now := time.Now()
		if err := db.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
			ContentID: id, SourceType: store.SourceTypeDecision, Title: statement,
			Metadata: map[string]any{}, VersionID: "v1", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
		if err := db.UpsertDecisionAttrs(ctx, id, status, "", nil, ""); err != nil {
			t.Fatalf("attrs %s: %v", id, err)
		}
	}
	mk("decision:1", "Switch the accountant to Acme Compta", store.DecisionStanding)
	mk("decision:2", "Use vendor B for hosting", store.DecisionProposed)

	type resp struct {
		Decisions []struct {
			Statement string `json:"statement"`
			Status    string `json:"status"`
		} `json:"decisions"`
	}
	tool := NewFindDecisionsTool(db)

	// No arguments → every decision.
	raw, err := tool.Execute(ctx, nil)
	if err != nil {
		t.Fatalf("execute all: %v", err)
	}
	var all resp
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatalf("unmarshal all: %v", err)
	}
	if len(all.Decisions) != 2 {
		t.Fatalf("want 2 decisions, got %d (%+v)", len(all.Decisions), all.Decisions)
	}

	// Query substring filter.
	raw, _ = tool.Execute(ctx, json.RawMessage(`{"query":"accountant"}`))
	var filtered resp
	_ = json.Unmarshal(raw, &filtered)
	if len(filtered.Decisions) != 1 || filtered.Decisions[0].Statement != "Switch the accountant to Acme Compta" {
		t.Fatalf("query filter off: %+v", filtered.Decisions)
	}

	// Status filter.
	raw, _ = tool.Execute(ctx, json.RawMessage(`{"status":"proposed"}`))
	var prop resp
	_ = json.Unmarshal(raw, &prop)
	if len(prop.Decisions) != 1 || prop.Decisions[0].Status != "proposed" {
		t.Fatalf("status filter off: %+v", prop.Decisions)
	}
}
