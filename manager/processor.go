package manager

import (
	"context"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/peteraglen/slack-manager/manager/internal/models"
)

func processor(ctx context.Context, coordinator *coordinator, alertCh <-chan models.Message, commandCh <-chan models.Message, extenderCh chan<- models.Message, logger common.Logger) error {
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

			alert := msg.(*models.Alert)

			if err := coordinator.AddAlert(ctx, alert); err != nil {
				logger.WithFields(alert.LogFields()).Errorf("Failed to process alert %s: %s", alert.ID, err)
				continue
			}

			if err := internal.TrySend(ctx, msg, extenderCh); err != nil {
				return err
			}
		case msg, ok := <-commandCh:
			if !ok {
				return nil
			}

			cmd := msg.(*models.Command)

			if err := coordinator.AddCommand(ctx, cmd); err != nil {
				logger.WithFields(cmd.LogFields()).Errorf("Failed to process %s command: %s", cmd.Action, err)
				continue
			}

			if err := internal.TrySend(ctx, msg, extenderCh); err != nil {
				return err
			}
		}
	}
}
