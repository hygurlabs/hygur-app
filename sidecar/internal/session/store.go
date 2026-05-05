// Package session holds in-memory per-conversation state for the RAG pipeline.
//
// State is keyed by session_id (a UUID supplied by the client) and includes:
//
//   - Entities extracted from sources and from prior assistant answers
//     (IBANs, amounts, structured communications, dates, …) so that follow-up
//     questions like "and the IBAN?" can be answered directly without re-querying.
//   - The list of recent (question, answer, source ids) tuples for traceability
//     and for detecting topic shifts.
//   - An "active topic" — a few keywords representing the current thread.
//
// The Store is intentionally in-memory and per-process: the user does not want
// session state persisted across sidecar restarts. A background goroutine
// evicts sessions whose UpdatedAt is older than ttl.
package session

import (
	"context"
	"sync"
	"time"
)

// Entity types we track. Centralized as constants so the rest of the codebase
// can refer to them without typos.
const (
	EntityIBAN          = "iban"
	EntityAmount        = "amount"
	EntityStructuredCom = "communication"
	EntityVATNumber     = "vat_number"
	EntityDueDate       = "due_date"
)

// Entity is a single extracted fact carrying provenance (source content_id and
// when we learned it). Entities are deduplicated within an EntityList by
// (Type, Value).
type Entity struct {
	Type    string // "iban" | "amount" | "communication" | "vat_number" | "due_date"
	Value   string // canonical value (e.g. "BE68539007547034")
	Source  string // content_id where this was learned, "" if from assistant answer
	AddedAt time.Time
}

// ResolvedQuery captures one (user question, assistant answer) round, the
// sources that were used, and any entities extracted from the answer. Stored
// in a capped-length slice on the SessionContext.
type ResolvedQuery struct {
	Question  string
	Answer    string
	SourceIDs []string
	Entities  []Entity
	AskedAt   time.Time
}

// SessionContext is the per-session accumulator. All access is guarded by
// the embedded RWMutex; callers should use the helper methods.
type SessionContext struct {
	SessionID       string
	Entities        map[string][]Entity // keyed by Type
	ResolvedQueries []ResolvedQuery     // most-recent last; capped at maxResolvedQueries
	ActiveTopic     string              // free-form keywords representing the thread
	UpdatedAt       time.Time

	mu sync.RWMutex
}

const maxResolvedQueries = 20

// AddEntity inserts an entity, deduplicating on (Type, Value). Older entries
// are kept; AddedAt is preserved on first insertion.
func (c *SessionContext) AddEntity(e Entity) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Entities == nil {
		c.Entities = map[string][]Entity{}
	}
	for _, existing := range c.Entities[e.Type] {
		if existing.Value == e.Value {
			return
		}
	}
	if e.AddedAt.IsZero() {
		e.AddedAt = time.Now()
	}
	c.Entities[e.Type] = append(c.Entities[e.Type], e)
	c.UpdatedAt = time.Now()
}

// GetEntities returns a copy of the entities for the given type. Empty slice
// when none. Safe for concurrent reads.
func (c *SessionContext) GetEntities(entityType string) []Entity {
	c.mu.RLock()
	defer c.mu.RUnlock()
	src := c.Entities[entityType]
	if len(src) == 0 {
		return nil
	}
	out := make([]Entity, len(src))
	copy(out, src)
	return out
}

// AppendResolvedQuery records a completed turn. Truncates to maxResolvedQueries
// from the back. Updates ActiveTopic to the latest non-empty topic seed when
// supplied.
func (c *SessionContext) AppendResolvedQuery(rq ResolvedQuery, topicSeed string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rq.AskedAt.IsZero() {
		rq.AskedAt = time.Now()
	}
	c.ResolvedQueries = append(c.ResolvedQueries, rq)
	if len(c.ResolvedQueries) > maxResolvedQueries {
		c.ResolvedQueries = c.ResolvedQueries[len(c.ResolvedQueries)-maxResolvedQueries:]
	}
	if topicSeed != "" {
		c.ActiveTopic = topicSeed
	}
	c.UpdatedAt = time.Now()
}

// LastResolvedQuery returns the most recently recorded (question, answer) pair,
// or zero value if none.
func (c *SessionContext) LastResolvedQuery() (ResolvedQuery, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.ResolvedQueries) == 0 {
		return ResolvedQuery{}, false
	}
	return c.ResolvedQueries[len(c.ResolvedQueries)-1], true
}

// Store holds session contexts in memory keyed by session ID, with a background
// GC goroutine that evicts entries older than ttl.
type Store struct {
	ttl      time.Duration
	mu       sync.RWMutex
	sessions map[string]*SessionContext
}

// NewStore creates a session store with the given TTL. ttl=0 means
// "never evict" (testing).
func NewStore(ttl time.Duration) *Store {
	return &Store{
		ttl:      ttl,
		sessions: map[string]*SessionContext{},
	}
}

// Get returns the existing context for sessionID, creating a fresh one if
// absent. Empty sessionID returns a transient context that is NOT stored
// (useful for the no-session-id code path so callers can treat it uniformly).
func (s *Store) Get(sessionID string) *SessionContext {
	if sessionID == "" {
		return &SessionContext{}
	}
	s.mu.RLock()
	ctx, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if ok {
		return ctx
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-check after acquiring write lock.
	if ctx, ok = s.sessions[sessionID]; ok {
		return ctx
	}
	ctx = &SessionContext{SessionID: sessionID, UpdatedAt: time.Now()}
	s.sessions[sessionID] = ctx
	return ctx
}

// Delete removes a session immediately. No-op if absent.
func (s *Store) Delete(sessionID string) {
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()
}

// Snapshot returns a shallow copy of the session map for diagnostics. Safe for
// concurrent reads but the returned contexts share the same mu.
func (s *Store) Snapshot() map[string]*SessionContext {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*SessionContext, len(s.sessions))
	for k, v := range s.sessions {
		out[k] = v
	}
	return out
}

// StartGC runs a background goroutine that evicts sessions older than ttl.
// Returns when ctx is cancelled. ttl=0 disables GC.
func (s *Store) StartGC(ctx context.Context) {
	if s.ttl <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(s.ttl / 4)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.evictExpired()
			}
		}
	}()
}

func (s *Store) evictExpired() {
	cutoff := time.Now().Add(-s.ttl)
	s.mu.Lock()
	for id, ctx := range s.sessions {
		ctx.mu.RLock()
		expired := ctx.UpdatedAt.Before(cutoff)
		ctx.mu.RUnlock()
		if expired {
			delete(s.sessions, id)
		}
	}
	s.mu.Unlock()
}
