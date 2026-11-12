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
	// Take attempts to take count tokens from the token bucket of the given channel, using the
	// rate and burst of limit. Either all count tokens are taken, or none.
	//
	// An error means the limit could not be checked. The [Server] then allows the alerts (fails open).
	Take(ctx context.Context, channel string, count int, limit config.RateLimitConfig) (RateLimitResult, error)
}

// RateLimitResult is the outcome of an [AlertRateLimiter.Take] call.
type RateLimitResult struct {
	// Allowed is true if the tokens were taken.
	Allowed bool

	// Remaining is the number of whole tokens left in the bucket after the call.
	Remaining int

	// RetryAfter is how long to wait until count tokens are available. It is only set when
	// Allowed is false. It is capped at 24 hours when count tokens can never be available
	// (count is larger than the burst).
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

// Take implements [AlertRateLimiter]. It never returns an error.
func (l *LocalAlertRateLimiter) Take(_ context.Context, channel string, count int, limit config.RateLimitConfig) (RateLimitResult, error) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	lim, ok := l.limiters[channel]
	if !ok {
		lim = rate.NewLimiter(rate.Limit(limit.AlertsPerSecond), limit.AllowedBurst)
		l.limiters[channel] = lim
	}

	r := lim.ReserveN(now, count)
	delay := r.DelayFrom(now)

	if !r.OK() || delay > 0 {
		r.CancelAt(now)

		if !r.OK() {
			delay = maxRetryAfterDelay
		}

		return newLocalResult(lim, now, false, delay), nil
	}

	return newLocalResult(lim, now, true, 0), nil
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
