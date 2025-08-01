package internal

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	common "github.com/peteraglen/slack-manager-common"
)

type Cache[T any] struct {
	cache     cache.CacheInterface[T]
	keyPrefix string
	logger    common.Logger
	rng       *rand.Rand
}

// NewCache creates a new Cache instance with the provided cache store, key prefix, and logger.
// The key prefix is optional, but may be useful for namespacing cache keys.
func NewCache[T any](cacheStore store.StoreInterface, keyPrefix string, logger common.Logger) *Cache[T] {
	return &Cache[T]{
		cache:     cache.New[T](cacheStore),
		keyPrefix: keyPrefix,
		logger:    logger,
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())), // #nosec
	}
}

// Get retrieves an item from the cache with the given key.
// If the item is not found, it returns a zero value of type T and false.
// If an error occurs during retrieval, it logs the error and returns a zero value of type T and false.
// Note: The key is prefixed with the cache's keyPrefix.
func (c *Cache[T]) Get(ctx context.Context, key string) (T, bool) {
	key = c.keyPrefix + key

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

// Set stores an item in the cache with the given key and expiration duration.
// If an error occurs during storage, it logs the error.
// Note: The key is prefixed with the cache's keyPrefix.
func (c *Cache[T]) Set(ctx context.Context, key string, value T, expiration time.Duration) {
	key = c.keyPrefix + key

	if err := c.cache.Set(ctx, key, value, store.WithExpiration(expiration)); err != nil {
		c.logger.Errorf("Cache write failed: %s", err)
	}
}

// SetWithRandomExpiration stores an item in the cache with a key, value, and a random expiration time.
// The expiration time is calculated by adding a random variation to a minimum expiration duration.
// The random variation is between 0 and the absolute value of the provided variation duration.
// If an error occurs during storage, it logs the error.
// Note: The key is prefixed with the cache's keyPrefix.
func (c *Cache[T]) SetWithRandomExpiration(ctx context.Context, key string, value T, minExpiration time.Duration, variation time.Duration) {
	key = c.keyPrefix + key

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

// Delete removes the item from the cache with the given key.
// This method returns an error if the deletion fails, since an explicit delete is expected to succeed.
// Note: The key is prefixed with the cache's keyPrefix.
func (c *Cache[T]) Delete(ctx context.Context, key string) error {
	key = c.keyPrefix + key

	if err := c.cache.Delete(ctx, key); err != nil {
		return fmt.Errorf("cache delete failed: %w", err)
	}

	return nil
}
