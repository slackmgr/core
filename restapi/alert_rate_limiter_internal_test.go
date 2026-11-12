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

func newTestLocalLimiter() (*LocalAlertRateLimiter, *fakeClock) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	l := NewLocalAlertRateLimiter()
	l.now = clock.Now

	return l, clock
}

func TestLocalAlertRateLimiter_Take(t *testing.T) {
	t.Parallel()

	limit := config.RateLimitConfig{AlertsPerSecond: 1, AllowedBurst: 10}

	t.Run("takes tokens within burst", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestLocalLimiter()

		res, err := l.Take(t.Context(), "C1", 4, limit)

		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 6, res.Remaining)
		assert.Equal(t, time.Duration(0), res.RetryAfter)
		assert.Equal(t, 4*time.Second, res.Reset)
	})

	t.Run("denies when bucket is exhausted, without taking tokens", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestLocalLimiter()

		res, err := l.Take(t.Context(), "C1", 8, limit)
		require.NoError(t, err)
		require.True(t, res.Allowed)

		res, err = l.Take(t.Context(), "C1", 5, limit)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 2, res.Remaining)
		assert.Equal(t, 3*time.Second, res.RetryAfter)

		// The denied request took no tokens.
		res, err = l.Take(t.Context(), "C1", 2, limit)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
	})

	t.Run("refills over time", func(t *testing.T) {
		t.Parallel()

		l, clock := newTestLocalLimiter()

		_, err := l.Take(t.Context(), "C1", 10, limit)
		require.NoError(t, err)

		clock.Advance(3 * time.Second)

		res, err := l.Take(t.Context(), "C1", 3, limit)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
	})

	t.Run("caps retry after when count exceeds burst", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestLocalLimiter()

		res, err := l.Take(t.Context(), "C1", 11, limit)

		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, maxRetryAfterDelay, res.RetryAfter)
	})

	t.Run("channels are independent", func(t *testing.T) {
		t.Parallel()

		l, _ := newTestLocalLimiter()

		_, err := l.Take(t.Context(), "C1", 10, limit)
		require.NoError(t, err)

		res, err := l.Take(t.Context(), "C2", 10, limit)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
	})
}

func TestLocalAlertRateLimiter_Sweep(t *testing.T) {
	t.Parallel()

	limit := config.RateLimitConfig{AlertsPerSecond: 1, AllowedBurst: 120}

	l, clock := newTestLocalLimiter()

	_, err := l.Take(t.Context(), "C_IDLE", 1, limit)
	require.NoError(t, err)
	_, err = l.Take(t.Context(), "C_BUSY", 100, limit)
	require.NoError(t, err)
	require.Equal(t, 2, l.size())

	// After the sweep interval, C_IDLE is full again (1 token refilled), C_BUSY is not (100 tokens missing).
	clock.Advance(localLimiterSweepInterval)

	_, err = l.Take(t.Context(), "C_OTHER", 1, limit)
	require.NoError(t, err)

	l.mu.Lock()
	_, idleExists := l.limiters["C_IDLE"]
	_, busyExists := l.limiters["C_BUSY"]
	l.mu.Unlock()

	assert.False(t, idleExists, "full bucket should be removed")
	assert.True(t, busyExists, "partially drained bucket should be kept")

	// Removing C_IDLE does not change its limit: a new bucket is full, just like the removed one.
	res, err := l.Take(t.Context(), "C_IDLE", 120, limit)
	require.NoError(t, err)
	assert.True(t, res.Allowed)

	// No sweep before the interval has passed again.
	clock.Advance(localLimiterSweepInterval / 2)

	_, err = l.Take(t.Context(), "C_OTHER", 1, limit)
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

func TestRedisAlertRateLimiter_Take(t *testing.T) {
	t.Parallel()

	limit := config.RateLimitConfig{AlertsPerSecond: 2, AllowedBurst: 30}
	wantArgs := []any{2.0, 30, 5}

	tests := []struct {
		name    string
		reply   any
		err     error
		want    RateLimitResult
		wantErr string
	}{
		{
			name:  "allowed",
			reply: []any{int64(1), int64(25), int64(0), int64(2_500_000)},
			want:  RateLimitResult{Allowed: true, Remaining: 25, Reset: 2500 * time.Millisecond},
		},
		{
			name:  "denied",
			reply: []any{int64(0), int64(3), int64(1_000_000), int64(13_500_000)},
			want:  RateLimitResult{Allowed: false, Remaining: 3, RetryAfter: time.Second, Reset: 13500 * time.Millisecond},
		},
		{
			name:  "denied forever",
			reply: []any{int64(0), int64(30), int64(-1), int64(0)},
			want:  RateLimitResult{Allowed: false, Remaining: 30, RetryAfter: maxRetryAfterDelay},
		},
		{
			name:    "redis error",
			err:     errors.New("connection refused"),
			wantErr: "connection refused",
		},
		{
			name:    "unexpected reply length",
			reply:   []any{int64(1)},
			wantErr: "unexpected reply length 1",
		},
		{
			name:    "unexpected reply type",
			reply:   "OK",
			wantErr: "redis alert rate limiter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := &mockScripter{}
			client.On("EvalSha", []string{"prefix:alert-rate-limit:C1"}, wantArgs).Return(tt.reply, tt.err)

			l := NewRedisAlertRateLimiter(client, "prefix:")
			res, err := l.Take(t.Context(), "C1", 5, limit)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, res)
			client.AssertExpectations(t)
		})
	}
}

func TestRedisAlertRateLimiter_LoadsScriptWhenMissing(t *testing.T) {
	t.Parallel()

	client := &mockScripter{}
	client.On("EvalSha", mock.Anything, mock.Anything).Return(nil, redis.ErrNoScript)
	client.On("Eval", mock.Anything, mock.Anything).Return([]any{int64(1), int64(0), int64(0), int64(0)}, nil)

	l := NewRedisAlertRateLimiter(client, "")
	res, err := l.Take(t.Context(), "C1", 1, config.RateLimitConfig{AlertsPerSecond: 1, AllowedBurst: 1})

	require.NoError(t, err)
	assert.True(t, res.Allowed)
	client.AssertExpectations(t)
}

func TestNewRedisAlertRateLimiter_DefaultKeyPrefix(t *testing.T) {
	t.Parallel()

	l := NewRedisAlertRateLimiter(nil, "")
	assert.Equal(t, config.DefaultKeyPrefix+"alert-rate-limit:C1", l.key("C1"))
}
