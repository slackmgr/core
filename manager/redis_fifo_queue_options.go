package manager

import (
	"errors"
	"time"
)

// RedisFifoQueueOption is a function that configures RedisFifoQueueOptions.
type RedisFifoQueueOption func(*RedisFifoQueueOptions)

// RedisFifoQueueOptions holds configuration for RedisFifoQueue.
type RedisFifoQueueOptions struct {
	// keyPrefix is the prefix for all Redis keys used by the queue.
	// Default: "slack-manager:queue"
	keyPrefix string

	// consumerGroup is the name of the Redis consumer group.
	// All instances should use the same consumer group to share the workload.
	// Default: "slack-manager"
	consumerGroup string

	// pollInterval is how long to wait between polling cycles when no messages are available.
	// Default: 2 seconds
	pollInterval time.Duration

	// maxStreamLength is the approximate maximum length of each stream.
	// Older messages are trimmed when this limit is exceeded.
	// Default: 10000
	maxStreamLength int64

	// streamRefreshInterval is how often to check for new streams.
	// Default: 30 seconds
	streamRefreshInterval time.Duration

	// claimMinIdleTime is the minimum time a message must be pending before it can be claimed.
	// This should be longer than the expected processing time for a message, but must be
	// less than lockTTL to ensure pending messages are claimable when the lock expires.
	// Default: 120 seconds
	claimMinIdleTime time.Duration

	// lockTTL is the time-to-live for the per-stream lock.
	// The lock ensures strict ordering by preventing other consumers from reading
	// from a stream while a message is being processed.
	// This should be longer than the expected processing time for a message.
	// Default: 140 seconds
	lockTTL time.Duration

	// streamInactivityTimeout is how long a stream can be inactive before it is cleaned up.
	// Inactive streams (no messages sent) are removed from the index and deleted from Redis.
	// Set to 0 to disable automatic cleanup.
	// Default: 48 hours
	streamInactivityTimeout time.Duration
}

func newRedisFifoQueueOptions() *RedisFifoQueueOptions {
	return &RedisFifoQueueOptions{
		keyPrefix:               "slack-manager:queue",
		consumerGroup:           "slack-manager",
		pollInterval:            2 * time.Second,
		maxStreamLength:         10000,
		streamRefreshInterval:   30 * time.Second,
		claimMinIdleTime:        120 * time.Second,
		lockTTL:                 140 * time.Second,
		streamInactivityTimeout: 48 * time.Hour,
	}
}

func (o *RedisFifoQueueOptions) validate() error {
	if o.keyPrefix == "" {
		return errors.New("key prefix cannot be empty")
	}

	if o.consumerGroup == "" {
		return errors.New("consumer group cannot be empty")
	}

	if o.pollInterval < time.Second || o.pollInterval > time.Minute {
		return errors.New("poll interval must be between 1 second and 1 minute")
	}

	if o.maxStreamLength < 100 {
		return errors.New("max stream length must be at least 100")
	}

	if o.streamRefreshInterval < 5*time.Second || o.streamRefreshInterval > 5*time.Minute {
		return errors.New("stream refresh interval must be between 5 seconds and 5 minutes")
	}

	if o.claimMinIdleTime < 10*time.Second || o.claimMinIdleTime > 10*time.Minute {
		return errors.New("claim min idle time must be between 10 seconds and 10 minutes")
	}

	if o.lockTTL < 30*time.Second || o.lockTTL > 30*time.Minute {
		return errors.New("lock TTL must be between 30 seconds and 30 minutes")
	}

	if o.claimMinIdleTime >= o.lockTTL {
		return errors.New("claim min idle time must be less than lock TTL to ensure strict ordering")
	}

	if o.streamInactivityTimeout != 0 && o.streamInactivityTimeout < time.Hour {
		return errors.New("stream inactivity timeout must be at least 1 hour (or 0 to disable)")
	}

	return nil
}

// WithKeyPrefix sets the Redis key prefix for the queue.
func WithKeyPrefix(prefix string) RedisFifoQueueOption {
	return func(o *RedisFifoQueueOptions) {
		o.keyPrefix = prefix
	}
}

// WithConsumerGroup sets the consumer group name.
func WithConsumerGroup(group string) RedisFifoQueueOption {
	return func(o *RedisFifoQueueOptions) {
		o.consumerGroup = group
	}
}

// WithPollInterval sets how long to wait between polling cycles when no messages are available.
func WithPollInterval(interval time.Duration) RedisFifoQueueOption {
	return func(o *RedisFifoQueueOptions) {
		o.pollInterval = interval
	}
}

// WithMaxStreamLength sets the approximate maximum length of each stream.
func WithMaxStreamLength(length int64) RedisFifoQueueOption {
	return func(o *RedisFifoQueueOptions) {
		o.maxStreamLength = length
	}
}

// WithStreamRefreshInterval sets how often to check for new streams.
func WithStreamRefreshInterval(interval time.Duration) RedisFifoQueueOption {
	return func(o *RedisFifoQueueOptions) {
		o.streamRefreshInterval = interval
	}
}

// WithClaimMinIdleTime sets the minimum idle time before a message can be claimed.
func WithClaimMinIdleTime(duration time.Duration) RedisFifoQueueOption {
	return func(o *RedisFifoQueueOptions) {
		o.claimMinIdleTime = duration
	}
}

// WithLockTTL sets the time-to-live for the per-stream lock.
func WithLockTTL(ttl time.Duration) RedisFifoQueueOption {
	return func(o *RedisFifoQueueOptions) {
		o.lockTTL = ttl
	}
}

// WithStreamInactivityTimeout sets how long a stream can be inactive before cleanup.
// Set to 0 to disable automatic cleanup.
func WithStreamInactivityTimeout(timeout time.Duration) RedisFifoQueueOption {
	return func(o *RedisFifoQueueOptions) {
		o.streamInactivityTimeout = timeout
	}
}
