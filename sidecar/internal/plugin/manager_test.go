package plugin

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Mock connector used throughout all manager tests.
// ---------------------------------------------------------------------------

type mockConnector struct {
	id          string
	initCalled  atomic.Bool
	startCalled atomic.Bool
	stopCalled  atomic.Bool
	initErr     error
	startErr    error
	stopErr     error
	health      HealthStatus
}

func newMock(id string) *mockConnector {
	return &mockConnector{
		id:     id,
		health: HealthStatus{Status: StatusHealthy},
	}
}

func (m *mockConnector) Info() ConnectorInfo        { return ConnectorInfo{ID: m.id, Name: m.id} }
func (m *mockConnector) Capabilities() Capabilities { return Capabilities{} }
func (m *mockConnector) ConfigSchema() ConfigSchema { return ConfigSchema{} }
func (m *mockConnector) Health() HealthStatus       { return m.health }
func (m *mockConnector) Init(_ context.Context, _ ConnectorConfig) error {
	m.initCalled.Store(true)
	return m.initErr
}
func (m *mockConnector) Start(_ context.Context) error {
	m.startCalled.Store(true)
	return m.startErr
}
func (m *mockConnector) Stop(_ context.Context) error {
	m.stopCalled.Store(true)
	return m.stopErr
}

// mockSyncer implements Connector + Syncer.
type mockSyncer struct {
	mockConnector
	syncCalled atomic.Bool
	syncErr    error
	syncResult *SyncResult
}

func newMockSyncer(id string) *mockSyncer {
	return &mockSyncer{
		mockConnector: mockConnector{
			id:     id,
			health: HealthStatus{Status: StatusHealthy},
		},
		syncResult: &SyncResult{Processed: 1},
	}
}

func (m *mockSyncer) Sync(_ context.Context, _ SyncOptions) (*SyncResult, error) {
	m.syncCalled.Store(true)
	return m.syncResult, m.syncErr
}

// ---------------------------------------------------------------------------
// Helper: creates a no-op logger.
// ---------------------------------------------------------------------------

func nopLogger() zerolog.Logger {
	return zerolog.Nop()
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestManager_Register_Success(t *testing.T) {
	m := NewManager(nil, nopLogger())
	conn := newMock("test.connector")

	err := m.Register(conn)
	require.NoError(t, err)

	got, ok := m.Get("test.connector")
	assert.True(t, ok)
	assert.Equal(t, conn, got)
}

func TestManager_Register_Duplicate_ReturnsError(t *testing.T) {
	m := NewManager(nil, nopLogger())
	conn := newMock("test.connector")

	require.NoError(t, m.Register(conn))
	err := m.Register(conn)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestManager_Register_AfterStart_HotStart(t *testing.T) {
	m := NewManager(nil, nopLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	// Register a connector that is enabled AFTER Start().
	// Pre-configure it so hot-start fires.
	conn := newMock("hot.connector")
	// Inject config before Register so the connector is marked enabled.
	m.mu.Lock()
	m.configs["hot.connector"] = ConnectorConfig{Enabled: true}
	m.mu.Unlock()

	err := m.Register(conn)
	require.NoError(t, err)

	// Give the hot-start goroutine (which runs inline) a moment if needed.
	// initAndStart is called synchronously from Register after mu.Unlock.
	assert.True(t, conn.initCalled.Load(), "Init should have been called on hot-start")
	assert.True(t, conn.startCalled.Load(), "Start should have been called on hot-start")
}

func TestManager_Register_AfterStart_NotEnabled_NoHotStart(t *testing.T) {
	m := NewManager(nil, nopLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	conn := newMock("disabled.connector")
	// Default config is Enabled: false → no hot-start.
	err := m.Register(conn)
	require.NoError(t, err)

	assert.False(t, conn.initCalled.Load())
	assert.False(t, conn.startCalled.Load())
}

func TestManager_Configure_UpdatesConfig(t *testing.T) {
	m := NewManager(nil, nopLogger())
	conn := newMock("cfg.connector")
	require.NoError(t, m.Register(conn))

	newCfg := ConnectorConfig{
		Enabled:  true,
		Settings: map[string]string{"key": "value"},
		Schedule: "@daily",
	}
	err := m.Configure("cfg.connector", newCfg)
	require.NoError(t, err)

	got, ok := m.GetConfig("cfg.connector")
	assert.True(t, ok)
	assert.Equal(t, newCfg, got)
}

func TestManager_Configure_UnknownConnector_ReturnsError(t *testing.T) {
	m := NewManager(nil, nopLogger())
	err := m.Configure("nonexistent", ConnectorConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestManager_EnableConnector_CallsInitAndStart(t *testing.T) {
	m := NewManager(nil, nopLogger())
	conn := newMock("enable.me")
	require.NoError(t, m.Register(conn))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, m.Start(ctx))

	err := m.EnableConnector("enable.me")
	require.NoError(t, err)

	assert.True(t, conn.initCalled.Load(), "Init must be called on Enable")
	assert.True(t, conn.startCalled.Load(), "Start must be called on Enable")

	cfg, ok := m.GetConfig("enable.me")
	assert.True(t, ok)
	assert.True(t, cfg.Enabled)
}

func TestManager_EnableConnector_BeforeStart_ReturnsError(t *testing.T) {
	m := NewManager(nil, nopLogger())
	conn := newMock("enable.me")
	require.NoError(t, m.Register(conn))

	err := m.EnableConnector("enable.me")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not started")
}

func TestManager_EnableConnector_TriggersAutoSync(t *testing.T) {
	m := NewManager(nil, nopLogger())
	conn := newMockSyncer("autosync.enable")
	require.NoError(t, m.Register(conn))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, m.Start(ctx))

	require.NoError(t, m.EnableConnector("autosync.enable"))

	require.Eventually(t, func() bool {
		return conn.syncCalled.Load()
	}, time.Second, 10*time.Millisecond, "Sync must be called shortly after Enable")
}

func TestManager_Configure_TriggersAutoSyncWhenEnabled(t *testing.T) {
	m := NewManager(nil, nopLogger())
	conn := newMockSyncer("autosync.configure")
	require.NoError(t, m.Register(conn))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, m.Start(ctx))

	err := m.Configure("autosync.configure", ConnectorConfig{
		Enabled:  true,
		Settings: map[string]string{"path": "/tmp"},
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return conn.syncCalled.Load()
	}, time.Second, 10*time.Millisecond, "Sync must be called shortly after Configure save")
}

func TestManager_Configure_DoesNotTriggerAutoSyncWhenDisabled(t *testing.T) {
	m := NewManager(nil, nopLogger())
	conn := newMockSyncer("autosync.disabled")
	require.NoError(t, m.Register(conn))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, m.Start(ctx))

	err := m.Configure("autosync.disabled", ConnectorConfig{Enabled: false})
	require.NoError(t, err)

	// Give any stray goroutine a chance to fire — it should not.
	time.Sleep(50 * time.Millisecond)
	assert.False(t, conn.syncCalled.Load(), "Sync must not run when config is disabled")
}

func TestManager_EnableConnector_NonSyncer_NoAutoSync(t *testing.T) {
	m := NewManager(nil, nopLogger())
	conn := newMock("autosync.nosync")
	require.NoError(t, m.Register(conn))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, m.Start(ctx))

	require.NoError(t, m.EnableConnector("autosync.nosync"))

	// Connector doesn't implement Syncer; the helper must short-circuit
	// without panicking and without scheduling anything.
	time.Sleep(50 * time.Millisecond)
	assert.True(t, conn.startCalled.Load())
}

func TestManager_DisableConnector_CallsStopAndRemovesSchedule(t *testing.T) {
	m := NewManager(nil, nopLogger())
	conn := newMock("disable.me")
	require.NoError(t, m.Register(conn))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, m.Start(ctx))

	// Enable first so it's running.
	require.NoError(t, m.EnableConnector("disable.me"))
	assert.True(t, conn.startCalled.Load())

	// Now disable.
	err := m.DisableConnector("disable.me")
	require.NoError(t, err)

	assert.True(t, conn.stopCalled.Load(), "Stop must be called on Disable")

	cfg, ok := m.GetConfig("disable.me")
	assert.True(t, ok)
	assert.False(t, cfg.Enabled)
}

func TestManager_DisableConnector_UnknownConnector_ReturnsError(t *testing.T) {
	m := NewManager(nil, nopLogger())
	err := m.DisableConnector("ghost")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestManager_TriggerSync_CallsSyncOnSyncer(t *testing.T) {
	m := NewManager(nil, nopLogger())
	conn := newMockSyncer("sync.me")
	require.NoError(t, m.Register(conn))

	ctx := context.Background()
	result, err := m.TriggerSync(ctx, "sync.me", SyncOptions{})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.Processed)
	assert.True(t, conn.syncCalled.Load())
}

func TestManager_TriggerSync_NonSyncer_ReturnsError(t *testing.T) {
	m := NewManager(nil, nopLogger())
	conn := newMock("nosync.connector")
	require.NoError(t, m.Register(conn))

	_, err := m.TriggerSync(context.Background(), "nosync.connector", SyncOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support sync")
}

func TestManager_TriggerSync_UnknownConnector_ReturnsError(t *testing.T) {
	m := NewManager(nil, nopLogger())
	_, err := m.TriggerSync(context.Background(), "ghost", SyncOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestManager_TriggerSync_Concurrent_SecondCallReturnsErrSyncInProgress(t *testing.T) {
	m := NewManager(nil, nopLogger())

	slowSyncer := &slowMockSyncer{
		mockSyncer: *newMockSyncer("slow.sync"),
		delay:      200 * time.Millisecond,
	}
	require.NoError(t, m.Register(slowSyncer))

	ctx := context.Background()
	errCh := make(chan error, 2)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, err := m.TriggerSync(ctx, "slow.sync", SyncOptions{})
		errCh <- err
	}()

	// Give the first goroutine a moment to acquire the lock.
	time.Sleep(20 * time.Millisecond)

	go func() {
		defer wg.Done()
		_, err := m.TriggerSync(ctx, "slow.sync", SyncOptions{})
		errCh <- err
	}()

	wg.Wait()
	close(errCh)

	var errs []error
	for e := range errCh {
		errs = append(errs, e)
	}

	require.Len(t, errs, 2)
	inProgressCount := 0
	for _, e := range errs {
		if errors.Is(e, ErrSyncInProgress) {
			inProgressCount++
		}
	}
	assert.Equal(t, 1, inProgressCount, "exactly one call should return ErrSyncInProgress")
}

// slowMockSyncer blocks for a duration before returning.
type slowMockSyncer struct {
	mockSyncer
	delay time.Duration
}

func (s *slowMockSyncer) Sync(ctx context.Context, opts SyncOptions) (*SyncResult, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
	}
	return s.mockSyncer.Sync(ctx, opts)
}

func TestManager_AllHealth_ReturnsMapOfStatuses(t *testing.T) {
	m := NewManager(nil, nopLogger())

	c1 := newMock("conn.a")
	c1.health = HealthStatus{Status: StatusHealthy}
	c2 := newMock("conn.b")
	c2.health = HealthStatus{Status: StatusUnhealthy, Message: "down"}

	require.NoError(t, m.Register(c1))
	require.NoError(t, m.Register(c2))

	health := m.AllHealth()
	assert.Len(t, health, 2)
	assert.Equal(t, StatusHealthy, health["conn.a"].Status)
	assert.Equal(t, StatusUnhealthy, health["conn.b"].Status)
}

func TestManager_ListInfos_ReturnsAllInfos(t *testing.T) {
	m := NewManager(nil, nopLogger())
	require.NoError(t, m.Register(newMock("a")))
	require.NoError(t, m.Register(newMock("b")))

	infos := m.ListInfos()
	assert.Len(t, infos, 2)
}

// ---------------------------------------------------------------------------
// Integration wiring test: Register → Start → Scheduler fires → Sync called.
// ---------------------------------------------------------------------------

func TestManager_SchedulerWiring_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	m := NewManager(nil, nopLogger())
	conn := newMockSyncer("sched.connector")

	// Pre-configure as enabled so Start() fires it.
	m.mu.Lock()
	m.configs["sched.connector"] = ConnectorConfig{Enabled: true}
	m.mu.Unlock()

	require.NoError(t, m.Register(conn))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	// Add a schedule that fires every second.
	err := m.scheduler.Add("sched.connector", "@every 1s")
	require.NoError(t, err)

	// Wait up to 2s for the scheduler to trigger at least one sync.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn.syncCalled.Load() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	assert.True(t, conn.syncCalled.Load(), "scheduler should have triggered Sync() within 2s")
}
