package manager

import (
	"context"

	"github.com/peteraglen/slack-manager/lib/common"
	"github.com/peteraglen/slack-manager/lib/core/models"
	"github.com/peteraglen/slack-manager/lib/internal"
)

func processor(ctx context.Context, coordinator Coordinator, alertCh <-chan models.Message, commandCh <-chan models.Message, extenderCh chan<- models.Message, logger common.Logger) error {
	logger.Debug("processor started")
	defer logger.Debug("processor exited")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-alertCh:
			if !ok {
				return nil
			}

			coordinator.AddAlert(ctx, msg.(*models.Alert))

			if err := internal.TrySend(ctx, msg, extenderCh); err != nil {
				return err
			}
		case msg, ok := <-commandCh:
			if !ok {
				return nil
			}

			coordinator.AddCommand(ctx, msg.(*models.Command))

			if err := internal.TrySend(ctx, msg, extenderCh); err != nil {
				return err
			}
		}
	}
}
