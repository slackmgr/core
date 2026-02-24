package manager_test

import (
	"context"
	"testing"
	"time"

	"github.com/slackmgr/core/manager"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestGate(maxDrainWait time.Duration) *manager.LocalRateLimitGate {
	return manager.NewLocalRateLimitGate(&types.NoopLogger{}, maxDrainWait)
}

func TestLocalRateLimitGate_NotBlockedByDefault(t *testing.T) {
	t.Parallel()

	gate := newTestGate(0)

	blocked, err := gate.IsBlocked(context.Background())

	require.NoError(t, err)
	assert.False(t, blocked)
}

func TestLocalRateLimitGate_WaitReturnsImmediatelyWhenNotBlocked(t *testing.T) {
	t.Parallel()

	gate := newTestGate(0)

	start := time.Now()
	err := gate.Wait(context.Background())
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 50*time.Millisecond, "Wait should return immediately when not blocked")
}

func TestLocalRateLimitGate_SignalBlocksWait(t *testing.T) {
	t.Parallel()

	gate := newTestGate(0)

	// Signal a rate limit 200 ms into the future.
	until := time.Now().Add(200 * time.Millisecond)
	err := gate.Signal(context.Background(), until)
	require.NoError(t, err)

	blocked, err := gate.IsBlocked(context.Background())
	require.NoError(t, err)
	assert.True(t, blocked)

	start := time.Now()
	err = gate.Wait(context.Background())
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, 150*time.Millisecond, "Wait should have blocked for the rate limit window")
}

func TestLocalRateLimitGate_SignalExtendsWindow(t *testing.T) {
	t.Parallel()

	gate := newTestGate(0)

	first := time.Now().Add(100 * time.Millisecond)
	require.NoError(t, gate.Signal(context.Background(), first))

	// Second Signal with a later time should extend the window.
	second := time.Now().Add(300 * time.Millisecond)
	require.NoError(t, gate.Signal(context.Background(), second))

	start := time.Now()
	err := gate.Wait(context.Background())
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond, "Wait should have honoured the extended window")
}

func TestLocalRateLimitGate_SignalDoesNotShortenWindow(t *testing.T) {
	t.Parallel()

	gate := newTestGate(0)

	long := time.Now().Add(300 * time.Millisecond)
	require.NoError(t, gate.Signal(context.Background(), long))

	// Earlier time should NOT shorten the already-recorded window.
	early := time.Now().Add(50 * time.Millisecond)
	require.NoError(t, gate.Signal(context.Background(), early))

	start := time.Now()
	err := gate.Wait(context.Background())
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond, "Window should not have been shortened by the earlier Signal")
}

func TestLocalRateLimitGate_ContextCancellationDuringWait(t *testing.T) {
	t.Parallel()

	gate := newTestGate(0)

	// Block for 10 seconds — we'll cancel the context before that.
	require.NoError(t, gate.Signal(context.Background(), time.Now().Add(10*time.Second)))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := gate.Wait(ctx)

	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestLocalRateLimitGate_ReadyFnFalseKeepsWaitBlocking(t *testing.T) {
	t.Parallel()

	// Short drain wait so the test doesn't take long.
	gate := newTestGate(300 * time.Millisecond)

	// Signal a very short time window so we get to phase 2 quickly.
	require.NoError(t, gate.Signal(context.Background(), time.Now().Add(50*time.Millisecond)))

	// readyFn always returns false.
	gate.SetReadyCheck(func() bool { return false })

	start := time.Now()
	err := gate.Wait(context.Background())
	elapsed := time.Since(start)

	// Should block until maxDrainWait (300 ms) expires, then fail-open.
	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, 250*time.Millisecond, "Wait should have blocked until drain wait expired")
}

func TestLocalRateLimitGate_ReadyFnTrueAllowsResumeAfterWindow(t *testing.T) {
	t.Parallel()

	gate := newTestGate(0)

	require.NoError(t, gate.Signal(context.Background(), time.Now().Add(100*time.Millisecond)))

	// readyFn immediately true.
	gate.SetReadyCheck(func() bool { return true })

	err := gate.Wait(context.Background())

	require.NoError(t, err)
}

func TestLocalRateLimitGate_ReadyFnFalseExceedsDrainWait_FailOpen(t *testing.T) {
	t.Parallel()

	// Very short drain wait so test stays fast.
	gate := newTestGate(150 * time.Millisecond)

	require.NoError(t, gate.Signal(context.Background(), time.Now().Add(50*time.Millisecond)))

	// readyFn never returns true.
	gate.SetReadyCheck(func() bool { return false })

	start := time.Now()
	err := gate.Wait(context.Background())
	elapsed := time.Since(start)

	// Should return (fail-open) after maxDrainWait.
	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond, "Should have waited for maxDrainWait")
	assert.Less(t, elapsed, 600*time.Millisecond, "Should not have waited much longer than maxDrainWait")
}
