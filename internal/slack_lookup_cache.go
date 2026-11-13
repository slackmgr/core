package internal

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/slackmgr/types"
)

// lookupCacheKeyVersion is prepended to lookup cache keys. It was introduced together with the
// [lookupCacheEntry] envelope, so that older versions (which store the bare value under the unversioned key)
// and newer versions never read each other's values while both run during a rolling upgrade.
const lookupCacheKeyVersion = "v2:"

// lookupCacheEntry is the cached value of a Slack lookup. It is kept in the cache beyond its freshness, so that a
// stale value can be served while Slack rate limits the lookup method.
type lookupCacheEntry[T any] struct {
	// FreshUntil is when the entry should be fetched again.
	FreshUntil time.Time `json:"freshUntil"`

	// NotFound is true if Slack reported that the looked up item does not exist (e.g. channel_not_found).
	NotFound bool `json:"notFound,omitempty"`

	// Value is the looked up value. It is the zero value if NotFound is true.
	Value T `json:"value"`
}

// lookupPolicy configures caching for one Slack lookup method.
type lookupPolicy struct {
	// action is the Slack API action, used for metrics and the per-method rate limit backoff.
	action string

	// key is the cache key, without [lookupCacheKeyVersion].
	key string

	// ttl is how long a found value is fresh. Up to jitter is added at random.
	ttl    time.Duration
	jitter time.Duration

	// notFoundTTL is how long a not found result is fresh. No jitter is added.
	notFoundTTL time.Duration

	// maxStale is how long after FreshUntil an entry is kept, to be served while the method is rate limited.
	maxStale time.Duration
}

// lookupFetchFunc fetches a value from Slack. notFound is true if Slack reported that the item does not exist.
// failFast is true when a stale value is available: the fetch should then not wait for rate limit backoffs or retry
// after a 429, since the stale value is served instead.
type lookupFetchFunc[T any] func(ctx context.Context, failFast bool) (value T, notFound bool, err error)

// cachedLookup returns a Slack lookup result from the cache, or from Slack if the cached value is missing or no
// longer fresh. A stale cached value is served instead of calling Slack while the method is backing off after a
// 429, and instead of an error if the call fails with a rate limit or transient error, or because the context
// deadline passed (e.g. a caller's lookup timeout during a slow Slack response). It is not served when the context
// is cancelled.
//
// The returned notFound flag reports whether Slack reported that the item does not exist.
func cachedLookup[T any](ctx context.Context, c *SlackAPIClient, logger types.Logger, p lookupPolicy, fetch lookupFetchFunc[T]) (T, bool, error) {
	key := lookupCacheKeyVersion + p.key

	stale, hasStale := getLookupCacheEntry[T](ctx, c, logger, key)

	if hasStale && time.Now().Before(stale.FreshUntil) {
		c.metrics.CounterInc(slackClientCacheRequestsTotalMetric, p.action, "hit")
		return stale.Value, stale.NotFound, nil
	}

	if hasStale && c.backoff != nil && time.Now().Before(c.backoff.blockedUntil(ctx, p.action)) {
		c.metrics.CounterInc(slackClientCacheRequestsTotalMetric, p.action, "stale")
		return stale.Value, stale.NotFound, nil
	}

	value, notFound, err := fetch(ctx, hasStale)
	if err != nil {
		if hasStale && serveStaleOnError(ctx, err) {
			c.metrics.CounterInc(slackClientCacheRequestsTotalMetric, p.action, "stale")
			logger.WithField("action", p.action).WithField("error", err).Info("Slack lookup failed, serving stale cached value")

			return stale.Value, stale.NotFound, nil
		}

		c.metrics.CounterInc(slackClientCacheRequestsTotalMetric, p.action, "miss")

		var zero T

		return zero, false, err
	}

	c.metrics.CounterInc(slackClientCacheRequestsTotalMetric, p.action, "miss")

	ttl := p.ttl
	if notFound {
		ttl = p.notFoundTTL
	} else if p.jitter > 0 {
		ttl += time.Duration(rand.N(int64(p.jitter))) // #nosec G404 -- weak random is acceptable for cache expiration jitter
	}

	entry := lookupCacheEntry[T]{
		FreshUntil: time.Now().Add(ttl),
		NotFound:   notFound,
		Value:      value,
	}

	if data, err := json.Marshal(entry); err != nil {
		logger.WithField("action", p.action).Errorf("Failed to json marshal cached Slack lookup: %s", err)
	} else {
		c.cache.Set(ctx, key, string(data), ttl+p.maxStale)
	}

	return value, notFound, nil
}

// serveStaleOnError reports whether a stale cached value should be served instead of the given fetch error.
func serveStaleOnError(ctx context.Context, err error) bool {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return true
	}

	return ctx.Err() == nil && (IsRateLimitError(err) || IsTransientError(err))
}

// getLookupCacheEntry reads and decodes a lookup cache entry. It returns false if the entry is missing or invalid.
func getLookupCacheEntry[T any](ctx context.Context, c *SlackAPIClient, logger types.Logger, key string) (*lookupCacheEntry[T], bool) {
	val, found := c.cache.Get(ctx, key)
	if !found {
		return nil, false
	}

	entry := &lookupCacheEntry[T]{}

	if err := json.Unmarshal([]byte(val), entry); err != nil {
		logger.WithField("cache_key", key).Errorf("Failed to json unmarshal cached Slack lookup: %s", err)
		return nil, false
	}

	return entry, true
}
