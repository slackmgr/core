package internal

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	cachestore "github.com/eko/gocache/lib/v4/store"
	"github.com/slack-go/slack"
	"github.com/slackmgr/types"
)

// Rate limit groups. A Slack 429 is handled differently depending on the group of the rate limited method.
const (
	// RateLimitGroupWrite is the group of Slack methods that post, update or delete messages (chat.postMessage,
	// chat.update and chat.delete). A 429 on a write method signals the global rate limit gate (see
	// [WithRateLimitGate]), which pauses all channel processing until the Retry-After window has passed.
	RateLimitGroupWrite = "write"

	// RateLimitGroupOther is the group of all other Slack methods: lookups such as conversations.info,
	// conversations.members, users.info, usergroups.users.list and conversations.history, and interactive
	// replies such as chat.postEphemeral, chat.responseURL and views.update. A 429 on one of these methods only
	// backs off that method: later calls to it wait for (or fail fast on) the method's own deadline, and cached
	// lookups serve a stale value instead. Channel processing is not paused.
	RateLimitGroupOther = "other"
)

const (
	// slackAPIRateLimitedTotalMetric counts Slack 429 responses, labelled by action and rate limit group.
	slackAPIRateLimitedTotalMetric = "slack_api_rate_limited_total"

	// slackAPIBackoffTotalMetric counts calls to a method that found an active per-method backoff before
	// calling Slack, labelled by action and outcome ("waited" = waited for the deadline, "rejected" = failed fast).
	slackAPIBackoffTotalMetric = "slack_api_backoff_total"

	// rateLimitBackoffKeyPrefix is the cache key prefix for per-method rate limit deadlines. The full key is
	// <cacheKeyPrefix>rate-limit-gate:<method>.
	rateLimitBackoffKeyPrefix = "rate-limit-gate:"
)

// ErrRateLimitBackoff is returned (wrapped) by [SlackAPIClient] methods when a call was not made, because the
// method is backing off after a Slack 429, and the backoff does not end within the context deadline or the
// rate limit retry budget.
var ErrRateLimitBackoff = errors.New("slack method is backing off after a rate limit error")

// writeMethods are the actions in [RateLimitGroupWrite]. The action names are the slack_action metric label values.
//
//nolint:gochecknoglobals
var writeMethods = map[string]struct{}{
	"chat.post":   {},
	"chat.update": {},
	"chat.delete": {},
}

// RateLimitGroup returns the rate limit group ([RateLimitGroupWrite] or [RateLimitGroupOther]) of a Slack
// API action, as used in the slack_action metric label.
func RateLimitGroup(action string) string {
	if _, ok := writeMethods[action]; ok {
		return RateLimitGroupWrite
	}

	return RateLimitGroupOther
}

// IsRateLimitError reports whether err is (or wraps) a Slack 429 error, or an [ErrRateLimitBackoff] error.
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	if _, ok := errors.AsType[*slack.RateLimitedError](err); ok {
		return true
	}

	return errors.Is(err, ErrRateLimitBackoff)
}

// methodBackoff stores per-method Slack rate limit deadlines in the cache store, with the key
// <cacheKeyPrefix>rate-limit-gate:<method>. With a shared cache store (e.g. Redis), the deadlines are shared by all
// API and Manager instances using the same key prefix. With an in-process store they are local to the instance.
//
// Signal extends a deadline with a read followed by a write, which is not atomic across instances. A concurrent
// signal may therefore occasionally shorten a deadline slightly, which only causes an extra 429.
type methodBackoff struct {
	cache  *Cache
	logger types.Logger
}

// newMethodBackoff creates a [methodBackoff] using the given cache store and key prefix.
func newMethodBackoff(cacheStore cachestore.StoreInterface, cacheKeyPrefix string, logger types.Logger) *methodBackoff {
	return &methodBackoff{
		cache:  NewCache(cacheStore, cacheKeyPrefix+rateLimitBackoffKeyPrefix, logger),
		logger: logger,
	}
}

// signal records that method is rate limited until the given time. An existing later deadline is kept.
func (b *methodBackoff) signal(ctx context.Context, method string, until time.Time) {
	ttl := time.Until(until)
	if ttl <= 0 {
		return
	}

	if current := b.blockedUntil(ctx, method); !current.Before(until) {
		return
	}

	b.cache.Set(ctx, method, strconv.FormatInt(until.UnixMilli(), 10), ttl)
}

// blockedUntil returns the rate limit deadline of method, or the zero time if the method is not backing off.
// Cache errors and invalid values are treated as no backoff (fail-open).
func (b *methodBackoff) blockedUntil(ctx context.Context, method string) time.Time {
	val, found := b.cache.Get(ctx, method)
	if !found {
		return time.Time{}
	}

	unixMilli, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		b.logger.WithField("action", method).Errorf("Invalid rate limit backoff value %q: %s", val, err)
		return time.Time{}
	}

	return time.UnixMilli(unixMilli)
}

// awaitMethodBackoff checks whether a non-write method is backing off after a 429 before it is called. If so, it waits
// until the deadline has passed, or returns an error wrapping [ErrRateLimitBackoff] without waiting if failFast is
// true, or if the deadline is beyond the context deadline or the remaining rate limit retry budget.
// Write methods are never checked here: they are paused by the global rate limit gate before a channel is processed,
// and waiting in the middle of processing a channel would hold the channel lock.
func (c *SlackAPIClient) awaitMethodBackoff(ctx context.Context, logger types.Logger, action string, budgetEnd time.Time, failFast bool) error {
	if c.backoff == nil || RateLimitGroup(action) == RateLimitGroupWrite {
		return nil
	}

	until := c.backoff.blockedUntil(ctx, action)

	remaining := time.Until(until)
	if remaining <= 0 {
		return nil
	}

	wait := remaining + jitter(500*time.Millisecond)
	waitEnd := time.Now().Add(wait)
	deadline, hasDeadline := ctx.Deadline()

	if failFast || waitEnd.After(budgetEnd) || (hasDeadline && waitEnd.After(deadline)) {
		c.metrics.CounterInc(slackAPIBackoffTotalMetric, action, "rejected")
		return fmt.Errorf("%w: %s is rate limited for another %s", ErrRateLimitBackoff, action, remaining.Round(time.Millisecond))
	}

	c.metrics.CounterInc(slackAPIBackoffTotalMetric, action, "waited")
	logger.WithField("action", action).WithField("wait", wait).Info("Slack API method is rate limited, waiting before calling it")

	return sleep(ctx, wait)
}

// signalRateLimit records a Slack 429 for action: the per-method deadline is always recorded, and the global rate
// limit gate is signalled for write methods only.
func (c *SlackAPIClient) signalRateLimit(ctx context.Context, logger types.Logger, action string, retryAfter time.Duration) {
	group := RateLimitGroup(action)
	until := time.Now().Add(retryAfter + 2*time.Second)

	c.metrics.CounterInc(slackAPIRateLimitedTotalMetric, action, group)
	c.metrics.Observe(slackAPIRateLimitDurationMetric, retryAfter.Seconds(), action)

	if c.backoff != nil {
		c.backoff.signal(ctx, action, until)
	}

	if group == RateLimitGroupWrite && c.gate != nil {
		if err := c.gate.Signal(ctx, until); err != nil {
			logger.WithField("error", err).WithField("action", action).Info("Failed to signal rate limit gate")
		}
	}
}
