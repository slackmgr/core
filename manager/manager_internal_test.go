package manager

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/eko/gocache/lib/v4/store"
	gocache_store "github.com/eko/gocache/store/go_cache/v4"
	gocache "github.com/patrickmn/go-cache"
	"github.com/slackmgr/core/config"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubDB is a minimal stub implementing types.DB for nil-guard tests.
type stubDB struct{}

func (s *stubDB) Init(_ context.Context, _ bool) error                          { return nil }
func (s *stubDB) SaveAlert(_ context.Context, _ *types.Alert) error             { return nil }
func (s *stubDB) SaveIssue(_ context.Context, _ types.Issue) error              { return nil }
func (s *stubDB) SaveIssues(_ context.Context, _ ...types.Issue) error          { return nil }
func (s *stubDB) MoveIssue(_ context.Context, _ types.Issue, _, _ string) error { return nil }
func (s *stubDB) FindOpenIssueByCorrelationID(_ context.Context, _, _ string) (string, json.RawMessage, error) {
	return "", nil, nil
}

func (s *stubDB) FindIssueBySlackPostID(_ context.Context, _, _ string) (string, json.RawMessage, error) {
	return "", nil, nil
}
func (s *stubDB) FindActiveChannels(_ context.Context) ([]string, error) { return nil, nil }
func (s *stubDB) LoadOpenIssuesInChannel(_ context.Context, _ string) (map[string]json.RawMessage, error) {
	return nil, nil
}
func (s *stubDB) SaveMoveMapping(_ context.Context, _ types.MoveMapping) error { return nil }
func (s *stubDB) FindMoveMapping(_ context.Context, _, _ string) (json.RawMessage, error) {
	return nil, nil
}
func (s *stubDB) DeleteMoveMapping(_ context.Context, _, _ string) error { return nil }
func (s *stubDB) SaveChannelProcessingState(_ context.Context, _ *types.ChannelProcessingState) error {
	return nil
}

func (s *stubDB) FindChannelProcessingState(_ context.Context, _ string) (*types.ChannelProcessingState, error) {
	return nil, nil
}
func (s *stubDB) DropAllData(_ context.Context) error { return nil }

// stubFifoQueue is a minimal stub implementing FifoQueue for nil-guard tests.
type stubFifoQueue struct{ name string }

func (s *stubFifoQueue) Name() string { return s.name }
func (s *stubFifoQueue) Send(_ context.Context, _, _, _ string) error {
	return nil
}

func (s *stubFifoQueue) Receive(_ context.Context, _ chan<- *types.FifoQueueItem) error {
	return nil
}

func TestManager_Run_NilGuards(t *testing.T) {
	t.Parallel()

	// SkipDatabaseCache must be true to prevent New() from wrapping a nil DB
	// in the cache middleware, which would make m.db non-nil in Run().
	cfg := &config.ManagerConfig{SkipDatabaseCache: true}

	tests := []struct {
		name         string
		db           types.DB
		alertQueue   FifoQueue
		commandQueue FifoQueue
		locker       ChannelLocker
		expectError  string
	}{
		{
			name:         "nil database",
			db:           nil,
			alertQueue:   &stubFifoQueue{name: "alerts"},
			commandQueue: &stubFifoQueue{name: "commands"},
			locker:       &NoopChannelLocker{},
			expectError:  "database cannot be nil",
		},
		{
			name:         "nil alert queue",
			db:           &stubDB{},
			alertQueue:   nil,
			commandQueue: &stubFifoQueue{name: "commands"},
			locker:       &NoopChannelLocker{},
			expectError:  "alert queue cannot be nil",
		},
		{
			name:         "nil command queue",
			db:           &stubDB{},
			alertQueue:   &stubFifoQueue{name: "alerts"},
			commandQueue: nil,
			locker:       &NoopChannelLocker{},
			expectError:  "command queue cannot be nil",
		},
		{
			name:         "nil channel locker",
			db:           &stubDB{},
			alertQueue:   &stubFifoQueue{name: "alerts"},
			commandQueue: &stubFifoQueue{name: "commands"},
			locker:       nil,
			expectError:  "channel locker cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := New(tt.db, tt.alertQueue, tt.commandQueue, nil, tt.locker, &mockLogger{}, nil, cfg, nil)

			err := m.Run(context.Background())

			require.Error(t, err)
			assert.Equal(t, tt.expectError, err.Error())
		})
	}
}

// stubCacheStore is a minimal stub implementing store.StoreInterface for validation tests.
// It is not a known distributed store, so validateCacheStoreIsDistributed must reject it.
type stubCacheStore struct{}

func (s *stubCacheStore) Get(_ context.Context, _ any) (any, error) { return nil, nil }
func (s *stubCacheStore) GetWithTTL(_ context.Context, _ any) (any, time.Duration, error) {
	return nil, 0, nil
}
func (s *stubCacheStore) Set(_ context.Context, _ any, _ any, _ ...store.Option) error { return nil }
func (s *stubCacheStore) Delete(_ context.Context, _ any) error                        { return nil }
func (s *stubCacheStore) Invalidate(_ context.Context, _ ...store.InvalidateOption) error {
	return nil
}
func (s *stubCacheStore) Clear(_ context.Context) error { return nil }
func (s *stubCacheStore) GetType() string               { return "stub" }

func TestValidateCacheStoreIsDistributed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		store       store.StoreInterface
		wantErr     bool
		errContains string
	}{
		{
			name:        "nil_store_returns_error",
			store:       nil,
			wantErr:     true,
			errContains: "must not be nil",
		},
		{
			name:        "go_cache_store_returns_error",
			store:       gocache_store.NewGoCache(gocache.New(5*time.Minute, time.Minute)),
			wantErr:     true,
			errContains: "GoCacheStore",
		},
		{
			name:        "unknown_stub_store_returns_error",
			store:       &stubCacheStore{},
			wantErr:     true,
			errContains: "is not a known distributed store",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateCacheStoreIsDistributed(tt.store)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestManager_Run_RejectInMemoryCacheStore(t *testing.T) {
	t.Parallel()

	// When SkipDatabaseCache=false (default), passing a go-cache (in-memory) store must
	// cause Run() to return an error immediately, preventing silent misrouting in
	// multi-instance deployments.
	cfg := config.NewDefaultManagerConfig()
	cfg.SkipDatabaseCache = false
	cfg.SlackClient.AppToken = "xapp-test"
	cfg.SlackClient.BotToken = "xoxb-test"

	gocacheClient := gocache.New(5*time.Minute, time.Minute)
	inMemoryStore := gocache_store.NewGoCache(gocacheClient)

	m := New(
		&stubDB{},
		&stubFifoQueue{name: "alerts"},
		&stubFifoQueue{name: "commands"},
		inMemoryStore,
		&NoopChannelLocker{},
		&mockLogger{},
		nil,
		cfg,
		nil,
	)

	err := m.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cache store for multi-instance deployment")
}
