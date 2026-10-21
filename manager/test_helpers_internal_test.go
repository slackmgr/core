package manager

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slackmgr/core/insights"
	"github.com/slackmgr/types"
)

// recordingMetrics is a thread-safe types.Metrics implementation that records counter values for assertions.
type recordingMetrics struct {
	mu       sync.Mutex
	counters map[string]float64
}

func newRecordingMetrics() *recordingMetrics {
	return &recordingMetrics{counters: make(map[string]float64)}
}

func metricKey(name string, labelValues ...string) string {
	return name + "{" + strings.Join(labelValues, ",") + "}"
}

func (r *recordingMetrics) RegisterCounter(_, _ string, _ ...string)                {}
func (r *recordingMetrics) RegisterGauge(_, _ string, _ ...string)                  {}
func (r *recordingMetrics) RegisterHistogram(_, _ string, _ []float64, _ ...string) {}
func (r *recordingMetrics) GaugeSet(_ string, _ float64, _ ...string)               {}
func (r *recordingMetrics) GaugeAdd(_ string, _ float64, _ ...string)               {}
func (r *recordingMetrics) Observe(_ string, _ float64, _ ...string)                {}
func (r *recordingMetrics) CounterInc(name string, labelValues ...string) {
	r.CounterAdd(name, 1, labelValues...)
}

func (r *recordingMetrics) CounterAdd(name string, value float64, labelValues ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.counters[metricKey(name, labelValues...)] += value
}

// counter returns the current value of the counter with the given name and label values.
func (r *recordingMetrics) counter(name string, labelValues ...string) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.counters[metricKey(name, labelValues...)]
}

// memorySink is a types.InsightsSink that stores events in memory. fn, if set, is called before storing.
type memorySink struct {
	mu     sync.Mutex
	events []types.InsightsEvent
	fn     func(ctx context.Context) error
}

func (s *memorySink) WriteInsightsEvents(ctx context.Context, events []types.InsightsEvent) error {
	if s.fn != nil {
		if err := s.fn(ctx); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.events = append(s.events, events...)

	return nil
}

// ofType returns the stored events of the given type.
func (s *memorySink) ofType(t types.InsightsEventType) []types.InsightsEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result []types.InsightsEvent

	for _, e := range s.events {
		if e.Type == t {
			result = append(result, e)
		}
	}

	return result
}

// newTestRecorder returns a started recorder writing to a new memorySink. Call flush before inspecting the sink.
func newTestRecorder(t *testing.T) (*insights.Recorder, *memorySink) {
	t.Helper()

	sink := &memorySink{}
	recorder := insights.NewRecorder(sink, &types.NoopLogger{}, &types.NoopMetrics{}, insights.WithFlushInterval(time.Millisecond))
	recorder.Start()

	t.Cleanup(func() { recorder.Shutdown(time.Second) })

	return recorder, sink
}

// flush stops the recorder and writes all buffered events.
func flush(recorder *insights.Recorder) {
	recorder.Shutdown(time.Second)
}
