package slack

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	slackapi "github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// historyTestClock is a settable clock for [postHistoryCache].
type historyTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *historyTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *historyTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

func newTestPostHistoryCache() (*postHistoryCache, *historyTestClock) {
	clock := &historyTestClock{now: time.Unix(1700000000, 0)}
	h := newPostHistoryCache()
	h.now = clock.Now

	return h, clock
}

// historyMessage returns a message with the given timestamp, relative to the test clock start.
func historyMessage(offset time.Duration) slackapi.Message {
	t := time.Unix(1700000000, 0).Add(offset)

	msg := slackapi.Message{}
	msg.Timestamp = fmt.Sprintf("%d.%06d", t.Unix(), t.Nanosecond()/1000)

	return msg
}

// countingFetch returns a fetch function that returns the given messages and error, and counts the calls.
func countingFetch(msgs []slackapi.Message, err error) (func(context.Context) ([]slackapi.Message, error), *atomic.Int32) {
	var calls atomic.Int32

	return func(context.Context) ([]slackapi.Message, error) {
		calls.Add(1)
		return msgs, err
	}, &calls
}

func TestPostHistoryCache_Lookup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	start := time.Unix(1700000000, 0)
	msgs := []slackapi.Message{historyMessage(-time.Minute), historyMessage(-time.Second)}

	t.Run("successful lookup is reused within the TTL", func(t *testing.T) {
		t.Parallel()

		h, clock := newTestPostHistoryCache()
		fetch, calls := countingFetch(msgs, nil)

		got, fetchedAt, err := h.lookup(ctx, "C1", start.Add(-2*time.Minute), fetch)
		require.NoError(t, err)
		assert.Equal(t, msgs, got)
		assert.Equal(t, start, fetchedAt)

		clock.Advance(postHistoryCacheTTL - time.Second)

		got, fetchedAt, err = h.lookup(ctx, "C1", start.Add(-2*time.Minute), fetch)
		require.NoError(t, err)
		assert.Equal(t, msgs, got)
		assert.Equal(t, start, fetchedAt, "the fetch time of the cached lookup is returned")
		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("cached lookup is filtered to the requested window", func(t *testing.T) {
		t.Parallel()

		h, _ := newTestPostHistoryCache()
		fetch, calls := countingFetch(msgs, nil)

		_, _, err := h.lookup(ctx, "C1", start.Add(-2*time.Minute), fetch)
		require.NoError(t, err)

		got, _, err := h.lookup(ctx, "C1", start.Add(-10*time.Second), fetch)
		require.NoError(t, err)
		assert.Equal(t, msgs[1:], got)
		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("cached lookup that does not cover the window is not used", func(t *testing.T) {
		t.Parallel()

		h, _ := newTestPostHistoryCache()
		fetch, calls := countingFetch(msgs, nil)

		_, _, err := h.lookup(ctx, "C1", start.Add(-10*time.Second), fetch)
		require.NoError(t, err)

		_, _, err = h.lookup(ctx, "C1", start.Add(-2*time.Minute), fetch)
		require.NoError(t, err)
		assert.Equal(t, int32(2), calls.Load())
	})

	t.Run("lookup is repeated after the TTL", func(t *testing.T) {
		t.Parallel()

		h, clock := newTestPostHistoryCache()
		fetch, calls := countingFetch(msgs, nil)

		_, _, err := h.lookup(ctx, "C1", start, fetch)
		require.NoError(t, err)

		clock.Advance(postHistoryCacheTTL)

		_, fetchedAt, err := h.lookup(ctx, "C1", start, fetch)
		require.NoError(t, err)
		assert.Equal(t, start.Add(postHistoryCacheTTL), fetchedAt)
		assert.Equal(t, int32(2), calls.Load())
	})

	t.Run("channels are cached separately", func(t *testing.T) {
		t.Parallel()

		h, _ := newTestPostHistoryCache()
		fetch, calls := countingFetch(msgs, nil)

		_, _, err := h.lookup(ctx, "C1", start, fetch)
		require.NoError(t, err)

		_, _, err = h.lookup(ctx, "C2", start, fetch)
		require.NoError(t, err)
		assert.Equal(t, int32(2), calls.Load())
	})
}

func TestPostHistoryCache_FailedLookup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	start := time.Unix(1700000000, 0)
	lookupErr := errors.New("some_unknown_error")

	t.Run("failed lookup is not retried within the backoff", func(t *testing.T) {
		t.Parallel()

		h, clock := newTestPostHistoryCache()
		fetch, calls := countingFetch(nil, lookupErr)

		_, _, err := h.lookup(ctx, "C1", start, fetch)
		require.ErrorIs(t, err, lookupErr)

		clock.Advance(postHistoryFailureBackoff - time.Second)

		_, _, err = h.lookup(ctx, "C1", start.Add(-time.Hour), fetch)
		require.ErrorIs(t, err, lookupErr, "the cached error wraps the original error")
		assert.Contains(t, err.Error(), "retrying after")
		assert.Equal(t, int32(1), calls.Load())

		clock.Advance(time.Second)

		_, _, err = h.lookup(ctx, "C1", start, fetch)
		require.ErrorIs(t, err, lookupErr)
		assert.Equal(t, int32(2), calls.Load(), "retried after the backoff")
	})

	t.Run("cancelled lookup is not cached", func(t *testing.T) {
		t.Parallel()

		h, _ := newTestPostHistoryCache()

		cancelled, cancel := context.WithCancel(ctx)

		_, _, err := h.lookup(cancelled, "C1", start, func(context.Context) ([]slackapi.Message, error) {
			cancel()
			return nil, context.Canceled
		})
		require.ErrorIs(t, err, context.Canceled)

		fetch, calls := countingFetch(nil, nil)

		_, _, err = h.lookup(ctx, "C1", start, fetch)
		require.NoError(t, err)
		assert.Equal(t, int32(1), calls.Load())
	})
}

func TestPostHistoryCache_ConcurrentLookupsFetchOnce(t *testing.T) {
	t.Parallel()

	h, _ := newTestPostHistoryCache()
	start := time.Unix(1700000000, 0)
	fetch, calls := countingFetch([]slackapi.Message{historyMessage(0)}, nil)

	var wg sync.WaitGroup

	results := make([][]slackapi.Message, 10)
	errs := make([]error, 10)

	for i := range 10 {
		wg.Go(func() {
			results[i], _, errs[i] = h.lookup(context.Background(), "C1", start.Add(-time.Minute), fetch)
		})
	}

	wg.Wait()

	for i := range 10 {
		require.NoError(t, errs[i])
		assert.Len(t, results[i], 1)
	}

	assert.Equal(t, int32(1), calls.Load())
}

func TestPostHistoryCache_WaitingLookupCanBeCancelled(t *testing.T) {
	t.Parallel()

	h, _ := newTestPostHistoryCache()
	start := time.Unix(1700000000, 0)

	fetching := make(chan struct{})
	release := make(chan struct{})

	go func() {
		_, _, _ = h.lookup(context.Background(), "C1", start, func(context.Context) ([]slackapi.Message, error) {
			close(fetching)
			<-release
			return nil, nil
		})
	}()

	<-fetching

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := h.lookup(ctx, "C1", start, func(context.Context) ([]slackapi.Message, error) {
		t.Error("fetch must not be called")
		return nil, nil
	})

	require.ErrorIs(t, err, context.Canceled)
	close(release)
}

func TestPostHistoryCache_RemoveMessage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	start := time.Unix(1700000000, 0)
	msgs := []slackapi.Message{historyMessage(-2 * time.Second), historyMessage(-time.Second)}

	h, _ := newTestPostHistoryCache()
	fetch, calls := countingFetch(msgs, nil)

	first, _, err := h.lookup(ctx, "C1", start.Add(-time.Minute), fetch)
	require.NoError(t, err)

	h.removeMessage("C1", msgs[0].Timestamp)
	h.removeMessage("C2", msgs[1].Timestamp) // Unknown channel is ignored

	got, _, err := h.lookup(ctx, "C1", start.Add(-time.Minute), fetch)
	require.NoError(t, err)
	assert.Equal(t, msgs[1:], got)
	assert.Equal(t, int32(1), calls.Load(), "still served from the cache")
	assert.Len(t, first, 2, "previously returned messages are not modified")
}

func TestPostHistoryCache_RemoveMessageIgnoresFailedLookup(t *testing.T) {
	t.Parallel()

	h, _ := newTestPostHistoryCache()
	lookupErr := errors.New("some_unknown_error")
	fetch, _ := countingFetch(nil, lookupErr)

	_, _, err := h.lookup(context.Background(), "C1", time.Unix(1700000000, 0), fetch)
	require.ErrorIs(t, err, lookupErr)

	h.removeMessage("C1", "1700000000.000000")

	_, _, err = h.lookup(context.Background(), "C1", time.Unix(1700000000, 0), fetch)
	require.ErrorIs(t, err, lookupErr, "the failed lookup is still cached")
}

func TestMessagesSince(t *testing.T) {
	t.Parallel()

	msgs := []slackapi.Message{historyMessage(-time.Second), historyMessage(0), historyMessage(time.Microsecond)}

	assert.Equal(t, msgs[1:], messagesSince(msgs, time.Unix(1700000000, 0)), "inclusive")
	assert.Empty(t, messagesSince(msgs, time.Unix(1700000001, 0)))
	assert.Equal(t, msgs, messagesSince(msgs, time.Unix(1600000000, 0)))
}
