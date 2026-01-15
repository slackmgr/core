package internal_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/peteraglen/slack-manager/internal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrySend(t *testing.T) {
	t.Parallel()

	sinkCh := make(chan int, 1)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	err1 := internal.TrySend(ctx, 42, sinkCh)
	var err2 error
	go func() {
		err2 = internal.TrySend(ctx, 43, sinkCh)
		wg.Done()
	}()
	cancel()
	wg.Wait()
	require.NoError(t, err1)
	assert.ErrorIs(t, err2, context.Canceled)
}

func TestIsCtxCanceledErr(t *testing.T) {
	t.Parallel()

	// Direct context.Canceled error
	err := context.Canceled
	assert.True(t, internal.IsCtxCanceledErr(err))

	// Different context error
	err = context.DeadlineExceeded
	assert.False(t, internal.IsCtxCanceledErr(err))

	// Wrapped context.Canceled error
	err = fmt.Errorf("some error: %w", context.Canceled)
	assert.True(t, internal.IsCtxCanceledErr(err))

	// Unrelated error with similar text should NOT match (was a bug before)
	err = errors.New("context canceled")
	assert.False(t, internal.IsCtxCanceledErr(err))

	// Deeply wrapped context.Canceled
	err = fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", context.Canceled))
	assert.True(t, internal.IsCtxCanceledErr(err))
}

func TestHash(t *testing.T) {
	t.Parallel()

	t.Run("empty input returns consistent hash", func(t *testing.T) {
		t.Parallel()

		hash1 := internal.Hash()
		hash2 := internal.Hash()
		assert.Equal(t, hash1, hash2)
		assert.NotEmpty(t, hash1)
	})

	t.Run("single string input", func(t *testing.T) {
		t.Parallel()

		hash := internal.Hash("hello")
		assert.NotEmpty(t, hash)
		// SHA256 of "hello" base64 encoded should be consistent
		assert.Equal(t, internal.Hash("hello"), hash)
	})

	t.Run("multiple strings are concatenated", func(t *testing.T) {
		t.Parallel()

		hash1 := internal.Hash("hello", "world")
		hash2 := internal.Hash("helloworld")
		// These should be the same since strings are just concatenated
		assert.Equal(t, hash1, hash2)
	})

	t.Run("different inputs produce different hashes", func(t *testing.T) {
		t.Parallel()

		hash1 := internal.Hash("foo")
		hash2 := internal.Hash("bar")
		assert.NotEqual(t, hash1, hash2)
	})

	t.Run("hash is URL-safe base64", func(t *testing.T) {
		t.Parallel()

		hash := internal.Hash("test")
		// URL-safe base64 should not contain + or /
		assert.NotContains(t, hash, "+")
		assert.NotContains(t, hash, "/")
	})
}

func TestHashBytes(t *testing.T) {
	t.Parallel()

	t.Run("returns 32 bytes for SHA256", func(t *testing.T) {
		t.Parallel()

		result := internal.HashBytes([]byte("hello"))
		assert.Len(t, result, 32) // SHA256 produces 32 bytes
	})

	t.Run("same input produces same hash", func(t *testing.T) {
		t.Parallel()

		result1 := internal.HashBytes([]byte("test"))
		result2 := internal.HashBytes([]byte("test"))
		assert.Equal(t, result1, result2)
	})

	t.Run("different inputs produce different hashes", func(t *testing.T) {
		t.Parallel()

		result1 := internal.HashBytes([]byte("foo"))
		result2 := internal.HashBytes([]byte("bar"))
		assert.NotEqual(t, result1, result2)
	})

	t.Run("empty input produces valid hash", func(t *testing.T) {
		t.Parallel()

		result := internal.HashBytes([]byte{})
		assert.Len(t, result, 32)
	})
}
