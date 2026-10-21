package insights

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- test doubles ---

// recordingMetrics is a thread-safe types.Metrics implementation that records counter and gauge values.
type recordingMetrics struct {
	mu     sync.Mutex
	values map[string]float64
}

func newRecordingMetrics() *recordingMetrics {
	return &recordingMetrics{values: make(map[string]float64)}
}

func (m *recordingMetrics) RegisterCounter(_, _ string, _ ...string)                {}
func (m *recordingMetrics) RegisterGauge(_, _ string, _ ...string)                  {}
func (m *recordingMetrics) RegisterHistogram(_, _ string, _ []float64, _ ...string) {}
func (m *recordingMetrics) Observe(_ string, _ float64, _ ...string)                {}
func (m *recordingMetrics) CounterInc(name string, labels ...string) {
	m.CounterAdd(name, 1, labels...)
}
func (m *recordingMetrics) GaugeAdd(_ string, _ float64, _ ...string) {}

func (m *recordingMetrics) CounterAdd(name string, value float64, labels ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.values[m.key(name, labels...)] += value
}

func (m *recordingMetrics) GaugeSet(name string, value float64, labels ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.values[m.key(name, labels...)] = value
}

func (m *recordingMetrics) key(name string, labels ...string) string {
	return name + "{" + strings.Join(labels, ",") + "}"
}

func (m *recordingMetrics) get(name string, labels ...string) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.values[m.key(name, labels...)]
}

// recordingSink stores written events. Its behaviour can be customised per call with fn.
type recordingSink struct {
	mu      sync.Mutex
	events  []types.InsightsEvent
	batches []int
	calls   atomic.Int32

	// fn, if set, is called before storing; a non-nil error fails the write.
	fn func(ctx context.Context, call int32) error
}

func (s *recordingSink) WriteInsightsEvents(ctx context.Context, events []types.InsightsEvent) error {
	call := s.calls.Add(1)

	if s.fn != nil {
		if err := s.fn(ctx, call); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.events = append(s.events, events...)
	s.batches = append(s.batches, len(events))

	return nil
}

func (s *recordingSink) written() []types.InsightsEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]types.InsightsEvent(nil), s.events...)
}

// countType returns the number of written events of type t, and the sum of their counts.
func (s *recordingSink) countType(t types.InsightsEventType) (int, int) {
	n, sum := 0, 0

	for _, e := range s.written() {
		if e.Type == t {
			n++
			sum += e.Count
		}
	}

	return n, sum
}

func event(i int) types.InsightsEvent {
	return types.InsightsEvent{Type: types.InsightsEventAlertGrouped, ChannelID: "C1", Count: i}
}

func fastOptions(extra ...RecorderOption) []RecorderOption {
	return append([]RecorderOption{
		WithFlushInterval(5 * time.Millisecond),
		WithBackoff(5*time.Millisecond, 20*time.Millisecond),
		WithWriteTimeout(time.Second),
	}, extra...)
}

// --- tests ---

func TestNilRecorder(t *testing.T) {
	t.Parallel()

	r := NewRecorder(nil, nil, nil)
	require.Nil(t, r)

	assert.False(t, r.Enabled())
	r.Record(event(1))
	r.Start()
	r.Shutdown(time.Second)
}

func TestRecorder_WritesInBatches(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	metrics := newRecordingMetrics()
	r := NewRecorder(sink, &types.NoopLogger{}, metrics, fastOptions(WithBatchSize(3))...)
	require.True(t, r.Enabled())

	r.Start()

	for i := range 7 {
		r.Record(event(i))
	}

	require.Eventually(t, func() bool { return len(sink.written()) == 7 }, 2*time.Second, time.Millisecond)

	r.Shutdown(time.Second)

	for _, n := range sink.batches {
		assert.LessOrEqual(t, n, 3)
	}

	for _, e := range sink.written() {
		assert.False(t, e.Time.IsZero(), "time is set by Record")
		assert.Equal(t, time.UTC, e.Time.Location(), "time is normalized to UTC")
	}

	assert.InDelta(t, 7.0, metrics.get(eventsWrittenTotalMetric), 0)
	n, _ := sink.countType(types.InsightsEventGap)
	assert.Zero(t, n, "no gap event when nothing was lost")
}

func TestRecorder_NormalizesEvents(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	r := NewRecorder(sink, nil, nil, fastOptions()...)
	r.Start()

	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.FixedZone("X", 3600))
	r.Record(types.InsightsEvent{Time: ts, Type: types.InsightsEventIssueCreated, Detail: strings.Repeat("d", 500)})
	r.Shutdown(time.Second)

	written := sink.written()
	require.Len(t, written, 1)
	assert.True(t, written[0].Time.Equal(ts))
	assert.Equal(t, time.UTC, written[0].Time.Location())
	assert.Len(t, written[0].Detail, types.InsightsMaxDetailLen)
}

// TestRecorder_SlowSinkNeverBlocksRecord proves that a sink that never returns cannot slow down the caller.
func TestRecorder_SlowSinkNeverBlocksRecord(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	sink := &recordingSink{fn: func(ctx context.Context, _ int32) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	metrics := newRecordingMetrics()
	r := NewRecorder(sink, nil, metrics, fastOptions(WithBufferSize(10), WithBatchSize(5), WithWriteTimeout(time.Hour))...)
	r.Start()

	started := time.Now()

	for i := range 10000 {
		r.Record(event(i))
	}

	elapsed := time.Since(started)
	assert.Less(t, elapsed, 200*time.Millisecond, "Record must never block, took %s", elapsed)

	// Shutdown must respect its deadline although the sink never returns on its own.
	shutdownStarted := time.Now()
	r.Shutdown(100 * time.Millisecond)
	assert.Less(t, time.Since(shutdownStarted), 500*time.Millisecond)

	close(release)

	assert.Positive(t, metrics.get(eventsDroppedTotalMetric, dropReasonBufferFull), "buffer overflow must be counted")
}

// TestRecorder_FailingSink proves that sink errors are contained, backed off, and reported as a gap.
func TestRecorder_FailingSink(t *testing.T) {
	t.Parallel()

	var failing atomic.Bool
	failing.Store(true)

	sink := &recordingSink{fn: func(_ context.Context, _ int32) error {
		if failing.Load() {
			return errors.New("database unavailable")
		}
		return nil
	}}
	metrics := newRecordingMetrics()
	logger := &countingLogger{}
	r := NewRecorder(sink, logger, metrics, fastOptions(WithBatchSize(2), WithErrorLogInterval(time.Hour))...)
	r.Start()

	for i := range 4 {
		r.Record(event(i))
	}

	require.Eventually(t, func() bool { return metrics.get(eventsDroppedTotalMetric, dropReasonWriteError) >= 4 }, 2*time.Second, time.Millisecond)
	assert.Greater(t, sink.calls.Load(), int32(1), "writes are retried for new batches after backoff")

	failing.Store(false)
	r.Record(event(100))

	require.Eventually(t, func() bool {
		n, _ := sink.countType(types.InsightsEventAlertGrouped)
		return n == 1
	}, 2*time.Second, time.Millisecond)

	r.Shutdown(time.Second)

	n, lost := sink.countType(types.InsightsEventGap)
	assert.Equal(t, 1, n, "one gap event after recovery")
	assert.InDelta(t, metrics.get(eventsDroppedTotalMetric, dropReasonWriteError), float64(lost), 0, "gap event reports all lost events")
	assert.Equal(t, int32(1), logger.errors.Load(), "errors are rate-limited")
}

// TestRecorder_PanickingSink proves that a panicking sink is recovered and the recorder keeps working.
func TestRecorder_PanickingSink(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{fn: func(_ context.Context, call int32) error {
		if call == 1 {
			panic("sink bug")
		}
		return nil
	}}
	metrics := newRecordingMetrics()
	r := NewRecorder(sink, nil, metrics, fastOptions(WithBatchSize(1))...)
	r.Start()

	r.Record(event(1))
	require.Eventually(t, func() bool { return metrics.get(eventsDroppedTotalMetric, dropReasonPanic) == 1 }, 2*time.Second, time.Millisecond)

	r.Record(event(2))
	require.Eventually(t, func() bool {
		n, _ := sink.countType(types.InsightsEventAlertGrouped)
		return n == 1
	}, 2*time.Second, time.Millisecond)

	r.Shutdown(time.Second)

	n, lost := sink.countType(types.InsightsEventGap)
	assert.Equal(t, 1, n)
	assert.Equal(t, 1, lost)
}

// TestRecorder_FullBuffer proves that Record drops exactly the overflow without blocking, and reports it.
func TestRecorder_FullBuffer(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	metrics := newRecordingMetrics()
	r := NewRecorder(sink, nil, metrics, fastOptions(WithBufferSize(5))...)

	// Not started yet: the buffer fills up.
	for i := range 8 {
		r.Record(event(i))
	}

	assert.Equal(t, int64(3), r.droppedFull.Load())

	r.Start()
	r.Shutdown(time.Second)

	n, _ := sink.countType(types.InsightsEventAlertGrouped)
	assert.Equal(t, 5, n)

	gaps, lost := sink.countType(types.InsightsEventGap)
	assert.Equal(t, 1, gaps)
	assert.Equal(t, 3, lost)
	assert.InDelta(t, 3.0, metrics.get(eventsDroppedTotalMetric, dropReasonBufferFull), 0)
}

func TestRecorder_ShutdownFlushesPendingEvents(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	r := NewRecorder(sink, nil, nil, WithFlushInterval(time.Hour), WithBatchSize(2))
	r.Start()

	for i := range 5 {
		r.Record(event(i))
	}

	r.Shutdown(time.Second)

	assert.Len(t, sink.written(), 5)

	// Idempotent, and Record after Shutdown neither blocks nor panics.
	r.Shutdown(time.Second)
	r.Record(event(99))
}

func TestRecorder_ShutdownWithoutFlush(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	metrics := newRecordingMetrics()
	r := NewRecorder(sink, nil, metrics, WithFlushInterval(time.Hour))

	for i := range 4 {
		r.Record(event(i))
	}

	started := time.Now()
	r.Shutdown(0)

	assert.Less(t, time.Since(started), 500*time.Millisecond)
	assert.Empty(t, sink.written())
	assert.InDelta(t, 4.0, metrics.get(eventsDroppedTotalMetric, dropReasonShutdown), 0)
}

func TestRecorder_ConcurrentRecord(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	r := NewRecorder(sink, nil, nil, fastOptions(WithBufferSize(100000))...)
	r.Start()

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 500 {
				r.Record(event(g*1000 + i))
			}
		})
	}

	wg.Wait()
	r.Shutdown(2 * time.Second)

	assert.Len(t, sink.written(), 4000)
}

func TestRecorderOptions_IgnoreInvalidValues(t *testing.T) {
	t.Parallel()

	r := NewRecorder(&recordingSink{}, nil, nil,
		WithBufferSize(0), WithBatchSize(-1), WithFlushInterval(0), WithWriteTimeout(0),
		WithBackoff(0, 0), WithErrorLogInterval(0))

	assert.Equal(t, DefaultBufferSize, r.opts.bufferSize)
	assert.Equal(t, DefaultBatchSize, r.opts.batchSize)
	assert.Equal(t, DefaultFlushInterval, r.opts.flushInterval)
	assert.Equal(t, DefaultWriteTimeout, r.opts.writeTimeout)
	assert.Equal(t, DefaultInitialBackoff, r.opts.initialBackoff)
	assert.Equal(t, DefaultMaxBackoff, r.opts.maxBackoff)
	assert.Equal(t, DefaultErrorLogInterval, r.opts.errorLogInterval)

	r = NewRecorder(&recordingSink{}, nil, nil, WithBackoff(time.Minute, time.Second))
	assert.Equal(t, time.Minute, r.opts.maxBackoff, "max backoff is at least the initial backoff")
}

func TestNextBackoff(t *testing.T) {
	t.Parallel()

	assert.Equal(t, time.Second, nextBackoff(0, time.Second, 8*time.Second))
	assert.Equal(t, 2*time.Second, nextBackoff(time.Second, time.Second, 8*time.Second))
	assert.Equal(t, 8*time.Second, nextBackoff(6*time.Second, time.Second, 8*time.Second))
}

func TestRateLimitedLogger(t *testing.T) {
	t.Parallel()

	logger := &countingLogger{}
	l := rateLimitedLogger{logger: logger, interval: 20 * time.Millisecond}

	l.log("msg", errors.New("e1"))
	l.log("msg", errors.New("e2"))
	l.log("msg", errors.New("e3"))
	assert.Equal(t, int32(1), logger.errors.Load())

	time.Sleep(25 * time.Millisecond)
	l.log("msg", errors.New("e4"))
	assert.Equal(t, int32(2), logger.errors.Load())
	assert.Equal(t, 2, logger.lastSuppressed())
}

func BenchmarkRecorder_Record(b *testing.B) {
	r := NewRecorder(&recordingSink{}, nil, nil, WithBufferSize(1))
	e := types.InsightsEvent{Time: time.Now(), Type: types.InsightsEventAlertGrouped, ChannelID: "C1", IssueID: "i", CorrelationID: "c"}

	b.ReportAllocs()

	for b.Loop() {
		r.Record(e)
	}
}

func TestRecorder_RecordDoesNotAllocate(t *testing.T) {
	r := NewRecorder(&recordingSink{}, nil, nil, WithBufferSize(1))
	e := types.InsightsEvent{Time: time.Now(), Type: types.InsightsEventAlertGrouped, ChannelID: "C1"}

	allocs := testing.AllocsPerRun(1000, func() { r.Record(e) })
	assert.Zero(t, allocs)
}

// countingLogger counts Error log calls and remembers the last suppressed_errors field.
type countingLogger struct {
	types.NoopLogger

	errors     atomic.Int32
	mu         sync.Mutex
	suppressed int
}

func (l *countingLogger) Errorf(_ string, _ ...any) { l.errors.Add(1) }

func (l *countingLogger) WithField(key string, value any) types.Logger {
	if key == "suppressed_errors" {
		if n, ok := value.(int); ok {
			l.mu.Lock()
			l.suppressed = n
			l.mu.Unlock()
		}
	}

	return l
}

func (l *countingLogger) lastSuppressed() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.suppressed
}
