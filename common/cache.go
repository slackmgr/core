package common

import (
	"context"
	"math"
	"math/rand"
	"time"

	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
)

type Cache[T any] struct {
	cache  cache.CacheInterface[T]
	logger Logger
	rng    *rand.Rand
}

func NewCache[T any](cache cache.CacheInterface[T], logger Logger) *Cache[T] {
	return &Cache[T]{
		cache:  cache,
		logger: logger,
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())), // #nosec
	}
}

func (c *Cache[T]) Get(ctx context.Context, key string) (T, bool) {
	value, err := c.cache.Get(ctx, key)
	if err != nil {
		if err.Error() == store.NOT_FOUND_ERR {
			return *new(T), false
		}

		c.logger.Errorf("Cache read failed: %s", err)

		return *new(T), false
	}

	return value, true
}

func (c *Cache[T]) Set(ctx context.Context, key string, value T, expiration time.Duration) {
	if err := c.cache.Set(ctx, key, value, store.WithExpiration(expiration)); err != nil {
		c.logger.Errorf("Cache write failed: %s", err)
	}
}

func (c *Cache[T]) SetWithRandomExpiration(ctx context.Context, key string, value T, minExpiration time.Duration, variation time.Duration) {
	variationSeconds := int(math.Abs(variation.Seconds()))
	expiration := minExpiration

	if variationSeconds > 0 {
		extra := c.rng.Intn(variationSeconds) // #nosec
		expiration += (time.Duration(extra) * time.Second)
	}

	if err := c.cache.Set(ctx, key, value, store.WithExpiration(expiration)); err != nil {
		c.logger.Errorf("Cache write failed: %s", err)
	}
}
