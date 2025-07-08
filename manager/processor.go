package manager

import (
	"context"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/peteraglen/slack-manager/manager/internal/models"
)

func processor(ctx context.Context, coordinator *coordinator, alertCh <-chan models.Message, commandCh <-chan models.Message, extenderCh chan<- models.Message, logger common.Logger) error {
	logger.Info("Message processor started")
	defer logger.Info("Message processor exited")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-alertCh:
			if !ok {
				return nil
			}

			alert, ok := msg.(*models.Alert)
			if !ok {
				logger.Errorf("Invalid message type %T on alert channel", msg)
				continue
			}

			if err := coordinator.AddAlert(ctx, alert); err != nil {
				msg.MarkAsFailed()
				logger.WithFields(alert.LogFields()).Errorf("Failed to process alert %s: %s", alert.UniqueID(), err)
				continue
			}

			if err := internal.TrySend(ctx, msg, extenderCh); err != nil {
				return err
			}
		case msg, ok := <-commandCh:
			if !ok {
				return nil
			}

			cmd, ok := msg.(*models.Command)
			if !ok {
				logger.Errorf("Invalid message type %T on command channel", msg)
				continue
			}

			if err := coordinator.AddCommand(ctx, cmd); err != nil {
				msg.MarkAsFailed()
				logger.WithFields(cmd.LogFields()).Errorf("Failed to process %s command: %s", cmd.Action, err)
				continue
			}

			if err := internal.TrySend(ctx, msg, extenderCh); err != nil {
				return err
			}
		}
	}
}
