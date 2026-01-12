package manager

import (
	"context"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	"golang.org/x/sync/errgroup"
)

func queueConsumer(ctx context.Context, queue FifoQueue, sinkCh chan<- models.InFlightMessage, msgConstructor models.InFlightMsgConstructor, logger common.Logger) error {
	logger.WithField("queue_name", queue.Name()).Info("Queue consumer started")
	defer logger.WithField("queue_name", queue.Name()).Info("Queue consumer exited")

	defer close(sinkCh)

	queueCh := make(chan *common.FifoQueueItem, 100)

	errg, ctx := errgroup.WithContext(ctx)

	errg.Go(func() error {
		return queue.Receive(ctx, queueCh)
	})

	errg.Go(func() error {
		for _item := range queueCh {
			item := _item

			itemLogger := logger.WithField("message_id", item.MessageID).WithField("channel_id", item.SlackChannelID)
			itemLogger.Debug("Message received")

			message, err := msgConstructor(item)
			if err != nil {
				itemLogger.Errorf("Failed to unmarshal message: %s", err)
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
