package manager

import (
	"context"
	"time"
)

// NoopChannelLocker is a no-op implementation of the ChannelLocker interface.
// It does not perform any locking and is used for testing or when locking is not required.
type NoopChannelLocker struct{}

// ChannelLock is a no-op implementation of the ChannelLock interface.
type NoopChannelLock struct {
	key string
}

// Obtain returns a no-op channel lock that does nothing.
func (n *NoopChannelLocker) Obtain(_ context.Context, key string, _ time.Duration, _ time.Duration) (ChannelLock, error) { //nolint:ireturn
	return &NoopChannelLock{key: key}, nil
}

// Key returns the key associated with the no-op channel lock.
func (n *NoopChannelLock) Key() string {
	return n.key
}

// Release is a no-op method that does nothing.
func (n *NoopChannelLock) Release(_ context.Context) error {
	return nil
}
