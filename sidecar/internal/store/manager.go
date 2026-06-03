package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// DefaultIdentityKey is the key of the primary database — the single user in
// local mode, or the account that owns a Cloud instance.
const DefaultIdentityKey = "local"

// Manager owns the per-identity SQLite handles and is the single place that
// turns an identity into a database file.
//
// Hygur's multi-user isolation model is DELIBERATELY file-per-identity (one .db
// per user) rather than row-level ownership columns. That choice is made here,
// up front, for one reason: it lets team/multi-user support (P5) ship WITHOUT a
// destructive migration of existing customer data — today's single hygur.db
// simply becomes that identity's database, and additional users get their own
// files. Co-mingling users in one DB with owner_id columns would instead force
// a risky data split later.
//
// Today there is a single identity, so the whole app runs against Default().
// Routing each request (and the background ingest/scheduler consumers) onto the
// per-identity handle returned by For() is the P5 work; this type is the seam it
// will plug into.
type Manager struct {
	basePath string
	mu       sync.Mutex
	dbs      map[string]*DB // keyed by resolved file path
}

// NewManager creates a Manager whose default database lives at basePath
// (e.g. <dataDir>/hygur.db).
func NewManager(basePath string) *Manager {
	return &Manager{basePath: basePath, dbs: make(map[string]*DB)}
}

// pathFor maps an identity key to its database file. The default/empty/"local"
// key uses basePath unchanged; every other identity gets an isolated sibling
// file under <dir(basePath)>/users/<key>.db.
func (m *Manager) pathFor(key string) string {
	if key == "" || key == DefaultIdentityKey {
		return m.basePath
	}
	return filepath.Join(filepath.Dir(m.basePath), "users", sanitizeKey(key)+".db")
}

// For returns the database for the given identity key, opening (and caching) it
// on first use. Concurrent-safe.
func (m *Manager) For(key string) (*DB, error) {
	path := m.pathFor(key)

	m.mu.Lock()
	defer m.mu.Unlock()
	if db, ok := m.dbs[path]; ok {
		return db, nil
	}
	db, err := NewDB(path)
	if err != nil {
		return nil, fmt.Errorf("store: open %q: %w", path, err)
	}
	m.dbs[path] = db
	return db, nil
}

// Default returns the primary database — the single identity used today. All
// current consumers hold the handle it returns.
func (m *Manager) Default() (*DB, error) {
	return m.For(DefaultIdentityKey)
}

// Close closes every open handle. Safe to call once at shutdown.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for path, db := range m.dbs {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(m.dbs, path)
	}
	return firstErr
}

// sanitizeKey keeps per-identity filenames safe and predictable across
// platforms — anything outside [A-Za-z0-9-_] becomes '_'.
func sanitizeKey(key string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, key)
}
