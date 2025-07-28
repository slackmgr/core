package manager

import (
	"context"
	"errors"
	"time"
)

// ErrChannelLockUnavailable is returned when a channel lock cannot be obtained.
var ErrChannelLockUnavailable = errors.New("channel lock unavailable")

type ChannelLocker interface {
	Obtain(ctx context.Context, key string, ttl time.Duration, maxWait time.Duration) (ChannelLock, error)
}

type ChannelLock interface {
	Release(ctx context.Context) error
	// Extend(ctx context.Context, ttl time.Duration) error
	// TTL(ctx context.Context) (time.Duration, error)
}
