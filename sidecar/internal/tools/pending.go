package tools

import (
	"encoding/json"
	"sync"
	"time"
)

// PendingActionTTL is how long a pending side-effect action waits for the user's
// confirmation before it expires. In-process only — no DB row (WP3 keeps the
// audit log for WP4). A generous window: the user reads a preview card, thinks,
// then confirms.
const PendingActionTTL = 10 * time.Minute

// PendingResult is the payload the registry returns to the LLM (as the tool
// result) when a SideEffect tool is gated: the model learns the action was NOT
// executed and is now awaiting the user's confirmation. Mirrored to the client
// as the SSE `pending_action` event so a Confirm/Cancel card can render.
type PendingResult struct {
	Pending  bool   `json:"pending"`
	ActionID string `json:"action_id"`
	Preview  string `json:"preview"`
}

// PendingAction is one gated side-effect awaiting human confirmation. It holds
// the exact tool + args so POST /actions/{action_id}/confirm can execute them
// verbatim after the user approves.
type PendingAction struct {
	ActionID  string
	ToolName  string
	Args      json.RawMessage
	Preview   string
	CreatedAt time.Time
}

// PendingActionStore is the in-process registry of gated side-effect actions.
// Fail-closed by construction: an action is executed ONLY when a matching entry
// exists and is taken (removed) here — a missing/expired entry can never run.
// Safe for concurrent use.
type PendingActionStore struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]PendingAction
	now func() time.Time // injectable clock for tests
}

// NewPendingActionStore returns a store with the given TTL (use PendingActionTTL).
func NewPendingActionStore(ttl time.Duration) *PendingActionStore {
	return &PendingActionStore{
		ttl: ttl,
		m:   make(map[string]PendingAction),
		now: time.Now,
	}
}

// Add records a pending action, evicting any already-expired entries first so
// the map can't grow unbounded on an abandoned session.
func (s *PendingActionStore) Add(a PendingAction) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictExpiredLocked()
	s.m[a.ActionID] = a
}

// Take removes and returns the action for id, but ONLY if it exists and has not
// expired. The second return is false for unknown or expired ids — the confirm
// handler then refuses to execute (fail-closed). Take always removes the entry
// so a confirmation can never be replayed.
func (s *PendingActionStore) Take(id string) (PendingAction, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.m[id]
	if !ok {
		return PendingAction{}, false
	}
	delete(s.m, id)
	if s.now().Sub(a.CreatedAt) > s.ttl {
		return PendingAction{}, false
	}
	return a, true
}

func (s *PendingActionStore) evictExpiredLocked() {
	cutoff := s.now().Add(-s.ttl)
	for id, a := range s.m {
		if a.CreatedAt.Before(cutoff) {
			delete(s.m, id)
		}
	}
}
