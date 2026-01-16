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
	// Obtain tries to obtain a lock for the given key (channel ID) with the specified TTL (time-to-live).
	// It will retry obtaining the lock until the maxWait duration is reached.
	// If the lock is successfully obtained, it returns a ChannelLock instance.
	// If the lock cannot be obtained within the maxWait duration, it returns ErrChannelLockUnavailable.
	Obtain(ctx context.Context, key string, ttl time.Duration, maxWait time.Duration) (ChannelLock, error)
}

// ChannelLock represents a single lock on a channel.
type ChannelLock interface {
	// Key returns the key associated with this lock.
	Key() string

	// Release releases the lock.
	// It does not accept a context parameter because releasing a lock is a commitment
	// that must complete regardless of the caller's context state. Each implementation
	// is responsible for managing its own timeouts internally.
	Release() error
}
