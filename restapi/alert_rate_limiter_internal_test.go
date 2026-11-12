package restapi

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/slackmgr/core/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeClock is a manually advanced clock for LocalAlertRateLimiter tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// takeOne takes count tokens of a single channel.
func takeOne(ctx context.Context, l AlertRateLimiter, channel string, count int, limit config.RateLimitConfig) (RateLimitResult, error) {
	results, err := l.TakeAll(ctx, map[string]int{channel: count}, limit)
	if err != nil {
		return RateLimitResult{}, err
	}

	return results[channel], nil
}

func newTestLocalLimiter() (*LocalAlertRateLimiter, *fakeClock) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	l := NewLocalAlertRateLimiter()
	l.now = clock.Now

	return l, clock
}

func TestLocalAlertRateLimiter_TakeAll(t *testing.T) {
	t.Parallel()

	limit := config.RateLimitConfig{AlertsPerSecond: 1, AllowedBurst: 10}

	t.Run("takes tokens within burst", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestLocalLimiter()

		res, err := takeOne(t.Context(), l, "C1", 4, limit)

		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 6, res.Remaining)
		assert.Equal(t, time.Duration(0), res.RetryAfter)
		assert.Equal(t, 4*time.Second, res.Reset)
	})

	t.Run("denies when bucket is exhausted, without taking tokens", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestLocalLimiter()

		res, err := takeOne(t.Context(), l, "C1", 8, limit)
		require.NoError(t, err)
		require.True(t, res.Allowed)

		res, err = takeOne(t.Context(), l, "C1", 5, limit)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 2, res.Remaining)
		assert.Equal(t, 3*time.Second, res.RetryAfter)

		// The denied request took no tokens.
		res, err = takeOne(t.Context(), l, "C1", 2, limit)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
	})

	t.Run("refills over time", func(t *testing.T) {
		t.Parallel()

		l, clock := newTestLocalLimiter()

		_, err := takeOne(t.Context(), l, "C1", 10, limit)
		require.NoError(t, err)

		clock.Advance(3 * time.Second)

		res, err := takeOne(t.Context(), l, "C1", 3, limit)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
	})

	t.Run("caps retry after when count exceeds burst", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestLocalLimiter()

		res, err := takeOne(t.Context(), l, "C1", 11, limit)

		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, maxRetryAfterDelay, res.RetryAfter)
	})

	t.Run("channels are independent", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestLocalLimiter()

		_, err := takeOne(t.Context(), l, "C1", 10, limit)
		require.NoError(t, err)

		res, err := takeOne(t.Context(), l, "C2", 10, limit)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
	})

	t.Run("takes the tokens of all channels", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestLocalLimiter()

		results, err := l.TakeAll(t.Context(), map[string]int{"C1": 3, "C2": 5}, limit)

		require.NoError(t, err)
		assert.Equal(t, map[string]RateLimitResult{
			"C1": {Allowed: true, Remaining: 7, Reset: 3 * time.Second},
			"C2": {Allowed: true, Remaining: 5, Reset: 5 * time.Second},
		}, results)
	})

	t.Run("takes no tokens if one channel is limited", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestLocalLimiter()

		_, err := takeOne(t.Context(), l, "C2", 8, limit)
		require.NoError(t, err)

		results, err := l.TakeAll(t.Context(), map[string]int{"C1": 4, "C2": 5}, limit)

		require.NoError(t, err)
		assert.Equal(t, map[string]RateLimitResult{
			"C1": {Allowed: false, Remaining: 10},
			"C2": {Allowed: false, Remaining: 2, RetryAfter: 3 * time.Second, Reset: 8 * time.Second},
		}, results)

		// C1 still has all its tokens, and C2 has the tokens left after the first call.
		res, err := takeOne(t.Context(), l, "C1", 10, limit)
		require.NoError(t, err)
		assert.True(t, res.Allowed)

		res, err = takeOne(t.Context(), l, "C2", 2, limit)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
	})

	t.Run("takes no tokens if one channel exceeds the burst", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestLocalLimiter()

		results, err := l.TakeAll(t.Context(), map[string]int{"C1": 4, "C2": 11}, limit)

		require.NoError(t, err)
		assert.False(t, results["C1"].Allowed)
		assert.Equal(t, time.Duration(0), results["C1"].RetryAfter)
		assert.Equal(t, 10, results["C1"].Remaining)
		assert.Equal(t, maxRetryAfterDelay, results["C2"].RetryAfter)
	})
}

func TestLocalAlertRateLimiter_Sweep(t *testing.T) {
	t.Parallel()

	limit := config.RateLimitConfig{AlertsPerSecond: 1, AllowedBurst: 120}

	l, clock := newTestLocalLimiter()

	_, err := takeOne(t.Context(), l, "C_IDLE", 1, limit)
	require.NoError(t, err)
	_, err = takeOne(t.Context(), l, "C_BUSY", 100, limit)
	require.NoError(t, err)
	require.Equal(t, 2, l.size())

	// After the sweep interval, C_IDLE is full again (1 token refilled), C_BUSY is not (100 tokens missing).
	clock.Advance(localLimiterSweepInterval)

	_, err = takeOne(t.Context(), l, "C_OTHER", 1, limit)
	require.NoError(t, err)

	l.mu.Lock()
	_, idleExists := l.limiters["C_IDLE"]
	_, busyExists := l.limiters["C_BUSY"]
	l.mu.Unlock()

	assert.False(t, idleExists, "full bucket should be removed")
	assert.True(t, busyExists, "partially drained bucket should be kept")

	// Removing C_IDLE does not change its limit: a new bucket is full, just like the removed one.
	res, err := takeOne(t.Context(), l, "C_IDLE", 120, limit)
	require.NoError(t, err)
	assert.True(t, res.Allowed)

	// No sweep before the interval has passed again.
	clock.Advance(localLimiterSweepInterval / 2)

	_, err = takeOne(t.Context(), l, "C_OTHER", 1, limit)
	require.NoError(t, err)
	assert.Equal(t, 3, l.size())
}

// mockScripter is a mock redis.UniversalClient that only supports script evaluation.
type mockScripter struct {
	mock.Mock
	redis.UniversalClient
}

func (m *mockScripter) EvalSha(ctx context.Context, _ string, keys []string, args ...any) *redis.Cmd {
	return m.result(ctx, m.Called(keys, args))
}

func (m *mockScripter) Eval(ctx context.Context, _ string, keys []string, args ...any) *redis.Cmd {
	return m.result(ctx, m.Called(keys, args))
}

func (m *mockScripter) result(ctx context.Context, args mock.Arguments) *redis.Cmd {
	cmd := redis.NewCmd(ctx)
	if v := args.Get(0); v != nil {
		cmd.SetVal(v)
	}
	if err := args.Error(1); err != nil {
		cmd.SetErr(err)
	}

	return cmd
}

func TestRedisAlertRateLimiter_TakeAll(t *testing.T) {
	t.Parallel()

	limit := config.RateLimitConfig{AlertsPerSecond: 2, AllowedBurst: 30}

	tests := []struct {
		name     string
		counts   map[string]int
		wantKeys []string
		wantArgs []any
		reply    any
		err      error
		want     map[string]RateLimitResult
		wantErr  string
	}{
		{
			name:     "allowed",
			counts:   map[string]int{"C1": 5},
			wantKeys: []string{"prefix:{alert-rate-limit}:C1"},
			wantArgs: []any{2.0, 30, 5},
			reply:    []any{int64(1), int64(25), int64(0), int64(2_500_000)},
			want:     map[string]RateLimitResult{"C1": {Allowed: true, Remaining: 25, Reset: 2500 * time.Millisecond}},
		},
		{
			name:     "denied",
			counts:   map[string]int{"C1": 5},
			wantKeys: []string{"prefix:{alert-rate-limit}:C1"},
			wantArgs: []any{2.0, 30, 5},
			reply:    []any{int64(0), int64(3), int64(1_000_000), int64(13_500_000)},
			want:     map[string]RateLimitResult{"C1": {Allowed: false, Remaining: 3, RetryAfter: time.Second, Reset: 13500 * time.Millisecond}},
		},
		{
			name:     "denied forever",
			counts:   map[string]int{"C1": 5},
			wantKeys: []string{"prefix:{alert-rate-limit}:C1"},
			wantArgs: []any{2.0, 30, 5},
			reply:    []any{int64(0), int64(30), int64(-1), int64(0)},
			want:     map[string]RateLimitResult{"C1": {Allowed: false, Remaining: 30, RetryAfter: maxRetryAfterDelay}},
		},
		{
			name:     "several channels, sorted",
			counts:   map[string]int{"C2": 7, "C1": 5},
			wantKeys: []string{"prefix:{alert-rate-limit}:C1", "prefix:{alert-rate-limit}:C2"},
			wantArgs: []any{2.0, 30, 5, 7},
			reply:    []any{int64(0), int64(20), int64(0), int64(5_000_000), int64(3), int64(2_000_000), int64(13_500_000)},
			want: map[string]RateLimitResult{
				"C1": {Allowed: false, Remaining: 20, Reset: 5 * time.Second},
				"C2": {Allowed: false, Remaining: 3, RetryAfter: 2 * time.Second, Reset: 13500 * time.Millisecond},
			},
		},
		{
			name:     "redis error",
			counts:   map[string]int{"C1": 5},
			wantKeys: []string{"prefix:{alert-rate-limit}:C1"},
			wantArgs: []any{2.0, 30, 5},
			err:      errors.New("connection refused"),
			wantErr:  "connection refused",
		},
		{
			name:     "unexpected reply length",
			counts:   map[string]int{"C1": 5},
			wantKeys: []string{"prefix:{alert-rate-limit}:C1"},
			wantArgs: []any{2.0, 30, 5},
			reply:    []any{int64(1)},
			wantErr:  "unexpected reply length 1",
		},
		{
			name:     "unexpected reply type",
			counts:   map[string]int{"C1": 5},
			wantKeys: []string{"prefix:{alert-rate-limit}:C1"},
			wantArgs: []any{2.0, 30, 5},
			reply:    "OK",
			wantErr:  "redis alert rate limiter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := &mockScripter{}
			client.On("EvalSha", tt.wantKeys, tt.wantArgs).Return(tt.reply, tt.err)

			l := NewRedisAlertRateLimiter(client, "prefix:")
			results, err := l.TakeAll(t.Context(), tt.counts, limit)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, results)
			client.AssertExpectations(t)
		})
	}
}

func TestRedisAlertRateLimiter_TakeAllEmpty(t *testing.T) {
	t.Parallel()

	client := &mockScripter{}
	l := NewRedisAlertRateLimiter(client, "")

	results, err := l.TakeAll(t.Context(), nil, config.RateLimitConfig{AlertsPerSecond: 1, AllowedBurst: 1})

	require.NoError(t, err)
	assert.Empty(t, results)
	client.AssertNotCalled(t, "EvalSha", mock.Anything, mock.Anything)
}

func TestRedisAlertRateLimiter_LoadsScriptWhenMissing(t *testing.T) {
	t.Parallel()

	client := &mockScripter{}
	client.On("EvalSha", mock.Anything, mock.Anything).Return(nil, redis.ErrNoScript)
	client.On("Eval", mock.Anything, mock.Anything).Return([]any{int64(1), int64(0), int64(0), int64(0)}, nil)

	l := NewRedisAlertRateLimiter(client, "")
	res, err := takeOne(t.Context(), l, "C1", 1, config.RateLimitConfig{AlertsPerSecond: 1, AllowedBurst: 1})

	require.NoError(t, err)
	assert.True(t, res.Allowed)
	client.AssertExpectations(t)
}

func TestNewRedisAlertRateLimiter_DefaultKeyPrefix(t *testing.T) {
	t.Parallel()

	l := NewRedisAlertRateLimiter(nil, "")
	assert.Equal(t, config.DefaultKeyPrefix+"{alert-rate-limit}:C1", l.key("C1"))
}
