package slack

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	slack "github.com/slack-go/slack"
	"github.com/slackmgr/core/internal"
)

const (
	// postHistoryCacheTTL is how long a successful channel history lookup is reused (see postHistoryCache).
	postHistoryCacheTTL = 10 * time.Second

	// postHistoryFailureBackoff is how long a failed channel history lookup is reused, before the lookup is retried.
	postHistoryFailureBackoff = 30 * time.Second
)

// postHistoryCache caches the channel history lookups for pending posts (see [Client.resolvePendingPost]), per channel.
//
// A successful lookup is reused for [postHistoryCacheTTL] by all issues in the channel whose lookup window it covers,
// and a failed lookup is not retried within [postHistoryFailureBackoff]. A failure caused by too many messages in the
// channel (see [internal.ErrTooManyMessages]) depends on the lookup window, so it only blocks lookups that start at the
// same time or earlier, and lookups with a shorter window are still made. Concurrent lookups in the same channel are
// serialized, so that only one of them calls Slack. The cache is kept in memory, since a channel is normally only
// processed by one manager instance at a time (channel lock). Posts created by another instance after a lookup are not
// in the cached result, but the grace period is evaluated at the time of the lookup, so they are waited for rather than
// assumed to not exist.
type postHistoryCache struct {
	mu       sync.Mutex
	channels map[string]*postHistoryChannel
	now      func() time.Time
}

// postHistoryChannel is the cache state for one channel.
type postHistoryChannel struct {
	// lock serializes lookups in the channel. It is a channel rather than a mutex, so that waiting can be cancelled.
	lock chan struct{}

	// entry is the latest lookup, or nil, except for lookups that failed with too many messages. Guarded by postHistoryCache.mu.
	entry *postHistoryEntry

	// tooMany is the latest lookup that failed with [internal.ErrTooManyMessages], or nil. It is kept separately from
	// entry, so that a successful lookup with a shorter window and a failing lookup with a longer window do not replace
	// each other, which would cause a new lookup for every issue. Guarded by postHistoryCache.mu.
	tooMany *postHistoryEntry
}

// postHistoryEntry is the result of one channel history lookup.
type postHistoryEntry struct {
	// fetchedAt is when the lookup started. The history snapshot is from this time or later.
	fetchedAt time.Time

	// oldest is the start of the lookup window. The lookup includes the posts since this time.
	oldest time.Time

	// msgs are the issue posts found by a successful lookup.
	msgs []slack.Message

	// err is the error of a failed lookup, or nil.
	err error
}

// newPostHistoryCache returns an empty [postHistoryCache].
func newPostHistoryCache() *postHistoryCache {
	return &postHistoryCache{
		channels: make(map[string]*postHistoryChannel),
		now:      time.Now,
	}
}

// lookup returns the issue posts in the channel since oldest, and the time the history was fetched (see
// [postHistoryEntry.fetchedAt]). The posts are served from the cache if possible, and fetched with fetch otherwise.
// Lookups cancelled by the context are not cached. See [postHistoryCache] for how failed lookups are reused.
func (h *postHistoryCache) lookup(ctx context.Context, channelID string, oldest time.Time, fetch func(ctx context.Context) ([]slack.Message, error)) ([]slack.Message, time.Time, error) {
	ch := h.channel(channelID)

	select {
	case ch.lock <- struct{}{}:
	case <-ctx.Done():
		return nil, time.Time{}, ctx.Err()
	}

	defer func() { <-ch.lock }()

	now := h.now()
	entry, tooMany := h.entries(ch)

	// A lookup with the same or a longer window would fail with too many messages as well
	if tooMany != nil && now.Sub(tooMany.fetchedAt) < postHistoryFailureBackoff && !oldest.After(tooMany.oldest) {
		return nil, tooMany.fetchedAt, cachedLookupError(now, tooMany)
	}

	if entry != nil {
		age := now.Sub(entry.fetchedAt)

		if entry.err != nil && age < postHistoryFailureBackoff {
			return nil, entry.fetchedAt, cachedLookupError(now, entry)
		}

		if entry.err == nil && age < postHistoryCacheTTL && !entry.oldest.After(oldest) {
			return messagesSince(entry.msgs, oldest), entry.fetchedAt, nil
		}
	}

	msgs, err := fetch(ctx)

	if ctx.Err() == nil {
		h.store(ch, &postHistoryEntry{fetchedAt: now, oldest: oldest, msgs: msgs, err: err})
	}

	if err != nil {
		return nil, now, err
	}

	return msgs, now, nil
}

// removeMessage removes a deleted (or soft-deleted) post from the cached lookup for the channel, if any,
// so that it is not adopted by a pending post.
func (h *postHistoryCache) removeMessage(channelID, ts string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch, ok := h.channels[channelID]
	if !ok || ch.entry == nil || ch.entry.err != nil {
		return
	}

	// The cached slice may have been returned to callers, so it is copied rather than modified in place.
	msgs := slices.DeleteFunc(slices.Clone(ch.entry.msgs), func(msg slack.Message) bool {
		return msg.Timestamp == ts
	})

	ch.entry = &postHistoryEntry{fetchedAt: ch.entry.fetchedAt, oldest: ch.entry.oldest, msgs: msgs}
}

// channel returns the cache state for the channel, creating it if needed.
func (h *postHistoryCache) channel(channelID string) *postHistoryChannel {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch, ok := h.channels[channelID]
	if !ok {
		ch = &postHistoryChannel{lock: make(chan struct{}, 1)}
		h.channels[channelID] = ch
	}

	return ch
}

// entries returns the latest lookup for the channel, and the latest lookup that failed with too many messages.
// Either may be nil.
func (h *postHistoryCache) entries(ch *postHistoryChannel) (*postHistoryEntry, *postHistoryEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()

	return ch.entry, ch.tooMany
}

// store caches a completed lookup for the channel. A lookup that failed with too many messages is stored separately
// (see [postHistoryChannel.tooMany]), and a successful lookup that covers its window clears it.
func (h *postHistoryCache) store(ch *postHistoryChannel, e *postHistoryEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if errors.Is(e.err, internal.ErrTooManyMessages) {
		ch.tooMany = e
		return
	}

	if e.err == nil && ch.tooMany != nil && !e.oldest.After(ch.tooMany.oldest) {
		ch.tooMany = nil
	}

	ch.entry = e
}

// cachedLookupError returns the error for a lookup served from a cached failed lookup.
func cachedLookupError(now time.Time, e *postHistoryEntry) error {
	return fmt.Errorf("channel history lookup failed %s ago, retrying after %s: %w", now.Sub(e.fetchedAt).Round(time.Second), postHistoryFailureBackoff, e.err)
}

// messagesSince returns the messages posted at or after oldest. Slack timestamps compare correctly as strings
// (see [internal.FormatSlackTimestamp]).
func messagesSince(msgs []slack.Message, oldest time.Time) []slack.Message {
	since := internal.FormatSlackTimestamp(oldest)

	var result []slack.Message

	for _, msg := range msgs {
		if msg.Timestamp >= since {
			result = append(result, msg)
		}
	}

	return result
}
