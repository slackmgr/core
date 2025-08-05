package manager

import (
	"context"
	"errors"
	"time"
)

// ErrChannelLockUnavailable is returned when a channel lock cannot be obtained.
var ErrChannelLockUnavailable = errors.New("channel lock unavailable")

// ChannelLocker is an interface for obtaining and releasing distributed locks on channels.
// It is used to ensure that only one process can perform operations on a channel at a time.
// This is particularly useful in a distributed environment where multiple instances of the manager may be running.
// Use the RedisChannelLocker implementation for a production-ready solution, using Redis as the backing store.
// Use the NoopChannelLocker for testing or when locking is not required (i.e in a single instance setup).
type ChannelLocker interface {
	Obtain(ctx context.Context, key string, ttl time.Duration, maxWait time.Duration) (ChannelLock, error)
}

type ChannelLock interface {
	Key() string
	Release(ctx context.Context) error
}
