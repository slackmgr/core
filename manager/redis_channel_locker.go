package manager

import (
	"context"
	"errors"
	"time"

	"github.com/bsm/redislock"
	redis "github.com/redis/go-redis/v9"
)

// lockReleaseTimeout is the timeout for lock release operations.
// Release uses context.Background() because it must complete regardless
// of the caller's context state - releasing a lock is a commitment.
// This should be shorter than the drain timeouts (default 3-5s) since this is a simple Redis DEL operation.
const lockReleaseTimeout = 2 * time.Second

// RedisChannelLocker is an implementation of the ChannelLocker interface that uses Redis for distributed locking.
// It allows multiple instances of the manager to coordinate access to channels, ensuring that only one instance can perform
// operations on a channel at a time.
// The locker uses a key prefix to avoid conflicts with other locks in Redis.
// It also supports retry backoff for obtaining locks, allowing for configurable retry strategies.
// The default retry backoff is 2 seconds, but it can be configured using the WithRetryBackoff method.
type RedisChannelLocker struct {
	client       *redislock.Client
	retryBackoff time.Duration
	keyPrefix    string
}

// RedisChannelLock is an implementation of the ChannelLock interface that uses Redis locks.
// It represents a lock on a specific channel.
// It is returned by the RedisChannelLocker when a lock is successfully obtained.
// The lock can be released using the Release method, which will remove the lock from Redis.
type RedisChannelLock struct {
	lock *redislock.Lock
}

// NewRedisChannelLocker creates a new RedisChannelLocker instance.
// It takes a Redis client as an argument, which is used to communicate with the Redis server.
// The client should be configured with the appropriate Redis server address and authentication details.
// The RedisChannelLocker can be configured with a custom retry backoff and key prefix using the WithRetryBackoff and WithKeyPrefix methods.
// If no custom values are provided, it defaults to a retry backoff of 2 seconds and a key prefix of "slack-manager:channel-lock:".
func NewRedisChannelLocker(client *redis.Client) *RedisChannelLocker {
	return &RedisChannelLocker{
		client:       redislock.New(client),
		retryBackoff: 2 * time.Second,
		keyPrefix:    "slack-manager:channel-lock:",
	}
}

// WithRetryBackoff sets the retry backoff duration for obtaining locks.
// If the backoff is less than or equal to zero, it defaults to 2 seconds.
func (r *RedisChannelLocker) WithRetryBackoff(backoff time.Duration) *RedisChannelLocker {
	if backoff <= 0 {
		backoff = 2 * time.Second
	}

	r.retryBackoff = backoff

	return r
}

// WithKeyPrefix sets the Redis key prefix for the locks.
// The default prefix is "slack-manager:channel-lock:".
// This prefix is used to avoid conflicts with other keys in Redis.
// An empty prefix means that the locks will have keys equal to the Slack channel ID.
func (r *RedisChannelLocker) WithKeyPrefix(prefix string) *RedisChannelLocker {
	r.keyPrefix = prefix
	return r
}

// Obtain tries to obtain a lock for the given key (channel ID) with a specified TTL (time to live).
// It uses a retry strategy based on the configured retry backoff and max wait duration.
// If the lock is successfully obtained, it returns a RedisChannelLock instance.
// If the lock cannot be obtained within the max wait duration, it returns ErrChannelLockUnavailable.
// The key is prefixed with the configured key prefix to avoid conflicts with other locks in Redis.
func (r *RedisChannelLocker) Obtain(ctx context.Context, key string, ttl time.Duration, maxWait time.Duration) (ChannelLock, error) { //nolint:ireturn
	var retryStrategy redislock.RetryStrategy

	// If maxWait is zero or less than the retry backoff, we do not retry.
	// This means we will try to obtain the lock once and fail immediately if it is not available.
	// If maxWait is greater than the retry backoff, we will retry until we reach the maxWait duration or the lock is obtained.
	if maxWait <= 0 || maxWait < r.retryBackoff {
		retryStrategy = redislock.NoRetry()
	} else {
		maxRetryAttemps := int(maxWait / r.retryBackoff)
		retryStrategy = redislock.LimitRetry(redislock.LinearBackoff(r.retryBackoff), maxRetryAttemps)
	}

	opts := &redislock.Options{
		RetryStrategy: retryStrategy,
	}

	key = r.keyPrefix + key

	lock, err := r.client.Obtain(ctx, key, ttl, opts)
	if err != nil {
		if errors.Is(err, redislock.ErrNotObtained) {
			return nil, ErrChannelLockUnavailable
		}
		return nil, err
	}

	return &RedisChannelLock{lock: lock}, nil
}

// Key returns the key associated with the RedisChannelLock instance.
func (l *RedisChannelLock) Key() string {
	if l.lock == nil {
		return ""
	}

	return l.lock.Key()
}

// Release releases the lock held by the RedisChannelLock instance.
// It removes the lock from Redis, allowing other instances to obtain the lock.
// If the lock is already released, or not held, it returns nil.
// It uses a new timeout context to ensure the release operation completes within a reasonable
// time frame, even if the caller's context is cancelled.
func (l *RedisChannelLock) Release() error {
	if l.lock == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), lockReleaseTimeout)
	defer cancel()

	if err := l.lock.Release(ctx); err != nil {
		if !errors.Is(err, redislock.ErrLockNotHeld) {
			return err
		}
	}

	l.lock = nil

	return nil
}
