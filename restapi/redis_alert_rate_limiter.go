package restapi

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/slackmgr/core/config"
)

// takeTokensScript is an atomic token bucket for several channels at once: either the tokens of all
// keys are taken, or none. Each bucket is stored as a hash with the token count ("tokens", may be
// fractional) and the time of the last update in microseconds ("ts"). A missing key is a full bucket.
// The Redis server time is used, so clock skew between API instances does not matter. A key expires
// when its bucket is full again, so idle channels use no memory.
//
// KEYS: the buckets. ARGV: rate (tokens per second), burst, then the count of each key.
// Returns: {allowed (0/1), then per key: remaining whole tokens, retry after (µs, 0 = has enough
// tokens, -1 = never), reset (µs)}.
//
//nolint:gochecknoglobals
var takeTokensScript = redis.NewScript(`
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])

local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])

local tokens = {}
local stamps = {}
local allowed = 1

for i, key in ipairs(KEYS) do
  local data = redis.call('HMGET', key, 'tokens', 'ts')
  local tk = tonumber(data[1])
  local ts = tonumber(data[2])

  if tk == nil or ts == nil then
    tk = burst
    ts = now
  end

  if now > ts then
    tk = math.min(burst, tk + (now - ts) * rate / 1000000)
    ts = now
  end

  tokens[i] = tk
  stamps[i] = ts

  -- Denied if any key has too few tokens.
  allowed = (tonumber(ARGV[i + 2]) > tk) and 0 or allowed
end

local result = {allowed}

for i, key in ipairs(KEYS) do
  local count = tonumber(ARGV[i + 2])
  local tk = tokens[i]
  local retry = 0

  if allowed == 1 then
    tk = tk - count
  elseif count > burst then
    retry = -1
  elseif count > tk then
    retry = math.ceil((count - tk) * 1000000 / rate)
  end

  local reset = 0
  if tk < burst then
    reset = math.ceil((burst - tk) * 1000000 / rate)
  end

  if allowed == 1 then
    redis.call('HSET', key, 'tokens', tk, 'ts', stamps[i])
    redis.call('PEXPIRE', key, math.max(1, math.ceil(reset / 1000)))
  end

  table.insert(result, math.floor(tk))
  table.insert(result, retry)
  table.insert(result, reset)
end

return result
`)

// RedisAlertRateLimiter is an [AlertRateLimiter] backed by Redis. All API instances using the same
// Redis and key prefix share the token bucket of each channel, so the configured rate limit is the
// total cap per channel, regardless of the number of instances.
//
// Each check is a single atomic Lua script call for all channels of a request (one round trip per
// request). The Redis server time is used, so clock skew between instances does not matter. The keys
// share the hash tag {alert-rate-limit}, so that the script can access the keys of several channels
// on Redis Cluster; all buckets are therefore stored on the same cluster node. Requires Redis 5 or
// later (or a compatible server, such as Valkey).
type RedisAlertRateLimiter struct {
	client    redis.UniversalClient
	keyPrefix string
}

// NewRedisAlertRateLimiter creates a [RedisAlertRateLimiter]. The bucket of a channel is stored under
// the key <keyPrefix>{alert-rate-limit}:<channel>. An empty keyPrefix defaults to [config.DefaultKeyPrefix].
func NewRedisAlertRateLimiter(client redis.UniversalClient, keyPrefix string) *RedisAlertRateLimiter {
	if keyPrefix == "" {
		keyPrefix = config.DefaultKeyPrefix
	}

	return &RedisAlertRateLimiter{
		client:    client,
		keyPrefix: keyPrefix,
	}
}

// TakeAll implements [AlertRateLimiter]. It returns an error if Redis is unavailable or returns an
// unexpected reply.
func (r *RedisAlertRateLimiter) TakeAll(ctx context.Context, counts map[string]int, limit config.RateLimitConfig) (map[string]RateLimitResult, error) {
	if len(counts) == 0 {
		return map[string]RateLimitResult{}, nil
	}

	channels := slices.Sorted(maps.Keys(counts))
	keys := make([]string, len(channels))
	args := make([]any, 0, len(channels)+2)
	args = append(args, limit.AlertsPerSecond, limit.AllowedBurst)

	for i, channel := range channels {
		keys[i] = r.key(channel)
		args = append(args, counts[channel])
	}

	vals, err := takeTokensScript.Run(ctx, r.client, keys, args...).Int64Slice()
	if err != nil {
		return nil, fmt.Errorf("redis alert rate limiter: %w", err)
	}

	if len(vals) != 1+3*len(channels) {
		return nil, fmt.Errorf("redis alert rate limiter: unexpected reply length %d", len(vals))
	}

	allowed := vals[0] == 1
	results := make(map[string]RateLimitResult, len(channels))

	for i, channel := range channels {
		remaining, retry, reset := vals[1+3*i], vals[2+3*i], vals[3+3*i]

		result := RateLimitResult{
			Allowed:   allowed,
			Remaining: max(int(remaining), 0),
			Reset:     time.Duration(reset) * time.Microsecond,
		}

		if !allowed {
			if retry < 0 {
				result.RetryAfter = maxRetryAfterDelay
			} else {
				result.RetryAfter = time.Duration(retry) * time.Microsecond
			}
		}

		results[channel] = result
	}

	return results, nil
}

func (r *RedisAlertRateLimiter) key(channel string) string {
	return r.keyPrefix + "{alert-rate-limit}:" + channel
}
