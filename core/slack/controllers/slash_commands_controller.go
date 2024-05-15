package controllers

import (
	"context"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/core/slack/handler"
	"github.com/slack-go/slack/socketmode"
)

type SlashCommandsController struct {
	logger common.Logger
}

func NewSlashCommandsController(eventhandler *handler.SocketModeHandler, logger common.Logger) *SlashCommandsController {
	c := &SlashCommandsController{
		logger: logger,
	}

	eventhandler.Handle(socketmode.EventTypeSlashCommand, c.handleEventTypeSlashCommands)

	return c
}

func (c *SlashCommandsController) handleEventTypeSlashCommands(_ context.Context, evt *socketmode.Event, clt *socketmode.Client) {
	ack(evt, clt)

	c.logger.WithField("operation", "slack").WithField("event", evt.Type).WithField("envelope_id", evt.Request.EnvelopeID).Debugf("Slash commands event")
}
