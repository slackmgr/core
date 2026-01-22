package controllers

import (
	"context"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/slack-go/slack/socketmode"
)

type slashCommandsController struct {
	logger common.Logger
}

func (c *slashCommandsController) handleEventTypeSlashCommands(_ context.Context, evt *socketmode.Event, clt SocketModeClient) {
	ack(evt, clt)

	c.logger.WithField("operation", "slack").WithField("event", evt.Type).WithField("envelope_id", evt.Request.EnvelopeID).Debugf("Slash commands event")
}
