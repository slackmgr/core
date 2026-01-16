package controllers

import (
	"context"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/slack-go/slack/socketmode"
)

type defaultController struct {
	logger common.Logger
}

func (c *defaultController) handle(_ context.Context, evt *socketmode.Event, clt *socketmode.Client) {
	ack(evt, clt)

	c.logger.WithField("event_type", evt.Type).Info("Unhandled Slack event type received")
}
