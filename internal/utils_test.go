package internal

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTrySend(t *testing.T) {
	sinkCh := make(chan int, 1)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	err1 := TrySend(ctx, 42, sinkCh)
	var err2 error
	go func() {
		err2 = TrySend(ctx, 43, sinkCh)
		wg.Done()
	}()
	cancel()
	wg.Wait()
	assert.NoError(t, err1)
	assert.ErrorIs(t, err2, context.Canceled)
}

func TestIsCtxCanceledErr(t *testing.T) {
	err := context.Canceled
	assert.True(t, IsCtxCanceledErr(err))

	err = context.DeadlineExceeded
	assert.False(t, IsCtxCanceledErr(err))

	err = fmt.Errorf("context canceled")
	assert.True(t, IsCtxCanceledErr(err))

	err = fmt.Errorf("some error: %w", context.Canceled)
	assert.True(t, IsCtxCanceledErr(err))
}
