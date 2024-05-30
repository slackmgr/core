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

	err := context.Canceled
	assert.True(t, internal.IsCtxCanceledErr(err))

	err = context.DeadlineExceeded
	assert.False(t, internal.IsCtxCanceledErr(err))

	err = errors.New("context canceled")
	assert.True(t, internal.IsCtxCanceledErr(err))

	err = fmt.Errorf("some error: %w", context.Canceled)
	assert.True(t, internal.IsCtxCanceledErr(err))
}
