package controllers

import (
	"context"

	"github.com/slack-go/slack/socketmode"
	"github.com/slackmgr/types"
)

type slashCommandsController struct {
	logger types.Logger
}

func (c *slashCommandsController) handleEventTypeSlashCommands(_ context.Context, evt *socketmode.Event, clt SocketModeClient) {
	ack(evt, clt)

	c.logger.WithField("operation", "slack").WithField("event", evt.Type).WithField("envelope_id", evt.Request.EnvelopeID).Debugf("Slash commands event")
}
