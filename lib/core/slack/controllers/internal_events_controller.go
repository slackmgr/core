package controllers

import (
	"context"

	"github.com/peteraglen/slack-manager/lib/common"
	"github.com/peteraglen/slack-manager/lib/core/slack/handler"
	"github.com/slack-go/slack/socketmode"
)

type InternalEventsController struct {
	logger common.Logger
}

func NewInternalEventsController(eventhandler *handler.SocketModeHandler, logger common.Logger) *InternalEventsController {
	c := &InternalEventsController{
		logger: logger,
	}

	eventhandler.HandleMultiple(
		[]socketmode.EventType{
			socketmode.EventTypeConnecting,
			socketmode.EventTypeInvalidAuth,
			socketmode.EventTypeConnectionError,
			socketmode.EventTypeConnected,
			socketmode.EventTypeIncomingError,
			socketmode.EventTypeErrorWriteFailed,
			socketmode.EventTypeErrorBadMessage,
		}, c.handleEventTypeClientInternal)

	eventhandler.Handle(socketmode.EventTypeHello, c.handleEventTypeHello)
	eventhandler.Handle(socketmode.EventTypeDisconnect, c.handleEventTypeDisconnect)

	return c
}

func (c *InternalEventsController) handleEventTypeClientInternal(_ context.Context, evt *socketmode.Event, _ *socketmode.Client) {
	c.logger.WithField("operation", "slack").WithField("event", evt.Type).Debugf("Slack client internal event")
}

func (c *InternalEventsController) handleEventTypeHello(_ context.Context, evt *socketmode.Event, _ *socketmode.Client) {
	c.logger.WithField("operation", "slack").WithField("event", evt.Type).Info("Slack socket mode connected")
}

func (c *InternalEventsController) handleEventTypeDisconnect(_ context.Context, evt *socketmode.Event, _ *socketmode.Client) {
	c.logger.WithField("operation", "slack").WithField("event", evt.Type).Info("Slack socker mode disconnected")
}
