package manager

import (
	"context"
	"errors"
	"time"
)

// ErrLockUnavailable is returned when a lock cannot be obtained.
var ErrLockUnavailable = errors.New("lock unavailable")

type Locker interface {
	Obtain(ctx context.Context, key string, ttl time.Duration, maxWait time.Duration) (Lock, error)
}

type Lock interface {
	Release(ctx context.Context) error
	Extend(ctx context.Context, ttl time.Duration) error
	TTL(ctx context.Context) (time.Duration, error)
}
