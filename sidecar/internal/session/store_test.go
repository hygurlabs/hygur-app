package session

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestStore_GetCreatesSession(t *testing.T) {
	s := NewStore(time.Hour)
	ctx := s.Get("abc")
	if ctx == nil {
		t.Fatal("Get returned nil")
	}
	if ctx.SessionID != "abc" {
		t.Errorf("SessionID = %q, want abc", ctx.SessionID)
	}
}

func TestStore_GetReturnsSameInstance(t *testing.T) {
	s := NewStore(time.Hour)
	a := s.Get("abc")
	b := s.Get("abc")
	if a != b {
		t.Error("expected same pointer for the same session id")
	}
}

func TestStore_EmptyIDReturnsTransient(t *testing.T) {
	s := NewStore(time.Hour)
	first := s.Get("")
	second := s.Get("")
	if first == second {
		t.Error("empty id should return transient (different) contexts each time")
	}
	// And the store should not have stored either.
	snap := s.Snapshot()
	if len(snap) != 0 {
		t.Errorf("expected empty snapshot, got %d entries", len(snap))
	}
}

func TestStore_Delete(t *testing.T) {
	s := NewStore(time.Hour)
	s.Get("abc")
	s.Delete("abc")
	if len(s.Snapshot()) != 0 {
		t.Errorf("expected store to be empty after Delete")
	}
}

func TestStore_Concurrent_NoRace(t *testing.T) {
	s := NewStore(time.Hour)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "session"
			ctx := s.Get(id)
			ctx.AddEntity(Entity{Type: EntityIBAN, Value: "BE22..."})
			ctx.AppendResolvedQuery(ResolvedQuery{Question: "Q", Answer: "A"}, "topic")
			_ = ctx.GetEntities(EntityIBAN)
		}(i)
	}
	wg.Wait()
}

func TestStore_GC_EvictsExpired(t *testing.T) {
	s := NewStore(500 * time.Millisecond)
	ctxOld := s.Get("old")
	ctxOld.UpdatedAt = time.Now().Add(-time.Hour) // simulate ancient session

	gcCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartGC(gcCtx)

	// First GC tick fires at ttl/4 = 125ms — sleep enough to give it room.
	time.Sleep(200 * time.Millisecond)
	if _, ok := s.Snapshot()["old"]; ok {
		t.Errorf("expected old session to be evicted after GC tick")
	}

	// A freshly-created session must survive the next tick.
	s.Get("new")
	time.Sleep(200 * time.Millisecond)
	if _, ok := s.Snapshot()["new"]; !ok {
		t.Errorf("expected fresh session to survive (UpdatedAt within TTL)")
	}
}

func TestSessionContext_AddEntity_Deduplicates(t *testing.T) {
	c := &SessionContext{}
	c.AddEntity(Entity{Type: EntityIBAN, Value: "BE22..."})
	c.AddEntity(Entity{Type: EntityIBAN, Value: "BE22..."})
	c.AddEntity(Entity{Type: EntityIBAN, Value: "FR14..."})
	if got := len(c.GetEntities(EntityIBAN)); got != 2 {
		t.Errorf("expected 2 unique IBANs, got %d", got)
	}
}

func TestSessionContext_AppendResolvedQuery_CapsLength(t *testing.T) {
	c := &SessionContext{}
	for i := 0; i < maxResolvedQueries+5; i++ {
		c.AppendResolvedQuery(ResolvedQuery{Question: "q"}, "")
	}
	if len(c.ResolvedQueries) != maxResolvedQueries {
		t.Errorf("expected len=%d, got %d", maxResolvedQueries, len(c.ResolvedQueries))
	}
}

func TestSessionContext_LastResolvedQuery(t *testing.T) {
	c := &SessionContext{}
	if _, ok := c.LastResolvedQuery(); ok {
		t.Errorf("expected ok=false for empty context")
	}
	c.AppendResolvedQuery(ResolvedQuery{Question: "first"}, "")
	c.AppendResolvedQuery(ResolvedQuery{Question: "second"}, "")
	rq, ok := c.LastResolvedQuery()
	if !ok || rq.Question != "second" {
		t.Errorf("expected last = 'second', got %+v ok=%v", rq, ok)
	}
}
