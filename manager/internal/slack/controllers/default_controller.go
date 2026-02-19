package controllers

import (
	"context"

	"github.com/slack-go/slack/socketmode"
	"github.com/slackmgr/types"
)

type defaultController struct {
	logger types.Logger
}

func (c *defaultController) handle(_ context.Context, evt *socketmode.Event, clt SocketModeClient) {
	ack(evt, clt)

	c.logger.WithField("event_type", evt.Type).Info("Unhandled Slack event type received")
}
