package manager

import (
	"context"
	"sync"
	"time"

	"github.com/slackmgr/types"
)

const (
	rateLimitGateSignalsTotalMetric      = "rate_limit_gate_signals_total"
	rateLimitGateDrainTimeoutTotalMetric = "rate_limit_gate_drain_timeout_total"
)

const defaultMaxDrainWait = 30 * time.Second

// RateLimitGate coordinates a global pause across all channel managers when a
// Slack 429 is detected. Channel managers call [RateLimitGate.Wait] before
// processing each work item; the gate blocks until the rate-limit window has
// expired and the Socket Mode handler is quiet (no in-flight event handlers).
type RateLimitGate interface {
	// Signal records that a rate limit was hit and that callers should block
	// until `until` has passed. Implementations extend the window if `until`
	// is later than any previously recorded deadline.
	Signal(ctx context.Context, until time.Time) error

	// Wait blocks until the gate is open and Socket Mode is quiet. It returns
	// immediately when the gate is not blocked. Context cancellation is
	// propagated as an error.
	Wait(ctx context.Context) error

	// IsBlocked reports whether the gate is currently blocking callers.
	IsBlocked(ctx context.Context) (bool, error)

	// SetReadyCheck registers a function that returns true when Socket Mode is
	// quiet (no in-flight event handlers). Must be called once from [Manager.Run]
	// after the Slack client is connected.
	SetReadyCheck(fn func() bool)
}

// LocalRateLimitGate is an in-process [RateLimitGate] implementation suitable
// for single-instance deployments. For multi-instance deployments use
// [RedisRateLimitGate] so that all instances respect the same rate-limit window.
type LocalRateLimitGate struct {
	mu           sync.RWMutex
	blockedUntil time.Time
	readyFn      func() bool
	maxDrainWait time.Duration
	logger       types.Logger
	metrics      types.Metrics
}

// NewLocalRateLimitGate creates a [LocalRateLimitGate].
// Pass a positive maxDrainWait to override the default 30-second Socket Mode
// drain limit; zero or negative values use the default.
func NewLocalRateLimitGate(logger types.Logger, maxDrainWait time.Duration) *LocalRateLimitGate {
	if maxDrainWait <= 0 {
		maxDrainWait = defaultMaxDrainWait
	}

	return &LocalRateLimitGate{
		maxDrainWait: maxDrainWait,
		logger:       logger,
	}
}

// Signal records a rate-limit deadline. The blocked window is extended if until
// is later than any previously recorded deadline.
func (g *LocalRateLimitGate) Signal(_ context.Context, until time.Time) error {
	g.mu.Lock()

	if until.After(g.blockedUntil) {
		g.blockedUntil = until
	}

	m := g.metrics
	g.mu.Unlock()

	if m != nil {
		m.Inc(rateLimitGateSignalsTotalMetric)
	}

	return nil
}

// Wait blocks until the rate-limit window has expired and Socket Mode is quiet.
// Returns immediately when the gate is not blocked.
func (g *LocalRateLimitGate) Wait(ctx context.Context) error {
	// Fast path: not blocked.
	g.mu.RLock()
	until := g.blockedUntil
	g.mu.RUnlock()

	if !time.Now().Before(until) {
		return nil
	}

	g.logger.Info("Rate limit gate: waiting for rate limit window to expire")

	// Phase 1: wait for the time window to expire. Sleep until the deadline,
	// capped at 500 ms so that concurrent Signal extensions are observed promptly.
	for {
		g.mu.RLock()
		until = g.blockedUntil
		g.mu.RUnlock()

		remaining := time.Until(until)
		if remaining <= 0 {
			break
		}

		sleep := min(remaining, 500*time.Millisecond)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
		}
	}

	// Phase 2: wait for Socket Mode to be quiet (poll every 100 ms).
	g.mu.RLock()
	fn := g.readyFn
	m := g.metrics
	g.mu.RUnlock()

	if fn == nil {
		return nil
	}

	drainDeadline := time.Now().Add(g.maxDrainWait)

	for !fn() {
		if time.Now().After(drainDeadline) {
			g.logger.Info("Rate limit gate: Socket Mode drain wait exceeded, resuming (fail-open)")

			if m != nil {
				m.Inc(rateLimitGateDrainTimeoutTotalMetric)
			}

			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	return nil
}

// IsBlocked reports whether the rate-limit window is currently active.
func (g *LocalRateLimitGate) IsBlocked(_ context.Context) (bool, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	return time.Now().Before(g.blockedUntil), nil
}

// SetReadyCheck registers the function used to determine when Socket Mode has no
// in-flight handlers. Called once from [Manager.Run] after the Slack client connects.
func (g *LocalRateLimitGate) SetReadyCheck(fn func() bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.readyFn = fn
}

// connectMetrics registers gate metrics against m and stores m for future use.
// Called once from [Manager.Run] after the metrics implementation is confirmed.
func (g *LocalRateLimitGate) connectMetrics(m types.Metrics) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.metrics = m
	m.RegisterCounter(rateLimitGateSignalsTotalMetric, "Total number of times the rate limit gate was signaled")
	m.RegisterCounter(rateLimitGateDrainTimeoutTotalMetric, "Total number of times the Socket Mode drain wait timed out during rate limit recovery")
}

// NoopRateLimitGate is a no-op [RateLimitGate] for tests and single-instance
// deployments that do not need distributed rate-limit coordination.
type NoopRateLimitGate struct{}

// Signal is a no-op; the gate never records a rate-limit deadline.
func (n *NoopRateLimitGate) Signal(_ context.Context, _ time.Time) error { return nil }

// Wait is a no-op; the gate never blocks callers.
func (n *NoopRateLimitGate) Wait(_ context.Context) error { return nil }

// IsBlocked always returns false; the gate is never blocking.
func (n *NoopRateLimitGate) IsBlocked(_ context.Context) (bool, error) { return false, nil }

// SetReadyCheck is a no-op; the gate does not use a ready-check function.
func (n *NoopRateLimitGate) SetReadyCheck(_ func() bool) {}
