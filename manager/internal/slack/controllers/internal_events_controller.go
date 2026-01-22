package controllers

import (
	"context"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/slack-go/slack/socketmode"
)

type internalEventsController struct {
	logger common.Logger
}

func (c *internalEventsController) handleEventTypeClientInternal(_ context.Context, evt *socketmode.Event, _ SocketModeClient) {
	c.logger.WithField("operation", "slack").WithField("event", evt.Type).Debugf("Slack client internal event")
}

func (c *internalEventsController) handleEventTypeHello(_ context.Context, evt *socketmode.Event, _ SocketModeClient) {
	c.logger.WithField("operation", "slack").WithField("event", evt.Type).Info("Slack socket mode connected")
}

func (c *internalEventsController) handleEventTypeDisconnect(_ context.Context, evt *socketmode.Event, _ SocketModeClient) {
	c.logger.WithField("operation", "slack").WithField("event", evt.Type).Info("Slack socker mode disconnected")
}
