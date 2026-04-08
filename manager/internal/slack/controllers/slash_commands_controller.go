package controllers

import (
	"context"

	"github.com/slack-go/slack/socketmode"
	"github.com/slackmgr/types"
)

type slashCommandsController struct {
	clt    SocketModeClient
	logger types.Logger
}

func (c *slashCommandsController) handleEventTypeSlashCommands(ctx context.Context, evt *socketmode.Event) {
	c.clt.Ack(ctx, evt.Request)

	c.logger.WithField("operation", "slack").WithField("event", evt.Type).WithField("envelope_id", evt.Request.EnvelopeID).Debugf("Slash commands event")
}
