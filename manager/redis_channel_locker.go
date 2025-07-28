package manager

import (
	"context"
	"time"

	"github.com/bsm/redislock"
	redis "github.com/redis/go-redis/v9"
)

type RedisChannelLocker struct {
	client       *redislock.Client
	retryBackoff time.Duration
}

type RedisChannelLock struct {
	lock *redislock.Lock
}

func NewRedisChannelLocker(client *redis.Client) *RedisChannelLocker {
	return &RedisChannelLocker{
		client:       redislock.New(client),
		retryBackoff: 2 * time.Second,
	}
}

func (r *RedisChannelLocker) WithRetryBackoff(backoff time.Duration) *RedisChannelLocker {
	if backoff <= 0 {
		backoff = 2 * time.Second
	}

	r.retryBackoff = backoff

	return r
}

func (r *RedisChannelLocker) Obtain(ctx context.Context, key string, ttl time.Duration, maxWait time.Duration) (ChannelLock, error) {
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

	lock, err := r.client.Obtain(ctx, key, ttl, opts)
	if err != nil {
		if err == redislock.ErrNotObtained {
			return nil, ErrChannelLockUnavailable
		}
		return nil, err
	}

	return &RedisChannelLock{lock: lock}, nil
}

func (l *RedisChannelLock) Release(ctx context.Context) error {
	if l.lock == nil {
		return nil
	}

	if err := l.lock.Release(ctx); err != nil && err != redislock.ErrLockNotHeld {
		return err
	}

	l.lock = nil

	return nil
}
