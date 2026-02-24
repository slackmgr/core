package manager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/slackmgr/types"
)

// signal Lua script: set key to unixMilli only if the new value is later than
// the existing one (or if the key does not exist).
//
//nolint:gochecknoglobals
var signalScript = redis.NewScript(`
local key = KEYS[1]
local newVal = tonumber(ARGV[1])
local ttl = tonumber(ARGV[2])
local cur = redis.call('GET', key)
if cur == false or tonumber(cur) < newVal then
    redis.call('SET', key, newVal, 'PX', ttl)
end
return 1
`)

// RedisRateLimitGate is a distributed RateLimitGate backed by Redis. It is
// suitable for multi-instance deployments where every instance must respect
// the same rate-limit window.
type RedisRateLimitGate struct {
	client       redis.UniversalClient
	key          string
	readyFn      func() bool
	maxDrainWait time.Duration
	logger       types.Logger
}

// NewRedisRateLimitGate creates a RedisRateLimitGate.
// Optional behaviour (key prefix, drain wait) is configured via RedisRateLimitGateOption.
func NewRedisRateLimitGate(client redis.UniversalClient, logger types.Logger, opts ...RedisRateLimitGateOption) *RedisRateLimitGate {
	o := newRedisRateLimitGateOptions()
	for _, opt := range opts {
		opt(o)
	}

	return &RedisRateLimitGate{
		client:       client,
		key:          o.keyPrefix + "rate-limit-gate",
		maxDrainWait: o.maxDrainWait,
		logger:       logger,
	}
}

func (g *RedisRateLimitGate) Signal(ctx context.Context, until time.Time) error {
	unixMilli := until.UnixMilli()
	ttlMs := time.Until(until).Milliseconds()

	if ttlMs <= 0 {
		return nil
	}

	if err := signalScript.Run(ctx, g.client, []string{g.key}, unixMilli, ttlMs).Err(); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("redis rate limit gate signal: %w", err)
	}

	return nil
}

func (g *RedisRateLimitGate) Wait(ctx context.Context) error {
	// Fast path: check if blocked at all.
	blocked, err := g.IsBlocked(ctx)
	if err != nil {
		// Fail-open: Redis error should not block channel managers.
		g.logger.WithField("error", err).Info("Rate limit gate: Redis error in IsBlocked, continuing (fail-open)")
		return nil
	}

	if !blocked {
		return nil
	}

	g.logger.Info("Rate limit gate: waiting for rate limit window to expire")

	// Phase 1: poll Redis every 500 ms until the key disappears.
	for {
		blocked, err = g.IsBlocked(ctx)
		if err != nil {
			g.logger.WithField("error", err).Info("Rate limit gate: Redis error during wait, continuing (fail-open)")
			break
		}

		if !blocked {
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}

	// Phase 2: wait for Socket Mode to be quiet (poll every 100 ms).
	if g.readyFn == nil {
		return nil
	}

	drainDeadline := time.Now().Add(g.maxDrainWait)

	for !g.readyFn() {
		if time.Now().After(drainDeadline) {
			g.logger.Info("Rate limit gate: Socket Mode drain wait exceeded, resuming (fail-open)")
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	return nil
}

func (g *RedisRateLimitGate) IsBlocked(ctx context.Context) (bool, error) {
	val, err := g.client.Get(ctx, g.key).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("redis rate limit gate check: %w", err)
	}

	// Parse the stored Unix millisecond timestamp.
	var unixMilli int64

	if _, err := fmt.Sscan(val, &unixMilli); err != nil {
		return false, fmt.Errorf("redis rate limit gate: invalid value %q: %w", val, err)
	}

	return time.Now().Before(time.UnixMilli(unixMilli)), nil
}

func (g *RedisRateLimitGate) SetReadyCheck(fn func() bool) {
	g.readyFn = fn
}
