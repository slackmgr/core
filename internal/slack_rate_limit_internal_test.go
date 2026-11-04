package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gocache_store "github.com/eko/gocache/store/go_cache/v4"
	gocache "github.com/patrickmn/go-cache"
	"github.com/slack-go/slack"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingMetrics records counter increments by metric name and label values.
type recordingMetrics struct {
	types.NoopMetrics

	mu       sync.Mutex
	counters map[string]int
	observed map[string]int
}

func newRecordingMetrics() *recordingMetrics {
	return &recordingMetrics{counters: map[string]int{}, observed: map[string]int{}}
}

func (m *recordingMetrics) CounterInc(name string, labelValues ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[name+"|"+strings.Join(labelValues, ",")]++
}

func (m *recordingMetrics) Observe(name string, _ float64, labelValues ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.observed[name+"|"+strings.Join(labelValues, ",")]++
}

func (m *recordingMetrics) counter(name string, labelValues ...string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counters[name+"|"+strings.Join(labelValues, ",")]
}

func (m *recordingMetrics) observations(name string, labelValues ...string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.observed[name+"|"+strings.Join(labelValues, ",")]
}

// spyGate records rate limit gate signals.
type spyGate struct {
	signals atomic.Int32
}

func (g *spyGate) Signal(_ context.Context, _ time.Time) error {
	g.signals.Add(1)
	return nil
}

// failingGate fails every signal.
type failingGate struct{}

func (g *failingGate) Signal(_ context.Context, _ time.Time) error {
	return errors.New("gate unavailable")
}

func newTestBackoff() *methodBackoff {
	store := gocache_store.NewGoCache(gocache.New(5*time.Minute, time.Minute))
	return newMethodBackoff(store, "test:", &types.NoopLogger{})
}

// newRateLimitTestClient returns a test client that records metrics and signals a spy gate.
// Rate limit errors are not retried, so that tests never sleep for a Retry-After.
func newRateLimitTestClient(t *testing.T, handler http.HandlerFunc) (*SlackAPIClient, *recordingMetrics, *spyGate) {
	t.Helper()

	c := newTestSlackAPIClient(t, handler)
	metrics := newRecordingMetrics()
	gate := &spyGate{}

	c.metrics = metrics
	c.gate = gate
	c.cfg.MaxAttemptsForRateLimitError = 1

	return c, metrics, gate
}

func writeRateLimited(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusTooManyRequests)
}

func channelInfoResponse(name string) map[string]any {
	return map[string]any{"ok": true, "channel": map[string]any{"id": "C1", "name": name}}
}

// seedStaleEntry stores a lookup cache entry that is no longer fresh.
func seedStaleEntry[T any](t *testing.T, c *SlackAPIClient, key string, value T) {
	t.Helper()

	data, err := json.Marshal(lookupCacheEntry[T]{FreshUntil: time.Now().Add(-time.Minute), Value: value})
	require.NoError(t, err)

	c.cache.Set(context.Background(), lookupCacheKeyVersion+key, string(data), time.Hour)
}

func TestRateLimitGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action string
		want   string
	}{
		{"chat.post", RateLimitGroupWrite},
		{"chat.update", RateLimitGroupWrite},
		{"chat.delete", RateLimitGroupWrite},
		{"conversations.info", RateLimitGroupLookup},
		{"conversations.members", RateLimitGroupLookup},
		{"conversations.history", RateLimitGroupLookup},
		{"users.info", RateLimitGroupLookup},
		{"usergroups.users.list", RateLimitGroupLookup},
		{"chat.postEphemeral", RateLimitGroupLookup},
		{"views.update", RateLimitGroupLookup},
		{"", RateLimitGroupLookup},
	}

	for _, tc := range tests {
		t.Run(tc.action, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, RateLimitGroup(tc.action))
		})
	}
}

func TestIsRateLimitError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"rate limited", &slack.RateLimitedError{RetryAfter: time.Second}, true},
		{"wrapped rate limited", fmt.Errorf("failed: %w", &slack.RateLimitedError{}), true},
		{"backoff", ErrRateLimitBackoff, true},
		{"wrapped backoff", fmt.Errorf("%w: conversations.info", ErrRateLimitBackoff), true},
		{"ratelimited string", errors.New(SlackErrRateLimited), false},
		{"other", errors.New("channel_not_found"), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, IsRateLimitError(tc.err))
		})
	}
}

func TestMethodBackoff(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("no deadline", func(t *testing.T) {
		t.Parallel()

		b := newTestBackoff()
		assert.True(t, b.blockedUntil(ctx, "conversations.info").IsZero())
	})

	t.Run("signal records deadline per method", func(t *testing.T) {
		t.Parallel()

		b := newTestBackoff()
		until := time.Now().Add(10 * time.Second)

		b.signal(ctx, "conversations.info", until)

		assert.Equal(t, until.UnixMilli(), b.blockedUntil(ctx, "conversations.info").UnixMilli())
		assert.True(t, b.blockedUntil(ctx, "users.info").IsZero(), "other methods are not affected")
	})

	t.Run("signal extends but never shortens", func(t *testing.T) {
		t.Parallel()

		b := newTestBackoff()
		later := time.Now().Add(20 * time.Second)
		earlier := time.Now().Add(5 * time.Second)

		b.signal(ctx, "conversations.info", later)
		b.signal(ctx, "conversations.info", earlier)
		assert.Equal(t, later.UnixMilli(), b.blockedUntil(ctx, "conversations.info").UnixMilli())

		latest := time.Now().Add(30 * time.Second)
		b.signal(ctx, "conversations.info", latest)
		assert.Equal(t, latest.UnixMilli(), b.blockedUntil(ctx, "conversations.info").UnixMilli())
	})

	t.Run("past deadline is ignored", func(t *testing.T) {
		t.Parallel()

		b := newTestBackoff()
		b.signal(ctx, "conversations.info", time.Now().Add(-time.Second))
		assert.True(t, b.blockedUntil(ctx, "conversations.info").IsZero())
	})

	t.Run("invalid value is treated as no backoff", func(t *testing.T) {
		t.Parallel()

		b := newTestBackoff()
		b.cache.Set(ctx, "conversations.info", "not-a-number", time.Minute)
		assert.True(t, b.blockedUntil(ctx, "conversations.info").IsZero())
	})

	t.Run("key format", func(t *testing.T) {
		t.Parallel()

		store := gocache_store.NewGoCache(gocache.New(5*time.Minute, time.Minute))
		b := newMethodBackoff(store, "prefix:", &types.NoopLogger{})
		b.signal(ctx, "conversations.info", time.Now().Add(10*time.Second))

		raw := NewCache(store, "", &types.NoopLogger{})
		_, found := raw.Get(ctx, "prefix:rate-limit-gate:conversations.info")
		assert.True(t, found)
	})
}

func TestAwaitMethodBackoff(t *testing.T) {
	t.Parallel()

	budgetEnd := func() time.Time { return time.Now().Add(time.Minute) }

	t.Run("not backing off", func(t *testing.T) {
		t.Parallel()

		c, metrics, _ := newRateLimitTestClient(t, nil)

		require.NoError(t, c.awaitMethodBackoff(context.Background(), c.logger, "conversations.info", budgetEnd(), false))
		assert.Equal(t, 0, metrics.counter(slackAPIBackoffTotalMetric, "conversations.info", "rejected"))
		assert.Equal(t, 0, metrics.counter(slackAPIBackoffTotalMetric, "conversations.info", "waited"))
	})

	t.Run("backoff disabled", func(t *testing.T) {
		t.Parallel()

		c, _, _ := newRateLimitTestClient(t, nil)
		c.backoff = nil

		require.NoError(t, c.awaitMethodBackoff(context.Background(), c.logger, "conversations.info", budgetEnd(), true))
	})

	t.Run("write methods are not checked", func(t *testing.T) {
		t.Parallel()

		c, _, _ := newRateLimitTestClient(t, nil)
		c.backoff.signal(context.Background(), "chat.update", time.Now().Add(time.Minute))

		require.NoError(t, c.awaitMethodBackoff(context.Background(), c.logger, "chat.update", budgetEnd(), true))
	})

	t.Run("fail fast", func(t *testing.T) {
		t.Parallel()

		c, metrics, _ := newRateLimitTestClient(t, nil)
		c.backoff.signal(context.Background(), "conversations.info", time.Now().Add(time.Minute))

		err := c.awaitMethodBackoff(context.Background(), c.logger, "conversations.info", budgetEnd(), true)
		require.ErrorIs(t, err, ErrRateLimitBackoff)
		assert.Equal(t, 1, metrics.counter(slackAPIBackoffTotalMetric, "conversations.info", "rejected"))
	})

	t.Run("deadline beyond context deadline", func(t *testing.T) {
		t.Parallel()

		c, metrics, _ := newRateLimitTestClient(t, nil)
		c.backoff.signal(context.Background(), "conversations.info", time.Now().Add(time.Minute))

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		start := time.Now()
		err := c.awaitMethodBackoff(ctx, c.logger, "conversations.info", budgetEnd(), false)
		require.ErrorIs(t, err, ErrRateLimitBackoff)
		assert.Less(t, time.Since(start), 500*time.Millisecond, "fails fast instead of waiting for the context")
		assert.Equal(t, 1, metrics.counter(slackAPIBackoffTotalMetric, "conversations.info", "rejected"))
	})

	t.Run("deadline beyond retry budget", func(t *testing.T) {
		t.Parallel()

		c, _, _ := newRateLimitTestClient(t, nil)
		c.backoff.signal(context.Background(), "conversations.info", time.Now().Add(time.Minute))

		err := c.awaitMethodBackoff(context.Background(), c.logger, "conversations.info", time.Now().Add(time.Second), false)
		require.ErrorIs(t, err, ErrRateLimitBackoff)
	})

	t.Run("waits for a short backoff", func(t *testing.T) {
		t.Parallel()

		c, metrics, _ := newRateLimitTestClient(t, nil)
		c.backoff.signal(context.Background(), "conversations.info", time.Now().Add(200*time.Millisecond))

		start := time.Now()
		require.NoError(t, c.awaitMethodBackoff(context.Background(), c.logger, "conversations.info", budgetEnd(), false))
		assert.GreaterOrEqual(t, time.Since(start), 150*time.Millisecond)
		assert.Equal(t, 1, metrics.counter(slackAPIBackoffTotalMetric, "conversations.info", "waited"))
	})

	t.Run("context cancelled while waiting", func(t *testing.T) {
		t.Parallel()

		c, _, _ := newRateLimitTestClient(t, nil)
		c.backoff.signal(context.Background(), "conversations.info", time.Now().Add(300*time.Millisecond))

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		err := c.awaitMethodBackoff(ctx, c.logger, "conversations.info", budgetEnd(), false)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestSignalRateLimit(t *testing.T) {
	t.Parallel()

	t.Run("lookup method backs off the method only", func(t *testing.T) {
		t.Parallel()

		c, metrics, gate := newRateLimitTestClient(t, nil)

		c.signalRateLimit(context.Background(), c.logger, "conversations.info", 10*time.Second)

		assert.Equal(t, int32(0), gate.signals.Load(), "global gate not signalled")
		assert.True(t, c.backoff.blockedUntil(context.Background(), "conversations.info").After(time.Now().Add(11*time.Second)))
		assert.Equal(t, 1, metrics.counter(slackAPIRateLimitedTotalMetric, "conversations.info", RateLimitGroupLookup))
		assert.Equal(t, 1, metrics.observations(slackAPIRateLimitDurationMetric, "conversations.info"))
	})

	t.Run("write method signals the global gate", func(t *testing.T) {
		t.Parallel()

		c, metrics, gate := newRateLimitTestClient(t, nil)

		c.signalRateLimit(context.Background(), c.logger, "chat.update", 10*time.Second)

		assert.Equal(t, int32(1), gate.signals.Load())
		assert.False(t, c.backoff.blockedUntil(context.Background(), "chat.update").IsZero())
		assert.Equal(t, 1, metrics.counter(slackAPIRateLimitedTotalMetric, "chat.update", RateLimitGroupWrite))
	})

	t.Run("no gate and no backoff", func(t *testing.T) {
		t.Parallel()

		c, _, _ := newRateLimitTestClient(t, nil)
		c.gate = nil
		c.backoff = nil

		assert.NotPanics(t, func() { c.signalRateLimit(context.Background(), c.logger, "chat.post", time.Second) })
	})

	t.Run("gate error is logged, not returned", func(t *testing.T) {
		t.Parallel()

		c, _, _ := newRateLimitTestClient(t, nil)
		c.gate = &failingGate{}

		assert.NotPanics(t, func() { c.signalRateLimit(context.Background(), c.logger, "chat.post", time.Second) })
	})
}

// TestRateLimitGroups_EndToEnd verifies that a 429 on a lookup does not signal the global gate, while a 429 on a
// write does, also when the 429 is not retried.
func TestRateLimitGroups_EndToEnd(t *testing.T) {
	t.Parallel()

	t.Run("lookup 429", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		c, metrics, gate := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			writeRateLimited(w)
		})

		_, err := c.GetChannelInfo(context.Background(), "C1")
		require.Error(t, err)
		assert.True(t, IsRateLimitError(err))
		assert.Equal(t, int32(0), gate.signals.Load())
		assert.Equal(t, 1, metrics.counter(slackAPIRateLimitedTotalMetric, "conversations.info", RateLimitGroupLookup))

		// The next call fails fast on the method backoff, without calling Slack.
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		_, err = c.GetChannelInfo(ctx, "C2")
		require.ErrorIs(t, err, ErrRateLimitBackoff)
		assert.Equal(t, int32(1), calls.Load())

		// Other lookup methods are not affected.
		assert.True(t, c.backoff.blockedUntil(context.Background(), "users.info").IsZero())
	})

	t.Run("write 429", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		c, metrics, gate := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			writeRateLimited(w)
		})

		_, err := c.ChatUpdateMessage(context.Background(), "C1", slack.MsgOptionUpdate("1.1"), slack.MsgOptionText("x", false))
		require.Error(t, err)
		assert.Equal(t, int32(1), gate.signals.Load())
		assert.Equal(t, 1, metrics.counter(slackAPIRateLimitedTotalMetric, "chat.update", RateLimitGroupWrite))

		// Write methods do not wait for their per-method backoff: they are paused by the gate between channel cycles.
		_, err = c.ChatUpdateMessage(context.Background(), "C1", slack.MsgOptionUpdate("1.1"), slack.MsgOptionText("x", false))
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrRateLimitBackoff)
		assert.Equal(t, int32(2), calls.Load())
	})
}

func TestGetChannelInfo_Caching(t *testing.T) {
	t.Parallel()

	t.Run("found channel is cached", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		c, metrics, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			assert.Equal(t, "/conversations.info", r.URL.Path)
			writeJSON(w, channelInfoResponse("alerts"))
		})

		for range 3 {
			channel, err := c.GetChannelInfo(context.Background(), "C1")
			require.NoError(t, err)
			assert.Equal(t, "alerts", channel.Name)
		}

		assert.Equal(t, int32(1), calls.Load())
		assert.Equal(t, 1, metrics.counter(slackClientCacheRequestsTotalMetric, "conversations.info", "miss"))
		assert.Equal(t, 2, metrics.counter(slackClientCacheRequestsTotalMetric, "conversations.info", "hit"))

		entry, ok := getLookupCacheEntry[*slack.Channel](context.Background(), c, c.logger, lookupCacheKeyVersion+"GetChannelInfo:C1")
		require.True(t, ok)

		ttl := c.cfg.ChannelInfoCacheTTL()
		freshFor := time.Until(entry.FreshUntil)
		assert.Greater(t, freshFor, ttl-5*time.Second)
		assert.LessOrEqual(t, freshFor, ttl+ttl/5)
	})

	t.Run("channel_not_found is cached for the shorter TTL", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		c, _, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			writeJSON(w, map[string]any{"ok": false, "error": SlackErrChannelNotFound})
		})

		for range 2 {
			_, err := c.GetChannelInfo(context.Background(), "C1")
			require.Error(t, err)
			assert.Equal(t, SlackErrChannelNotFound, err.Error())
		}

		assert.Equal(t, int32(1), calls.Load())

		entry, ok := getLookupCacheEntry[*slack.Channel](context.Background(), c, c.logger, lookupCacheKeyVersion+"GetChannelInfo:C1")
		require.True(t, ok)
		assert.True(t, entry.NotFound)
		assert.LessOrEqual(t, time.Until(entry.FreshUntil), c.cfg.ChannelNotFoundCacheTTL())
	})

	t.Run("stale value is served during backoff without calling Slack", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		c, metrics, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			writeJSON(w, channelInfoResponse("new-name"))
		})

		seedStaleEntry(t, c, "GetChannelInfo:C1", &slack.Channel{GroupConversation: slack.GroupConversation{Name: "old-name"}})
		c.backoff.signal(context.Background(), "conversations.info", time.Now().Add(time.Minute))

		channel, err := c.GetChannelInfo(context.Background(), "C1")
		require.NoError(t, err)
		assert.Equal(t, "old-name", channel.Name)
		assert.Equal(t, int32(0), calls.Load())
		assert.Equal(t, 1, metrics.counter(slackClientCacheRequestsTotalMetric, "conversations.info", "stale"))
	})

	t.Run("stale value is refreshed when not backing off", func(t *testing.T) {
		t.Parallel()

		c, _, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, channelInfoResponse("new-name"))
		})

		seedStaleEntry(t, c, "GetChannelInfo:C1", &slack.Channel{GroupConversation: slack.GroupConversation{Name: "old-name"}})

		channel, err := c.GetChannelInfo(context.Background(), "C1")
		require.NoError(t, err)
		assert.Equal(t, "new-name", channel.Name)
	})

	t.Run("stale value is served on 429, without retrying", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		c, metrics, gate := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			writeRateLimited(w)
		})
		c.cfg.MaxAttemptsForRateLimitError = 10

		seedStaleEntry(t, c, "GetChannelInfo:C1", &slack.Channel{GroupConversation: slack.GroupConversation{Name: "old-name"}})

		channel, err := c.GetChannelInfo(context.Background(), "C1")
		require.NoError(t, err)
		assert.Equal(t, "old-name", channel.Name)
		assert.Equal(t, int32(1), calls.Load())
		assert.Equal(t, int32(0), gate.signals.Load())
		assert.Equal(t, 1, metrics.counter(slackClientCacheRequestsTotalMetric, "conversations.info", "stale"))
	})

	t.Run("stale value is not served on other errors", func(t *testing.T) {
		t.Parallel()

		c, _, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]any{"ok": false, "error": "invalid_auth"})
		})

		seedStaleEntry(t, c, "GetChannelInfo:C1", &slack.Channel{GroupConversation: slack.GroupConversation{Name: "old-name"}})

		_, err := c.GetChannelInfo(context.Background(), "C1")
		require.Error(t, err)
		assert.Equal(t, "invalid_auth", err.Error())
	})

	t.Run("invalid cache entry is refetched", func(t *testing.T) {
		t.Parallel()

		c, _, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, channelInfoResponse("alerts"))
		})

		c.cache.Set(context.Background(), lookupCacheKeyVersion+"GetChannelInfo:C1", "{invalid", time.Minute)

		channel, err := c.GetChannelInfo(context.Background(), "C1")
		require.NoError(t, err)
		assert.Equal(t, "alerts", channel.Name)
	})

	t.Run("uses configured TTLs", func(t *testing.T) {
		t.Parallel()

		c, _, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, channelInfoResponse("alerts"))
		})
		c.cfg.ChannelInfoCacheTTLSeconds = 1000

		_, err := c.GetChannelInfo(context.Background(), "C1")
		require.NoError(t, err)

		entry, ok := getLookupCacheEntry[*slack.Channel](context.Background(), c, c.logger, lookupCacheKeyVersion+"GetChannelInfo:C1")
		require.True(t, ok)
		assert.Greater(t, time.Until(entry.FreshUntil), 990*time.Second)
	})
}

func TestGetUserInfo_Caching(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	c, _, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/users.info", r.URL.Path)
		writeJSON(w, map[string]any{"ok": true, "user": map[string]any{"id": "U1", "real_name": "Jane"}})
	})

	for range 2 {
		user, err := c.GetUserInfo(context.Background(), "U1")
		require.NoError(t, err)
		assert.Equal(t, "Jane", user.RealName)
	}

	assert.Equal(t, int32(1), calls.Load())

	t.Run("stale on 429", func(t *testing.T) {
		t.Parallel()

		c, _, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) { writeRateLimited(w) })
		seedStaleEntry(t, c, "GetUserInfo:U1", &slack.User{ID: "U1", RealName: "Old"})

		user, err := c.GetUserInfo(context.Background(), "U1")
		require.NoError(t, err)
		assert.Equal(t, "Old", user.RealName)
	})

	t.Run("error without stale value", func(t *testing.T) {
		t.Parallel()

		c, _, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) { writeRateLimited(w) })

		_, err := c.GetUserInfo(context.Background(), "U1")
		require.Error(t, err)
		assert.True(t, IsRateLimitError(err))
	})
}

func TestListUserGroupMembers_Caching(t *testing.T) {
	t.Parallel()

	t.Run("members are cached", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		c, _, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			writeJSON(w, map[string]any{"ok": true, "users": []string{"U1", "U2"}})
		})

		for range 2 {
			members, err := c.ListUserGroupMembers(context.Background(), "S1")
			require.NoError(t, err)
			assert.Len(t, members, 2)
		}

		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("unknown group has no members", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		c, _, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			writeJSON(w, map[string]any{"ok": false, "error": SlackErrNoSuchSubTeam})
		})

		for range 2 {
			members, err := c.ListUserGroupMembers(context.Background(), "S1")
			require.NoError(t, err)
			assert.NotNil(t, members)
			assert.Empty(t, members)
		}

		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("error", func(t *testing.T) {
		t.Parallel()

		c, _, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]any{"ok": false, "error": "invalid_auth"})
		})

		_, err := c.ListUserGroupMembers(context.Background(), "S1")
		require.Error(t, err)
	})
}

func TestGetUserIDsInChannel_Caching(t *testing.T) {
	t.Parallel()

	t.Run("members are read from all pages and cached", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		c, _, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if err := r.ParseForm(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			if r.Form.Get("cursor") == "" {
				writeJSON(w, map[string]any{"ok": true, "members": []string{"U1"}, "response_metadata": map[string]any{"next_cursor": "next"}})
				return
			}

			writeJSON(w, map[string]any{"ok": true, "members": []string{"U2"}})
		})

		for range 2 {
			members, err := c.GetUserIDsInChannel(context.Background(), "C1")
			require.NoError(t, err)
			assert.Equal(t, map[string]struct{}{"U1": {}, "U2": {}}, members)
		}

		assert.Equal(t, int32(2), calls.Load())
	})

	t.Run("unknown channel has no members", func(t *testing.T) {
		t.Parallel()

		c, _, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]any{"ok": false, "error": SlackErrChannelNotFound})
		})

		members, err := c.GetUserIDsInChannel(context.Background(), "C1")
		require.NoError(t, err)
		assert.NotNil(t, members)
		assert.Empty(t, members)

		isMember, err := c.BotIsInChannel(context.Background(), "C1")
		require.NoError(t, err)
		assert.False(t, isMember)
	})

	t.Run("error", func(t *testing.T) {
		t.Parallel()

		c, _, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]any{"ok": false, "error": "invalid_auth"})
		})

		_, err := c.GetUserIDsInChannel(context.Background(), "C1")
		require.Error(t, err)
	})
}

func TestCachedLookup_TransientErrors(t *testing.T) {
	t.Parallel()

	t.Run("stale value is served on a transient error", func(t *testing.T) {
		t.Parallel()

		c, metrics, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		})
		c.cfg.MaxAttemptsForTransientError = 1

		seedStaleEntry(t, c, "GetChannelInfo:C1", &slack.Channel{GroupConversation: slack.GroupConversation{Name: "old-name"}})

		channel, err := c.GetChannelInfo(context.Background(), "C1")
		require.NoError(t, err)
		assert.Equal(t, "old-name", channel.Name)
		assert.Equal(t, 1, metrics.counter(slackClientCacheRequestsTotalMetric, "conversations.info", "stale"))
	})

	t.Run("transient error without stale value", func(t *testing.T) {
		t.Parallel()

		c, metrics, _ := newRateLimitTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		})
		c.cfg.MaxAttemptsForTransientError = 1

		_, err := c.GetChannelInfo(context.Background(), "C1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "transient error")
		assert.Equal(t, 1, metrics.counter(slackClientCacheRequestsTotalMetric, "conversations.info", "miss"))
	})
}
