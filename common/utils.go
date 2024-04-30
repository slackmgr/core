package common

import (
	"context"
	"errors"
	"strings"
)

func TrySend[T any](ctx context.Context, msg T, sinkCh chan<- T) error {
	select {
	case sinkCh <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func IsCtxCanceledErr(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}

	if strings.HasSuffix(err.Error(), context.Canceled.Error()) {
		return true
	}

	return false
}
