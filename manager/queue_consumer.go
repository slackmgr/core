package manager

import (
	"context"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	"golang.org/x/sync/errgroup"
)

func queueConsumer(ctx context.Context, queue FifoQueue, sinkCh chan<- models.Message, unmarshalFunc models.UnmarshalFunc, logger common.Logger) error {
	logger.Debug("queueConsumer started")
	defer logger.Debug("queueConsumer exited")

	defer close(sinkCh)

	queueCh := make(chan *common.FifoQueueItem, 100)

	errg, ctx := errgroup.WithContext(ctx)

	errg.Go(func() error {
		return queue.Receive(ctx, queueCh)
	})

	errg.Go(func() error {
		for _item := range queueCh {
			item := _item

			logger = logger.WithField("message_id", item.MessageID).WithField("group_id", item.GroupID)
			logger.Debug("Message received")

			if item.Ack == nil {
				logger.Error("Message ack function is nil")
				continue
			}

			message, err := unmarshalFunc(item)
			if err != nil {
				logger.Errorf("Failed to unmarshal message: %s", err)
				continue
			}

			message.SetAckFunc(item.Ack)
			message.SetExtendVisibilityFunc(item.ExtendVisibility)

			if err := internal.TrySend(ctx, message, sinkCh); err != nil {
				return err
			}
		}

		return nil
	})

	return errg.Wait()
}
