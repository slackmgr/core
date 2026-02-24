package manager

import (
	"time"

	"github.com/slackmgr/core/config"
)

// RedisRateLimitGateOption is a function that configures RedisRateLimitGateOptions.
type RedisRateLimitGateOption func(*redisRateLimitGateOptions)

type redisRateLimitGateOptions struct {
	// keyPrefix is prepended to the Redis key used by the gate.
	// Default: "slack-manager:"
	keyPrefix string

	// maxDrainWait is the maximum time to wait for Socket Mode to go quiet
	// after the rate-limit window has expired before resuming (fail-open).
	// Default: 30 seconds
	maxDrainWait time.Duration
}

func newRedisRateLimitGateOptions() *redisRateLimitGateOptions {
	return &redisRateLimitGateOptions{
		keyPrefix:    config.DefaultKeyPrefix,
		maxDrainWait: defaultMaxDrainWait,
	}
}

// WithRateLimitGateKeyPrefix sets the Redis key prefix for the gate.
func WithRateLimitGateKeyPrefix(prefix string) RedisRateLimitGateOption {
	return func(o *redisRateLimitGateOptions) {
		o.keyPrefix = prefix
	}
}

// WithRateLimitGateMaxDrainWait sets the maximum time to wait for Socket Mode
// to go quiet before resuming with a fail-open behaviour.
func WithRateLimitGateMaxDrainWait(d time.Duration) RedisRateLimitGateOption {
	return func(o *redisRateLimitGateOptions) {
		o.maxDrainWait = d
	}
}
