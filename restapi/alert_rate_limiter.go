package restapi

import (
	"context"
	"sync"
	"time"

	"github.com/slackmgr/core/config"
	"golang.org/x/time/rate"
)

// localLimiterSweepInterval is how often [LocalAlertRateLimiter] removes the limiters of idle channels.
const localLimiterSweepInterval = time.Minute

// AlertRateLimiter enforces the per-channel alert rate limit (APIConfig.RateLimitPerAlertChannel)
// of the [Server]. Each channel has its own token bucket.
//
// The [Server] uses a [LocalAlertRateLimiter] by default, which limits each API instance separately.
// Use [RedisAlertRateLimiter] (via [Server.WithAlertRateLimiter]) to share the limit between all
// API instances, so that the configured rate is the total cap per channel.
type AlertRateLimiter interface {
	// TakeAll attempts to take tokens from the token buckets of all channels of a request at once,
	// using the rate and burst of limit. counts maps each channel to its number of tokens. Either the
	// tokens of all channels are taken, or none, so that a request is accepted or rejected as a whole.
	//
	// The result has an entry for each channel in counts. Allowed is the same for all entries: true if
	// the tokens were taken. When it is false, RetryAfter is set for the channels with too few tokens,
	// and zero for the others.
	//
	// An error means the limit could not be checked. The [Server] then allows the alerts (fails open).
	TakeAll(ctx context.Context, counts map[string]int, limit config.RateLimitConfig) (map[string]RateLimitResult, error)
}

// RateLimitResult is the outcome of an [AlertRateLimiter.TakeAll] call for one channel.
type RateLimitResult struct {
	// Allowed is true if the tokens were taken (for all channels of the call).
	Allowed bool

	// Remaining is the number of whole tokens left in the bucket after the call.
	Remaining int

	// RetryAfter is how long to wait until the channel has enough tokens. It is only set when
	// Allowed is false, and the channel has too few tokens. It is capped at 24 hours when the
	// tokens can never be available (the count is larger than the burst).
	RetryAfter time.Duration

	// Reset is how long until the bucket is full again.
	Reset time.Duration
}

// LocalAlertRateLimiter is an in-process [AlertRateLimiter]. Each API instance has its own buckets,
// so with N instances the effective limit per channel is up to N times the configured limit.
// Use [RedisAlertRateLimiter] for multi-instance deployments.
//
// Buckets of idle channels are removed once they are full again, so memory use is bounded by the
// number of recently active channels.
type LocalAlertRateLimiter struct {
	mu        sync.Mutex
	limiters  map[string]*rate.Limiter
	lastSweep time.Time
	now       func() time.Time
}

// NewLocalAlertRateLimiter creates a [LocalAlertRateLimiter].
func NewLocalAlertRateLimiter() *LocalAlertRateLimiter {
	return &LocalAlertRateLimiter{
		limiters: make(map[string]*rate.Limiter),
		now:      time.Now,
	}
}

// TakeAll implements [AlertRateLimiter]. It never returns an error.
func (l *LocalAlertRateLimiter) TakeAll(_ context.Context, counts map[string]int, limit config.RateLimitConfig) (map[string]RateLimitResult, error) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	reservations := make(map[string]*rate.Reservation, len(counts))
	retryAfter := make(map[string]time.Duration, len(counts))
	allowed := true

	for channel, count := range counts {
		lim, ok := l.limiters[channel]
		if !ok {
			lim = rate.NewLimiter(rate.Limit(limit.AlertsPerSecond), limit.AllowedBurst)
			l.limiters[channel] = lim
		}

		r := lim.ReserveN(now, count)
		reservations[channel] = r

		switch {
		case !r.OK():
			retryAfter[channel] = maxRetryAfterDelay
			allowed = false
		case r.DelayFrom(now) > 0:
			retryAfter[channel] = r.DelayFrom(now)
			allowed = false
		}
	}

	if !allowed {
		// Return the tokens of all channels, including those that had enough.
		for _, r := range reservations {
			r.CancelAt(now)
		}
	}

	results := make(map[string]RateLimitResult, len(counts))

	for channel := range counts {
		results[channel] = newLocalResult(l.limiters[channel], now, allowed, retryAfter[channel])
	}

	return results, nil
}

// sweep removes the limiters with a full bucket. A full bucket is identical to a new one, so
// removing it does not change the limit. Must be called with l.mu held.
func (l *LocalAlertRateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < localLimiterSweepInterval {
		return
	}

	l.lastSweep = now

	for channel, lim := range l.limiters {
		if lim.TokensAt(now) >= float64(lim.Burst()) {
			delete(l.limiters, channel)
		}
	}
}

// size returns the number of channels with a limiter (for tests).
func (l *LocalAlertRateLimiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.limiters)
}

func newLocalResult(lim *rate.Limiter, now time.Time, allowed bool, retryAfter time.Duration) RateLimitResult {
	tokens := lim.TokensAt(now)
	burst := float64(lim.Burst())

	var reset time.Duration
	if r := float64(lim.Limit()); r > 0 && tokens < burst {
		reset = time.Duration((burst - tokens) / r * float64(time.Second))
	}

	return RateLimitResult{
		Allowed:    allowed,
		Remaining:  max(int(tokens), 0),
		RetryAfter: retryAfter,
		Reset:      reset,
	}
}
