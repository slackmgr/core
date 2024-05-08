package controllers

import (
	"context"

	"github.com/peteraglen/slack-manager/common"
	"github.com/peteraglen/slack-manager/core/slack/handler"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

type EventsAPIController struct {
	logger common.Logger
}

func NewEventsAPIController(eventhandler *handler.SocketModeHandler, logger common.Logger) *EventsAPIController {
	c := &EventsAPIController{
		logger: logger,
	}

	eventhandler.Handle(socketmode.EventTypeEventsAPI, c.handleEventTypeEventsAPI)

	return c
}

func (c *EventsAPIController) handleEventTypeEventsAPI(_ context.Context, evt *socketmode.Event, clt *socketmode.Client) {
	ack(evt, clt)

	apiEvent, _ := evt.Data.(slackevents.EventsAPIEvent)

	c.logger.WithField("operation", "slack").WithField("envelope_id", evt.Request.EnvelopeID).WithField("event_type", apiEvent.Type).WithField("event_inner_type", apiEvent.InnerEvent.Type).Info("Unhandled events API event")
}
