package restapi

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/slackmgr/core/config"
)

// takeTokensScript is an atomic token bucket. The bucket is stored as a hash with the token count
// ("tokens", may be fractional) and the time of the last update in microseconds ("ts"). A missing
// key is a full bucket. The Redis server time is used, so clock skew between API instances does not
// matter. The key expires when the bucket is full again, so idle channels use no memory.
//
// ARGV: rate (tokens per second), burst, count.
// Returns: {allowed (0/1), remaining whole tokens, retry after (µs, -1 = never), reset (µs)}.
//
//nolint:gochecknoglobals
var takeTokensScript = redis.NewScript(`
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local count = tonumber(ARGV[3])

local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])

local data = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(data[1])
local ts = tonumber(data[2])

if tokens == nil or ts == nil then
  tokens = burst
  ts = now
end

if now > ts then
  tokens = math.min(burst, tokens + (now - ts) * rate / 1000000)
  ts = now
end

local allowed = 0
local retry = 0

if count <= tokens then
  tokens = tokens - count
  allowed = 1
elseif count > burst then
  retry = -1
else
  retry = math.ceil((count - tokens) * 1000000 / rate)
end

local reset = 0
if tokens < burst then
  reset = math.ceil((burst - tokens) * 1000000 / rate)
end

if allowed == 1 then
  redis.call('HSET', KEYS[1], 'tokens', tokens, 'ts', ts)
  redis.call('PEXPIRE', KEYS[1], math.max(1, math.ceil(reset / 1000)))
end

return {allowed, math.floor(tokens), retry, reset}
`)

// RedisAlertRateLimiter is an [AlertRateLimiter] backed by Redis. All API instances using the same
// Redis and key prefix share the token bucket of each channel, so the configured rate limit is the
// total cap per channel, regardless of the number of instances.
//
// Each check is a single atomic Lua script call (one round trip per channel per request). The
// Redis server time is used, so clock skew between instances does not matter. Requires Redis 5 or later
// (or a compatible server, such as Valkey).
type RedisAlertRateLimiter struct {
	client    redis.UniversalClient
	keyPrefix string
}

// NewRedisAlertRateLimiter creates a [RedisAlertRateLimiter]. The bucket of a channel is stored under
// the key <keyPrefix>alert-rate-limit:<channel>. An empty keyPrefix defaults to [config.DefaultKeyPrefix].
func NewRedisAlertRateLimiter(client redis.UniversalClient, keyPrefix string) *RedisAlertRateLimiter {
	if keyPrefix == "" {
		keyPrefix = config.DefaultKeyPrefix
	}

	return &RedisAlertRateLimiter{
		client:    client,
		keyPrefix: keyPrefix,
	}
}

// Take implements [AlertRateLimiter]. It returns an error if Redis is unavailable or returns an
// unexpected reply.
func (r *RedisAlertRateLimiter) Take(ctx context.Context, channel string, count int, limit config.RateLimitConfig) (RateLimitResult, error) {
	vals, err := takeTokensScript.Run(ctx, r.client, []string{r.key(channel)}, limit.AlertsPerSecond, limit.AllowedBurst, count).Int64Slice()
	if err != nil {
		return RateLimitResult{}, fmt.Errorf("redis alert rate limiter: %w", err)
	}

	if len(vals) != 4 {
		return RateLimitResult{}, fmt.Errorf("redis alert rate limiter: unexpected reply length %d", len(vals))
	}

	result := RateLimitResult{
		Allowed:   vals[0] == 1,
		Remaining: max(int(vals[1]), 0),
		Reset:     time.Duration(vals[3]) * time.Microsecond,
	}

	if !result.Allowed {
		if vals[2] < 0 {
			result.RetryAfter = maxRetryAfterDelay
		} else {
			result.RetryAfter = time.Duration(vals[2]) * time.Microsecond
		}
	}

	return result, nil
}

func (r *RedisAlertRateLimiter) key(channel string) string {
	return r.keyPrefix + "alert-rate-limit:" + channel
}
