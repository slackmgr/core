//go:build integration

package internal_test

import (
	"context"
	"testing"
	"time"

	redis_store "github.com/eko/gocache/store/rediscluster/v4"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/ksuid"
	"github.com/slackmgr/core/internal"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/require"
)

func TestCacheForStrings(t *testing.T) {
	ctx := context.Background()
	logger := &types.NoopLogger{}

	options := &redis.Options{
		Addr:     "localhost:6379",
		Username: "",
		Password: "",
		DB:       0,
	}

	redisClient := redis.NewClient(options)
	cacheStore := redis_store.NewRedisCluster(redisClient)
	cachePrefix := "__integration_test:" + ksuid.New().String() + ":"
	cache := internal.NewCache(cacheStore, cachePrefix, logger).WithPanicOnError()

	key := "exampleKey:" + ksuid.New().String()
	value := "exampleValue"
	expiration := 5 * time.Second

	cache.Set(ctx, key, value, expiration)
	retrievedValue, found := cache.Get(ctx, key)
	require.True(t, found, "Expected value to be found in cache")
	require.Equal(t, value, retrievedValue, "Expected retrieved value to match the set value")
}
