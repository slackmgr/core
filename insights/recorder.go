package insights

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/slackmgr/types"
)

// Metric names registered by the [Recorder]. The host's metrics prefix (e.g. "slackmgr_") is applied by the caller.
const (
	eventsWrittenTotalMetric = "insights_events_written_total"
	eventsDroppedTotalMetric = "insights_events_dropped_total"
	writeDurationMetric      = "insights_write_duration_seconds"
	bufferSizeMetric         = "insights_buffer_size"
)

// Drop reasons used as label values for the dropped events metric.
const (
	dropReasonBufferFull = "buffer_full"
	dropReasonWriteError = "write_error"
	dropReasonPanic      = "panic"
	dropReasonShutdown   = "shutdown"
)

// Default recorder settings. See the corresponding [RecorderOption] functions.
const (
	DefaultBufferSize       = 10000
	DefaultBatchSize        = 500
	DefaultFlushInterval    = time.Second
	DefaultWriteTimeout     = 5 * time.Second
	DefaultInitialBackoff   = time.Second
	DefaultMaxBackoff       = time.Minute
	DefaultErrorLogInterval = time.Minute
)

var errSinkPanic = errors.New("insights sink panicked")

// RecorderOption configures a [Recorder].
type RecorderOption func(*recorderOptions)

type recorderOptions struct {
	bufferSize       int
	batchSize        int
	flushInterval    time.Duration
	writeTimeout     time.Duration
	initialBackoff   time.Duration
	maxBackoff       time.Duration
	errorLogInterval time.Duration
}

// WithBufferSize sets the capacity of the in-memory event buffer. When the buffer is full, new events are dropped.
// Values below 1 are ignored. Default [DefaultBufferSize].
func WithBufferSize(n int) RecorderOption {
	return func(o *recorderOptions) {
		if n > 0 {
			o.bufferSize = n
		}
	}
}

// WithBatchSize sets the maximum number of events per sink write. Values below 1 are ignored. Default [DefaultBatchSize].
func WithBatchSize(n int) RecorderOption {
	return func(o *recorderOptions) {
		if n > 0 {
			o.batchSize = n
		}
	}
}

// WithFlushInterval sets the maximum time an event waits in a partial batch before it is written.
// Values below 1ms are ignored. Default [DefaultFlushInterval].
func WithFlushInterval(d time.Duration) RecorderOption {
	return func(o *recorderOptions) {
		if d >= time.Millisecond {
			o.flushInterval = d
		}
	}
}

// WithWriteTimeout sets the deadline of each sink write. Values below 1ms are ignored. Default [DefaultWriteTimeout].
func WithWriteTimeout(d time.Duration) RecorderOption {
	return func(o *recorderOptions) {
		if d >= time.Millisecond {
			o.writeTimeout = d
		}
	}
}

// WithBackoff sets the initial and maximum pause after a failed sink write. The pause doubles after each consecutive
// failure. While paused, events accumulate in the buffer, and are dropped when it is full. Values below 1ms are
// ignored. Defaults [DefaultInitialBackoff] and [DefaultMaxBackoff].
func WithBackoff(initial, maxBackoff time.Duration) RecorderOption {
	return func(o *recorderOptions) {
		if initial >= time.Millisecond {
			o.initialBackoff = initial
		}

		if maxBackoff >= time.Millisecond {
			o.maxBackoff = maxBackoff
		}
	}
}

// WithErrorLogInterval sets the minimum interval between error log entries. Errors in between are counted and
// summarized in the next entry. Values below 1ms are ignored. Default [DefaultErrorLogInterval].
func WithErrorLogInterval(d time.Duration) RecorderOption {
	return func(o *recorderOptions) {
		if d >= time.Millisecond {
			o.errorLogInterval = d
		}
	}
}

// Recorder records [types.InsightsEvent] values asynchronously to a [types.InsightsSink].
//
// The Recorder is designed to never affect the code that records events:
//   - [Recorder.Record] never blocks. Events are put in a bounded buffer, and dropped (and counted) when it is full.
//   - Events are written in batches by a single background goroutine, never by the caller.
//   - Sink errors and panics are recovered, counted and logged (rate-limited). They never propagate.
//   - After a failed write, the recorder pauses with exponential backoff, to avoid adding load to a struggling database.
//   - [Recorder.Shutdown] flushes remaining events with a hard deadline.
//
// A nil *Recorder is valid and records nothing, so callers can use it without checks. Use [Recorder.Enabled] to
// skip building events entirely when recording is disabled.
//
// Lost events are reported in the event stream itself, as an [types.InsightsEventGap] event written after the
// next successful write.
type Recorder struct {
	sink    types.InsightsSink
	logger  types.Logger
	metrics types.Metrics
	opts    recorderOptions

	ch chan types.InsightsEvent

	// droppedFull counts all events dropped by Record because the buffer was full. It is only incremented.
	droppedFull atomic.Int64

	// droppedFullSeen is the value of droppedFull last absorbed by the writer goroutine.
	droppedFullSeen int64

	// lost counts lost events not yet reported in a gap event. Only accessed by the writer goroutine.
	lost int64

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	// shutdownCtx is cancelled when the shutdown deadline passes, aborting any in-flight write.
	shutdownCtx    context.Context //nolint:containedctx // Owned by the recorder lifecycle, not a request
	shutdownCancel context.CancelFunc
	deadlineMu     sync.Mutex
	deadline       time.Time

	errLog rateLimitedLogger
}

// NewRecorder creates a [Recorder] writing to sink. It returns nil (a valid, disabled recorder) if sink is nil.
// Call [Recorder.Start] to start writing, and [Recorder.Shutdown] to stop.
func NewRecorder(sink types.InsightsSink, logger types.Logger, metrics types.Metrics, opts ...RecorderOption) *Recorder {
	if sink == nil {
		return nil
	}

	if logger == nil {
		logger = &types.NoopLogger{}
	}

	if metrics == nil {
		metrics = &types.NoopMetrics{}
	}

	o := recorderOptions{
		bufferSize:       DefaultBufferSize,
		batchSize:        DefaultBatchSize,
		flushInterval:    DefaultFlushInterval,
		writeTimeout:     DefaultWriteTimeout,
		initialBackoff:   DefaultInitialBackoff,
		maxBackoff:       DefaultMaxBackoff,
		errorLogInterval: DefaultErrorLogInterval,
	}

	for _, opt := range opts {
		opt(&o)
	}

	if o.maxBackoff < o.initialBackoff {
		o.maxBackoff = o.initialBackoff
	}

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	logger = logger.WithField("component", "insights-recorder")

	r := &Recorder{
		sink:           sink,
		logger:         logger,
		metrics:        metrics,
		opts:           o,
		ch:             make(chan types.InsightsEvent, o.bufferSize),
		stopCh:         make(chan struct{}),
		doneCh:         make(chan struct{}),
		shutdownCtx:    shutdownCtx,
		shutdownCancel: shutdownCancel,
		errLog:         rateLimitedLogger{logger: logger, interval: o.errorLogInterval},
	}

	metrics.RegisterCounter(eventsWrittenTotalMetric, "Total insights events written to the sink")
	metrics.RegisterCounter(eventsDroppedTotalMetric, "Total insights events dropped, by reason", "reason")
	metrics.RegisterHistogram(writeDurationMetric, "Duration of insights sink writes in seconds", []float64{0.005, 0.01, 0.05, 0.1, 0.5, 1, 5})
	metrics.RegisterGauge(bufferSizeMetric, "Number of insights events waiting in the buffer")
	metrics.CounterAdd(eventsWrittenTotalMetric, 0)

	for _, reason := range []string{dropReasonBufferFull, dropReasonWriteError, dropReasonPanic, dropReasonShutdown} {
		metrics.CounterAdd(eventsDroppedTotalMetric, 0, reason)
	}

	return r
}

// Enabled returns true if the recorder records events (i.e. it is not nil).
func (r *Recorder) Enabled() bool {
	return r != nil
}

// Record queues an event for writing. It never blocks: if the buffer is full, the event is dropped and counted.
// If e.Time is zero, it is set to the current time. Record is safe for concurrent use, and may be called before
// [Recorder.Start] and after [Recorder.Shutdown] (events are then buffered or dropped).
func (r *Recorder) Record(e types.InsightsEvent) {
	if r == nil {
		return
	}

	if e.Time.IsZero() {
		e.Time = time.Now()
	}

	select {
	case r.ch <- e:
	default:
		r.droppedFull.Add(1)
	}
}

// Start starts the background writer goroutine. It is safe to call multiple times.
func (r *Recorder) Start() {
	if r == nil {
		return
	}

	r.startOnce.Do(func() {
		go r.run()
	})
}

// Shutdown stops the recorder. It writes the events remaining in the buffer until the buffer is empty or the
// timeout has passed, and then returns. It never blocks longer than the timeout (plus scheduling overhead):
// an in-flight sink write is cancelled at the deadline. A zero or negative timeout skips flushing.
// Events not written by the deadline are dropped and counted. It is safe to call multiple times.
func (r *Recorder) Shutdown(timeout time.Duration) {
	if r == nil {
		return
	}

	r.Start() // Ensure the writer goroutine exists, so doneCh is eventually closed.

	r.stopOnce.Do(func() {
		deadline := time.Now().Add(max(timeout, 0))

		r.deadlineMu.Lock()
		r.deadline = deadline
		r.deadlineMu.Unlock()

		close(r.stopCh)

		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()

		select {
		case <-r.doneCh:
		case <-timer.C:
			r.shutdownCancel()

			// Give the writer a brief moment to observe the cancellation and record final metrics.
			select {
			case <-r.doneCh:
			case <-time.After(50 * time.Millisecond):
			}
		}

		r.shutdownCancel()
	})
}

// run is the writer loop. It only returns after Shutdown has been called.
func (r *Recorder) run() {
	defer close(r.doneCh)

	batch := make([]types.InsightsEvent, 0, r.opts.batchSize)
	ticker := time.NewTicker(r.opts.flushInterval)
	defer ticker.Stop()

	backoff := time.Duration(0)

	for {
		// Pause after a failed write. Events accumulate in the buffer meanwhile.
		if backoff > 0 {
			timer := time.NewTimer(backoff)

			select {
			case <-r.stopCh:
				timer.Stop()
				r.drain(batch)
				return
			case <-timer.C:
			}
		}

		select {
		case <-r.stopCh:
			r.drain(batch)
			return
		case e := <-r.ch:
			batch = append(batch, e)

			if len(batch) < r.opts.batchSize {
				continue
			}
		case <-ticker.C:
			r.flushMetrics()

			if len(batch) == 0 {
				continue
			}
		}

		if r.write(r.shutdownCtx, batch) {
			backoff = 0
		} else {
			backoff = nextBackoff(backoff, r.opts.initialBackoff, r.opts.maxBackoff)
		}

		batch = batch[:0]
	}
}

// drain writes the pending batch and the remaining buffered events until the buffer is empty, a write fails,
// or the shutdown deadline passes. Events left unwritten are counted as dropped.
func (r *Recorder) drain(batch []types.InsightsEvent) {
	defer r.flushMetrics()

	r.deadlineMu.Lock()
	deadline := r.deadline
	r.deadlineMu.Unlock()

	ctx, cancel := context.WithDeadline(r.shutdownCtx, deadline)
	defer cancel()

	unwritten := 0

	for {
	fill:
		for len(batch) < r.opts.batchSize {
			select {
			case e := <-r.ch:
				batch = append(batch, e)
			default:
				break fill
			}
		}

		if len(batch) == 0 {
			break
		}

		if ctx.Err() != nil {
			unwritten += len(batch)
			break
		}

		ok := r.write(ctx, batch)
		batch = batch[:0]

		if !ok {
			break
		}
	}

	if n := unwritten + len(r.ch); n > 0 {
		r.metrics.CounterAdd(eventsDroppedTotalMetric, float64(n), dropReasonShutdown)
	}
}

// write normalizes and writes a batch, followed by a gap event if events were lost since the last successful write.
// It returns true on success. Failures and panics are counted and logged, never propagated.
func (r *Recorder) write(parent context.Context, batch []types.InsightsEvent) bool {
	now := time.Now().UTC()

	for i := range batch {
		batch[i].Normalize(now)
	}

	r.absorbDroppedFull()

	events := batch
	if r.lost > 0 {
		events = append(make([]types.InsightsEvent, 0, len(batch)+1), batch...)
		events = append(events, types.InsightsEvent{
			Time:   now,
			Type:   types.InsightsEventGap,
			Count:  int(r.lost),
			Values: map[string]float64{types.InsightsValueLostEvents: float64(r.lost)},
		})
	}

	ctx, cancel := context.WithTimeout(parent, r.opts.writeTimeout)
	defer cancel()

	started := time.Now()
	err := r.safeWrite(ctx, events)
	r.metrics.Observe(writeDurationMetric, time.Since(started).Seconds())

	if err != nil {
		reason := dropReasonWriteError

		switch {
		case errors.Is(err, errSinkPanic):
			reason = dropReasonPanic
		case parent.Err() != nil:
			reason = dropReasonShutdown
		}

		r.metrics.CounterAdd(eventsDroppedTotalMetric, float64(len(batch)), reason)
		r.lost += int64(len(batch))
		r.errLog.log(fmt.Sprintf("Failed to write %d insights events", len(batch)), err)

		return false
	}

	r.metrics.CounterAdd(eventsWrittenTotalMetric, float64(len(batch)))
	r.lost = 0

	return true
}

// safeWrite calls the sink, converting panics to errors.
func (r *Recorder) safeWrite(ctx context.Context, events []types.InsightsEvent) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%w: %v", errSinkPanic, p)
		}
	}()

	return r.sink.WriteInsightsEvents(ctx, events)
}

// flushMetrics publishes the buffer-full drop counter and the buffer size gauge.
func (r *Recorder) flushMetrics() {
	r.metrics.GaugeSet(bufferSizeMetric, float64(len(r.ch)))
	r.absorbDroppedFull()
}

// absorbDroppedFull moves new buffer-full drops into the lost counter and publishes them as metrics.
// Only called by the writer goroutine.
func (r *Recorder) absorbDroppedFull() {
	current := r.droppedFull.Load()
	delta := current - r.droppedFullSeen

	if delta <= 0 {
		return
	}

	r.droppedFullSeen = current
	r.lost += delta
	r.metrics.CounterAdd(eventsDroppedTotalMetric, float64(delta), dropReasonBufferFull)
}

// nextBackoff doubles the backoff, starting at initial and capped at maxBackoff.
func nextBackoff(current, initial, maxBackoff time.Duration) time.Duration {
	if current <= 0 {
		return initial
	}

	return min(current*2, maxBackoff)
}

// rateLimitedLogger logs errors at most once per interval, summarizing suppressed errors.
type rateLimitedLogger struct {
	logger     types.Logger
	interval   time.Duration
	lastLogged time.Time
	suppressed int
}

func (l *rateLimitedLogger) log(msg string, err error) {
	now := time.Now()

	if !l.lastLogged.IsZero() && now.Sub(l.lastLogged) < l.interval {
		l.suppressed++
		return
	}

	logger := l.logger
	if l.suppressed > 0 {
		logger = logger.WithField("suppressed_errors", l.suppressed)
	}

	logger.Errorf("%s: %s", msg, err)

	l.lastLogged = now
	l.suppressed = 0
}
