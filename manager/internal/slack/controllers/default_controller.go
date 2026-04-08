package controllers

import (
	"context"

	"github.com/slack-go/slack/socketmode"
	"github.com/slackmgr/types"
)

type defaultController struct {
	clt    SocketModeClient
	logger types.Logger
}

func (c *defaultController) handle(ctx context.Context, evt *socketmode.Event) {
	c.clt.Ack(ctx, evt.Request)

	c.logger.WithField("event_type", evt.Type).Info("Unhandled Slack event type received")
}
