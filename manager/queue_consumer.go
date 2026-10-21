package manager

import (
	"context"

	"github.com/slackmgr/core/internal"
	"github.com/slackmgr/core/manager/internal/models"
	"github.com/slackmgr/types"
	"golang.org/x/sync/errgroup"
)

// queueConsumer reads items from the queue, converts them to in-flight messages and forwards them to sinkCh.
// Items that cannot be converted (e.g. malformed JSON) are acked and discarded, since retrying cannot succeed.
// Leaving them unacknowledged would make the queue redeliver them indefinitely, and for queues with strict
// per-channel ordering (Redis Streams) block all later messages in the same channel.
func queueConsumer(ctx context.Context, queue FifoQueue, queueType string, sinkCh chan<- models.InFlightMessage, msgConstructor models.InFlightMsgConstructor, logger types.Logger, metrics types.Metrics) error {
	logger.WithField("queue_name", queue.Name()).Info("Queue consumer started")
	defer logger.WithField("queue_name", queue.Name()).Info("Queue consumer exited")

	defer close(sinkCh)

	registerQueueMetrics(metrics)

	queueCh := make(chan *types.FifoQueueItem, 50)

	errg, ctx := errgroup.WithContext(ctx)

	errg.Go(func() error {
		return queue.Receive(ctx, queueCh)
	})

	errg.Go(func() error {
		for item := range queueCh {
			logger.WithField("message_id", item.MessageID).WithField("channel_id", item.SlackChannelID).Debug("Message received")

			message, err := msgConstructor(item)
			if err != nil {
				logger.WithField("message_id", item.MessageID).WithField("channel_id", item.SlackChannelID).Errorf("Discarding unreadable message: %s", err)
				item.Ack()
				metrics.CounterInc(queueMessagesProcessedTotalMetric, queueType, queueResultUnreadable)
				continue
			}

			if err := internal.TrySend(ctx, message, sinkCh); err != nil {
				return err
			}
		}

		return nil
	})

	return errg.Wait()
}
