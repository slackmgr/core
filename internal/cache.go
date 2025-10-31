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

// Cache is a wrapper around a cache store that provides methods for getting, setting, and deleting cached items.
type Cache struct {
	cache        cache.CacheInterface[string]
	keyPrefix    string
	logger       common.Logger
	rng          *rand.Rand
	panicOnError bool // If true, panics on cache errors instead of logging them
}

// NewCache creates a new Cache instance with the provided cache store, key prefix, and logger.
// The key prefix is optional, but may be useful for namespacing cache keys.
func NewCache(cacheStore store.StoreInterface, keyPrefix string, logger common.Logger) *Cache {
	return &Cache{
		cache:     cache.New[string](cacheStore),
		keyPrefix: keyPrefix,
		logger:    logger,
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())), // #nosec
	}
}

// WithPanicOnError enables panicking on cache errors instead of logging them, in all Get and Set methods.
// This is useful for testing and debugging purposes, but should be used with caution in production environments.
// The default behavior is to log errors, not to panic.
func (c *Cache) WithPanicOnError() *Cache {
	c.panicOnError = true
	return c
}

// Get retrieves an item from the cache with the given key.
// If the item is not found, it returns an empty string and false.
// If an error occurs during retrieval, it logs the error and returns an empty string and false (or panics if WithPanicOnError is set).
// Note: The key is prefixed with the cache's keyPrefix.
func (c *Cache) Get(ctx context.Context, key string) (string, bool) {
	key = c.keyPrefix + key

	value, err := c.cache.Get(ctx, key)
	if err != nil {
		if err.Error() == store.NOT_FOUND_ERR {
			return "", false
		}

		if c.panicOnError {
			panic(fmt.Sprintf("Cache read failed: %s", err))
		}

		c.logger.Errorf("Cache read failed: %s", err)

		return "", false
	}

	return value, true
}

// Set stores an item in the cache with the given key and expiration duration.
// If an error occurs during storage, it logs the error (or panics if WithPanicOnError is set).
// Note: The key is prefixed with the cache's keyPrefix.
func (c *Cache) Set(ctx context.Context, key string, value string, expiration time.Duration) {
	key = c.keyPrefix + key

	if err := c.cache.Set(ctx, key, value, store.WithExpiration(expiration)); err != nil {
		if c.panicOnError {
			panic(fmt.Sprintf("Cache write failed: %s", err))
		}

		c.logger.Errorf("Cache write failed: %s", err)
	}
}

// SetWithRandomExpiration stores an item in the cache with a key, value, and a random expiration time.
// The expiration time is calculated by adding a random variation to a minimum expiration duration.
// The random variation is between 0 and the absolute value of the provided variation duration.
// If an error occurs during storage, it logs the error (or panics if WithPanicOnError is set).
// Note: The key is prefixed with the cache's keyPrefix.
func (c *Cache) SetWithRandomExpiration(ctx context.Context, key string, value string, minExpiration time.Duration, variation time.Duration) {
	key = c.keyPrefix + key

	variationSeconds := int(math.Abs(variation.Seconds()))
	expiration := minExpiration

	if variationSeconds > 0 {
		extra := c.rng.Intn(variationSeconds) // #nosec
		expiration += (time.Duration(extra) * time.Second)
	}

	if err := c.cache.Set(ctx, key, value, store.WithExpiration(expiration)); err != nil {
		if c.panicOnError {
			panic(fmt.Sprintf("Cache write failed: %s", err))
		}

		c.logger.Errorf("Cache write failed: %s", err)
	}
}

// Delete removes the item from the cache with the given key.
// This method returns an error if the deletion fails, since an explicit delete is expected to always succeed.
// Note: The key is prefixed with the cache's keyPrefix.
func (c *Cache) Delete(ctx context.Context, key string) error {
	key = c.keyPrefix + key

	if err := c.cache.Delete(ctx, key); err != nil {
		return fmt.Errorf("cache delete failed: %w", err)
	}

	return nil
}
