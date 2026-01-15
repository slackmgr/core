package internal_test

import (
	"context"
	"io"
	"testing"
	"time"

	gocache_store "github.com/eko/gocache/store/go_cache/v4"
	gocache "github.com/patrickmn/go-cache"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockLogger struct{}

func (m *mockLogger) Debug(msg string)                               {}
func (m *mockLogger) Debugf(format string, args ...any)              {}
func (m *mockLogger) Info(msg string)                                {}
func (m *mockLogger) Infof(format string, args ...any)               {}
func (m *mockLogger) Error(msg string)                               {}
func (m *mockLogger) Errorf(format string, args ...any)              {}
func (m *mockLogger) WithField(key string, value any) common.Logger  { return m }
func (m *mockLogger) WithFields(fields map[string]any) common.Logger { return m }
func (m *mockLogger) HttpLoggingHandler() io.Writer                  { return io.Discard }

func newTestCache(keyPrefix string) *internal.Cache {
	gocacheClient := gocache.New(5*time.Minute, time.Minute)
	store := gocache_store.NewGoCache(gocacheClient)
	return internal.NewCache(store, keyPrefix, &mockLogger{})
}

func TestCache_GetSet(t *testing.T) {
	t.Parallel()

	t.Run("set and get value", func(t *testing.T) {
		t.Parallel()

		cache := newTestCache("test:")
		ctx := context.Background()

		cache.Set(ctx, "key1", "value1", time.Minute)

		value, found := cache.Get(ctx, "key1")
		require.True(t, found)
		assert.Equal(t, "value1", value)
	})

	t.Run("get non-existent key returns not found", func(t *testing.T) {
		t.Parallel()

		cache := newTestCache("test:")
		ctx := context.Background()

		value, found := cache.Get(ctx, "nonexistent")
		assert.False(t, found)
		assert.Empty(t, value)
	})

	t.Run("key prefix is applied", func(t *testing.T) {
		t.Parallel()

		cache1 := newTestCache("prefix1:")
		cache2 := newTestCache("prefix2:")
		ctx := context.Background()

		cache1.Set(ctx, "key", "value1", time.Minute)
		cache2.Set(ctx, "key", "value2", time.Minute)

		value1, found1 := cache1.Get(ctx, "key")
		value2, found2 := cache2.Get(ctx, "key")

		require.True(t, found1)
		require.True(t, found2)
		assert.Equal(t, "value1", value1)
		assert.Equal(t, "value2", value2)
	})
}

func TestCache_SetWithRandomExpiration(t *testing.T) {
	t.Parallel()

	t.Run("sets value with random expiration", func(t *testing.T) {
		t.Parallel()

		cache := newTestCache("test:")
		ctx := context.Background()

		cache.SetWithRandomExpiration(ctx, "key", "value", time.Minute, 30*time.Second)

		value, found := cache.Get(ctx, "key")
		require.True(t, found)
		assert.Equal(t, "value", value)
	})

	t.Run("zero variation uses min expiration", func(t *testing.T) {
		t.Parallel()

		cache := newTestCache("test:")
		ctx := context.Background()

		cache.SetWithRandomExpiration(ctx, "key", "value", time.Minute, 0)

		value, found := cache.Get(ctx, "key")
		require.True(t, found)
		assert.Equal(t, "value", value)
	})

	t.Run("negative variation is treated as absolute", func(t *testing.T) {
		t.Parallel()

		cache := newTestCache("test:")
		ctx := context.Background()

		// Should not panic with negative variation
		cache.SetWithRandomExpiration(ctx, "key", "value", time.Minute, -30*time.Second)

		value, found := cache.Get(ctx, "key")
		require.True(t, found)
		assert.Equal(t, "value", value)
	})
}

func TestCache_Delete(t *testing.T) {
	t.Parallel()

	t.Run("deletes existing key", func(t *testing.T) {
		t.Parallel()

		cache := newTestCache("test:")
		ctx := context.Background()

		cache.Set(ctx, "key", "value", time.Minute)

		err := cache.Delete(ctx, "key")
		require.NoError(t, err)

		_, found := cache.Get(ctx, "key")
		assert.False(t, found)
	})

	t.Run("delete non-existent key succeeds with go-cache", func(t *testing.T) {
		t.Parallel()

		cache := newTestCache("test:")
		ctx := context.Background()

		// Note: go-cache in-memory store does not return error for non-existent keys.
		// Other stores (like Redis) may behave differently.
		err := cache.Delete(ctx, "nonexistent")
		assert.NoError(t, err)
	})
}

func TestCache_WithPanicOnError(t *testing.T) {
	t.Parallel()

	t.Run("returns same cache instance", func(t *testing.T) {
		t.Parallel()

		cache := newTestCache("test:")
		result := cache.WithPanicOnError()

		assert.Same(t, cache, result)
	})
}

func TestCache_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	cache := newTestCache("concurrent:")
	ctx := context.Background()

	// Test that SetWithRandomExpiration is thread-safe (uses rand/v2)
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				key := "key"
				cache.SetWithRandomExpiration(ctx, key, "value", time.Minute, 30*time.Second)
				cache.Get(ctx, key)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}
