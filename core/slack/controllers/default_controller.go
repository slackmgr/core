package controllers

import (
	"context"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/core/slack/handler"
	"github.com/slack-go/slack/socketmode"
)

type DefaultController struct {
	logger common.Logger
}

func NewDefaultController(eventhandler *handler.SocketModeHandler, logger common.Logger) *DefaultController {
	c := &DefaultController{
		logger: logger,
	}

	eventhandler.HandleDefault(c.handle)

	return c
}

func (c *DefaultController) handle(_ context.Context, evt *socketmode.Event, clt *socketmode.Client) {
	ack(evt, clt)

	c.logger.WithField("event_type", evt.Type).Info("Unhandled Slack event type received")
}
