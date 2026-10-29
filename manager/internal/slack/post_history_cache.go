package slack

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	slack "github.com/slack-go/slack"
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
// and a failed lookup is not retried within [postHistoryFailureBackoff]. Concurrent lookups in the same channel are
// serialized, so that only one of them calls Slack. The cache is kept in memory, since a channel is only processed by
// one manager instance at a time (channel lock).
type postHistoryCache struct {
	mu       sync.Mutex
	channels map[string]*postHistoryChannel
	now      func() time.Time
}

// postHistoryChannel is the cache state for one channel.
type postHistoryChannel struct {
	// lock serializes lookups in the channel. It is a channel rather than a mutex, so that waiting can be cancelled.
	lock chan struct{}

	// entry is the latest lookup, or nil. Guarded by postHistoryCache.mu.
	entry *postHistoryEntry
}

// postHistoryEntry is the result of one channel history lookup.
type postHistoryEntry struct {
	// fetchedAt is when the lookup started. The history snapshot is from this time or later.
	fetchedAt time.Time
	oldest    time.Time
	msgs      []slack.Message
	err       error
}

func newPostHistoryCache() *postHistoryCache {
	return &postHistoryCache{
		channels: make(map[string]*postHistoryChannel),
		now:      time.Now,
	}
}

// lookup returns the issue posts in the channel since oldest, and the time the history was fetched (see
// [postHistoryEntry.fetchedAt]). The posts are served from the cache if possible, and fetched with fetch otherwise.
// Lookups cancelled by the context are not cached.
func (h *postHistoryCache) lookup(ctx context.Context, channelID string, oldest time.Time, fetch func(ctx context.Context) ([]slack.Message, error)) ([]slack.Message, time.Time, error) {
	ch := h.channel(channelID)

	select {
	case ch.lock <- struct{}{}:
	case <-ctx.Done():
		return nil, time.Time{}, ctx.Err()
	}

	defer func() { <-ch.lock }()

	now := h.now()

	if entry := h.entry(ch); entry != nil {
		age := now.Sub(entry.fetchedAt)

		if entry.err != nil && age < postHistoryFailureBackoff {
			return nil, entry.fetchedAt, fmt.Errorf("channel history lookup failed %s ago, retrying after %s: %w", age.Round(time.Second), postHistoryFailureBackoff, entry.err)
		}

		if entry.err == nil && age < postHistoryCacheTTL && !entry.oldest.After(oldest) {
			return messagesSince(entry.msgs, oldest), entry.fetchedAt, nil
		}
	}

	msgs, err := fetch(ctx)

	if ctx.Err() == nil {
		h.mu.Lock()
		ch.entry = &postHistoryEntry{fetchedAt: now, oldest: oldest, msgs: msgs, err: err}
		h.mu.Unlock()
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

func (h *postHistoryCache) entry(ch *postHistoryChannel) *postHistoryEntry {
	h.mu.Lock()
	defer h.mu.Unlock()

	return ch.entry
}

// messagesSince returns the messages posted at or after oldest. Slack timestamps have a fixed number of decimals
// (and seconds digits until the year 2286), and compare correctly as strings.
func messagesSince(msgs []slack.Message, oldest time.Time) []slack.Message {
	since := fmt.Sprintf("%d.%06d", oldest.Unix(), oldest.Nanosecond()/1000)

	var result []slack.Message

	for _, msg := range msgs {
		if msg.Timestamp >= since {
			result = append(result, msg)
		}
	}

	return result
}
