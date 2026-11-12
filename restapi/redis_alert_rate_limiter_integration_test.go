//go:build integration

package restapi_test

import (
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/segmentio/ksuid"
	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/restapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newIntegrationRedisLimiters(t *testing.T) (*redis.Client, string, *restapi.RedisAlertRateLimiter, *restapi.RedisAlertRateLimiter) {
	t.Helper()

	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	t.Cleanup(func() { _ = client.Close() })

	prefix := "__integration_test:" + ksuid.New().String() + ":"

	// Two limiters with the same client and prefix act like two API instances.
	return client, prefix, restapi.NewRedisAlertRateLimiter(client, prefix), restapi.NewRedisAlertRateLimiter(client, prefix)
}

func TestRedisAlertRateLimiter_SharedBucket(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	_, _, a, b := newIntegrationRedisLimiters(t)
	limit := config.RateLimitConfig{AlertsPerSecond: 0.001, AllowedBurst: 10}

	res, err := a.Take(ctx, "C1", 6, limit)
	require.NoError(t, err)
	assert.True(t, res.Allowed)
	assert.Equal(t, 4, res.Remaining)

	// The second instance sees the tokens taken by the first.
	res, err = b.Take(ctx, "C1", 5, limit)
	require.NoError(t, err)
	assert.False(t, res.Allowed)
	assert.Equal(t, 4, res.Remaining)
	assert.InDelta(t, 1000*time.Second, res.RetryAfter, float64(time.Second))

	// The denied request took no tokens.
	res, err = b.Take(ctx, "C1", 4, limit)
	require.NoError(t, err)
	assert.True(t, res.Allowed)
	assert.Equal(t, 0, res.Remaining)

	// Other channels are independent.
	res, err = a.Take(ctx, "C2", 10, limit)
	require.NoError(t, err)
	assert.True(t, res.Allowed)
}

func TestRedisAlertRateLimiter_RefillAndExpiry(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	client, prefix, a, _ := newIntegrationRedisLimiters(t)
	limit := config.RateLimitConfig{AlertsPerSecond: 20, AllowedBurst: 10}

	res, err := a.Take(ctx, "C1", 10, limit)
	require.NoError(t, err)
	require.True(t, res.Allowed)
	assert.InDelta(t, 500*time.Millisecond, res.Reset, float64(5*time.Millisecond))

	// The key expires when the bucket is full again.
	ttl, err := client.PTTL(ctx, prefix+"alert-rate-limit:C1").Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0))
	assert.LessOrEqual(t, ttl, 500*time.Millisecond)

	res, err = a.Take(ctx, "C1", 5, limit)
	require.NoError(t, err)
	require.False(t, res.Allowed)

	time.Sleep(res.RetryAfter + 10*time.Millisecond)

	res, err = a.Take(ctx, "C1", 5, limit)
	require.NoError(t, err)
	assert.True(t, res.Allowed)
}

func TestRedisAlertRateLimiter_CountAboveBurst(t *testing.T) {
	t.Parallel()

	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	t.Cleanup(func() { _ = client.Close() })
	a := restapi.NewRedisAlertRateLimiter(client, "__integration_test:"+ksuid.New().String()+":")

	res, err := a.Take(t.Context(), "C1", 11, config.RateLimitConfig{AlertsPerSecond: 1, AllowedBurst: 10})
	require.NoError(t, err)
	assert.False(t, res.Allowed)
	assert.Equal(t, 10, res.Remaining)
	assert.Equal(t, 24*time.Hour, res.RetryAfter)
}
