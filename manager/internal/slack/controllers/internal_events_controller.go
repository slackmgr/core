package controllers

import (
	"context"

	"github.com/slack-go/slack/socketmode"
	"github.com/slackmgr/types"
)

type internalEventsController struct {
	logger types.Logger
}

func (c *internalEventsController) handleEventTypeClientInternal(_ context.Context, evt *socketmode.Event, _ SocketModeClient) {
	c.logger.WithField("operation", "slack").WithField("event", evt.Type).Debugf("Slack client internal event")
}

func (c *internalEventsController) handleEventTypeHello(_ context.Context, evt *socketmode.Event, _ SocketModeClient) {
	c.logger.WithField("operation", "slack").WithField("event", evt.Type).Info("Slack socket mode connected")
}

func (c *internalEventsController) handleEventTypeDisconnect(_ context.Context, evt *socketmode.Event, _ SocketModeClient) {
	c.logger.WithField("operation", "slack").WithField("event", evt.Type).Info("Slack socket mode disconnected")
}
