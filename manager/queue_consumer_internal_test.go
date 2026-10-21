package manager

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sliceQueue is a FifoQueue that delivers a fixed set of items and then blocks until the context is cancelled.
type sliceQueue struct {
	items []*types.FifoQueueItem
}

func (q *sliceQueue) Name() string { return "test-queue" }

func (q *sliceQueue) Send(_ context.Context, _, _, _ string) error { return nil }

func (q *sliceQueue) Receive(ctx context.Context, sinkCh chan<- *types.FifoQueueItem) error {
	defer close(sinkCh)

	for _, item := range q.items {
		select {
		case sinkCh <- item:
		case <-ctx.Done():
			return nil
		}
	}

	<-ctx.Done()

	return nil
}

func TestQueueConsumer_UnreadableMessageIsAckedAndCounted(t *testing.T) {
	t.Parallel()

	var badAcks, badNacks, goodAcks atomic.Int32

	queue := &sliceQueue{items: []*types.FifoQueueItem{
		{MessageID: "bad", SlackChannelID: "C1", Body: "{not json", Ack: func() { badAcks.Add(1) }, Nack: func() { badNacks.Add(1) }},
		{MessageID: "good", Body: `{"slackChannelId":"C1","header":"h"}`, Ack: func() { goodAcks.Add(1) }, Nack: func() {}},
	}}

	metrics := newRecordingMetrics()
	recorder, insightsSink := newTestRecorder(t)
	sinkCh := make(chan models.InFlightMessage, 10)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- queueConsumer(ctx, queue, queueTypeAlert, sinkCh, models.NewAlertFromQueueItem, &types.NoopLogger{}, metrics, recorder)
	}()

	select {
	case msg := <-sinkCh:
		alert, ok := msg.(*models.Alert)
		require.True(t, ok)
		assert.Equal(t, "C1", alert.SlackChannelID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the readable message")
	}

	cancel()
	require.NoError(t, <-done)

	assert.Equal(t, int32(1), badAcks.Load(), "unreadable message must be acked")
	assert.Equal(t, int32(0), badNacks.Load())
	assert.Equal(t, int32(0), goodAcks.Load(), "readable message is acked later by the channel manager")
	assert.InDelta(t, 1.0, metrics.counter(queueMessagesProcessedTotalMetric, queueTypeAlert, queueResultUnreadable), 0)

	flush(recorder)

	dropped := insightsSink.ofType(types.InsightsEventAlertDropped)
	require.Len(t, dropped, 1)
	assert.Equal(t, types.InsightsReasonUnreadable, dropped[0].Reason)
	assert.Equal(t, "C1", dropped[0].ChannelID)
}
