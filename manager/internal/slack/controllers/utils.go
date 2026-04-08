package controllers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/slackmgr/core/manager/internal/models"
)

func sendCommand(ctx context.Context, fifoQueue FifoQueueProducer, cmd *models.Command) error {
	body, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal command: %w", err)
	}

	return fifoQueue.Send(ctx, cmd.SlackChannelID, cmd.DedupID(), string(body))
}
