package plugin

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestScheduler creates a Scheduler with a nil Manager (sufficient for
// expression-validation tests; sync-trigger tests are covered in manager_test.go).
func newTestScheduler() *Scheduler {
	return NewScheduler(nil, zerolog.Nop())
}

func TestScheduler_Add_ValidExpression_NoError(t *testing.T) {
	tests := []struct {
		name string
		expr string
	}{
		{"5-field cron", "0 */6 * * *"},
		{"@daily shorthand", "@daily"},
		{"@every 1h", "@every 1h"},
		{"@hourly shorthand", "@hourly"},
		{"@midnight shorthand", "@midnight"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestScheduler()
			err := s.Add("connector.test", tc.expr)
			assert.NoError(t, err)
		})
	}
}

func TestScheduler_Add_InvalidExpression_ReturnsError(t *testing.T) {
	tests := []struct {
		name string
		expr string
	}{
		{"empty string", ""},
		{"too few fields", "* * *"},
		{"too many fields", "* * * * * * *"},
		{"invalid range", "99 * * * *"},
		{"garbage", "not-a-cron-expression"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestScheduler()
			err := s.Add("connector.test", tc.expr)
			require.Error(t, err, "expected error for invalid expression %q", tc.expr)
			assert.Contains(t, err.Error(), "invalid cron expression")
		})
	}
}

func TestScheduler_Remove_ExistingEntry_NoError(t *testing.T) {
	s := newTestScheduler()
	require.NoError(t, s.Add("conn.a", "@every 1h"))

	// Must not panic or error.
	s.Remove("conn.a")

	// Entry should be gone from the map.
	s.mu.Lock()
	_, exists := s.entries["conn.a"]
	s.mu.Unlock()
	assert.False(t, exists)
}

func TestScheduler_Remove_NonExistentEntry_NoError(t *testing.T) {
	s := newTestScheduler()
	// Should not panic or error even if the entry was never added.
	assert.NotPanics(t, func() {
		s.Remove("ghost.connector")
	})
}

func TestScheduler_Add_SameID_Twice_ReplacesEntry(t *testing.T) {
	s := newTestScheduler()

	require.NoError(t, s.Add("conn.dup", "@daily"))

	s.mu.Lock()
	firstID := s.entries["conn.dup"]
	s.mu.Unlock()

	require.NoError(t, s.Add("conn.dup", "@weekly"))

	s.mu.Lock()
	secondID := s.entries["conn.dup"]
	entryCount := len(s.entries)
	s.mu.Unlock()

	// Only one entry should exist for this connector.
	assert.Equal(t, 1, entryCount, "should have exactly one entry after re-Add")
	// The entry IDs differ because cron assigns new IDs on each AddFunc call.
	assert.NotEqual(t, firstID, secondID, "entry ID should change after re-registration")
}

func TestScheduler_Add_MultipleConnectors_IndependentEntries(t *testing.T) {
	s := newTestScheduler()

	require.NoError(t, s.Add("conn.1", "@daily"))
	require.NoError(t, s.Add("conn.2", "@hourly"))

	s.mu.Lock()
	count := len(s.entries)
	s.mu.Unlock()

	assert.Equal(t, 2, count)
}
