package restapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// recordingMetrics is a thread-safe types.Metrics implementation that records counter values for assertions.
type recordingMetrics struct {
	mu       sync.Mutex
	counters map[string]float64
}

func newRecordingMetrics() *recordingMetrics {
	return &recordingMetrics{counters: make(map[string]float64)}
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

	r.counters[name+"{"+strings.Join(labelValues, ",")+"}"] += value
}

func (r *recordingMetrics) counter(name string, labelValues ...string) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.counters[name+"{"+strings.Join(labelValues, ",")+"}"]
}

func postAlerts(t *testing.T, server *Server, body []byte) int {
	t.Helper()

	router := gin.New()
	router.POST("/alert", server.handleAlerts)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alert", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	return w.Code
}

func TestServer_HandleAlerts_RejectionMetrics(t *testing.T) {
	t.Parallel()

	twoAlerts := func(t *testing.T) []byte {
		t.Helper()

		body, err := json.Marshal(map[string]any{"alerts": []types.Alert{
			{SlackChannelID: "C123", Header: "a"},
			{SlackChannelID: "C123", Header: "b"},
		}})
		require.NoError(t, err)

		return body
	}

	tests := []struct {
		name        string
		body        func(t *testing.T) []byte
		channelInfo *ChannelInfo
		wantStatus  int
		wantReason  string
		wantCount   float64
	}{
		{
			name:       "unparseable body counts as one",
			body:       func(_ *testing.T) []byte { return []byte("{not json") },
			wantStatus: http.StatusBadRequest,
			wantReason: rejectReasonInvalidBody,
			wantCount:  1,
		},
		{
			name: "validation failure rejects all alerts in the request",
			body: func(t *testing.T) []byte {
				t.Helper()

				body, err := json.Marshal(map[string]any{"alerts": []types.Alert{
					{SlackChannelID: "C123", Header: "a"},
					{SlackChannelID: "C123"},
				}})
				require.NoError(t, err)

				return body
			},
			wantStatus: http.StatusBadRequest,
			wantReason: rejectReasonValidation,
			wantCount:  2,
		},
		{
			name:        "channel not found",
			body:        twoAlerts,
			channelInfo: &ChannelInfo{ChannelExists: false},
			wantStatus:  http.StatusBadRequest,
			wantReason:  rejectReasonChannelNotFound,
			wantCount:   2,
		},
		{
			name:        "channel archived",
			body:        twoAlerts,
			channelInfo: &ChannelInfo{ChannelExists: true, ChannelIsArchived: true},
			wantStatus:  http.StatusBadRequest,
			wantReason:  rejectReasonChannelArchived,
			wantCount:   2,
		},
		{
			name:        "not in channel",
			body:        twoAlerts,
			channelInfo: &ChannelInfo{ChannelExists: true},
			wantStatus:  http.StatusBadRequest,
			wantReason:  rejectReasonNotInChannel,
			wantCount:   2,
		},
		{
			name:        "too many users",
			body:        twoAlerts,
			channelInfo: &ChannelInfo{ChannelExists: true, ManagerIsInChannel: true, UserCount: 5000},
			wantStatus:  http.StatusBadRequest,
			wantReason:  rejectReasonTooManyUsers,
			wantCount:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server, _, channelInfo := newTestServer(t)
			metrics := newRecordingMetrics()
			server.metrics = metrics

			channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
			if tt.channelInfo != nil {
				channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(tt.channelInfo, nil)
			}

			assert.Equal(t, tt.wantStatus, postAlerts(t, server, tt.body(t)))
			assert.InDelta(t, tt.wantCount, metrics.counter(alertsRejectedTotalMetric, tt.wantReason), 0)
		})
	}
}

func TestServer_HandleAlerts_FilteredMetric(t *testing.T) {
	t.Parallel()

	server, queue, channelInfo := newTestServer(t)
	metrics := newRecordingMetrics()
	server.metrics = metrics

	channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
	channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(&ChannelInfo{ChannelExists: true, ManagerIsInChannel: true}, nil)
	queue.On("Send", mock.Anything, "C123", mock.Anything, mock.Anything).Return(nil)

	body, err := json.Marshal(map[string]any{"alerts": []types.Alert{
		{SlackChannelID: "C123", Header: "a", Text: "contains noise", IgnoreIfTextContains: []string{"noise"}},
		{SlackChannelID: "C123", Header: "b", Text: "real"},
	}})
	require.NoError(t, err)

	assert.Equal(t, http.StatusAccepted, postAlerts(t, server, body))
	assert.InDelta(t, 1.0, metrics.counter(alertsFilteredTotalMetric, filterReasonIgnoreTerm), 0)
	queue.AssertNumberOfCalls(t, "Send", 1)
}

func TestServer_HandleAlerts_QueueErrorCountsOnlyUnqueuedAlerts(t *testing.T) {
	t.Parallel()

	server, queue, channelInfo := newTestServer(t)
	metrics := newRecordingMetrics()
	server.metrics = metrics

	channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
	channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(&ChannelInfo{ChannelExists: true, ManagerIsInChannel: true}, nil)
	queue.On("Send", mock.Anything, "C123", mock.Anything, mock.Anything).Return(nil).Once()
	queue.On("Send", mock.Anything, "C123", mock.Anything, mock.Anything).Return(errors.New("queue down"))

	body, err := json.Marshal(map[string]any{"alerts": []types.Alert{
		{SlackChannelID: "C123", Header: "a"},
		{SlackChannelID: "C123", Header: "b"},
		{SlackChannelID: "C123", Header: "c"},
	}})
	require.NoError(t, err)

	assert.Equal(t, http.StatusInternalServerError, postAlerts(t, server, body))
	assert.InDelta(t, 2.0, metrics.counter(alertsRejectedTotalMetric, rejectReasonQueueError), 0)
}

func TestRejectReasonOf(t *testing.T) {
	t.Parallel()

	assert.Equal(t, rejectReasonChannelArchived, rejectReasonOf(newAlertRejection(rejectReasonChannelArchived, "x"), "fallback"))
	assert.Equal(t, rejectReasonNoRoute, rejectReasonOf(newNonRetryableProcessingError("x").withReason(rejectReasonNoRoute), "fallback"))
	assert.Equal(t, rejectReasonNotInChannel, rejectReasonOf(
		newNonRetryableProcessingError("wrapped: %w", newAlertRejection(rejectReasonNotInChannel, "x")).withReason(rejectReasonNotInChannel), "fallback"))
	assert.Equal(t, "fallback", rejectReasonOf(errors.New("plain"), "fallback"))
	assert.Equal(t, "fallback", rejectReasonOf(newNonRetryableProcessingError("no reason"), "fallback"))

	r := newAlertRejection(rejectReasonQueueError, "boom: %w", errors.ErrUnsupported)
	require.ErrorIs(t, r, errors.ErrUnsupported)
	assert.Equal(t, "boom: unsupported operation", r.Error())
}

func TestServer_RunRawAlertConsumer_RejectionMetrics(t *testing.T) {
	t.Parallel()

	server, _, channelInfo := newTestServerWithInitializedSettings(t)
	metrics := newRecordingMetrics()
	server.metrics = metrics

	channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
	channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(&ChannelInfo{ChannelExists: true, ChannelIsArchived: true}, nil)

	consumer := newMockFifoQueueConsumer()
	consumer.On("Receive", mock.Anything, mock.Anything).Return(nil)

	archivedAlert, err := json.Marshal(types.Alert{SlackChannelID: "C123", Header: "x"})
	require.NoError(t, err)

	var acks sync.WaitGroup
	acks.Add(2)

	consumer.SendItem(&types.FifoQueueItem{MessageID: "1", Body: "invalid json", Ack: acks.Done, Nack: func() {}})
	consumer.SendItem(&types.FifoQueueItem{MessageID: "2", Body: string(archivedAlert), Ack: acks.Done, Nack: func() {}})

	ctx, cancel := context.WithCancel(t.Context())

	go func() {
		acks.Wait()
		cancel()
	}()

	_ = server.runRawAlertConsumer(ctx, consumer)

	assert.InDelta(t, 1.0, metrics.counter(alertsRejectedTotalMetric, rejectReasonInvalidBody), 0)
	assert.InDelta(t, 1.0, metrics.counter(alertsRejectedTotalMetric, rejectReasonChannelArchived), 0)
}
