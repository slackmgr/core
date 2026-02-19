package controllers

import (
	"context"

	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"github.com/slackmgr/types"
)

type eventsAPIController struct {
	logger types.Logger
}

func (c *eventsAPIController) handleEventTypeEventsAPI(_ context.Context, evt *socketmode.Event, clt SocketModeClient) {
	ack(evt, clt)

	apiEvent, _ := evt.Data.(slackevents.EventsAPIEvent)

	c.logger.WithField("operation", "slack").WithField("envelope_id", evt.Request.EnvelopeID).WithField("event_type", apiEvent.Type).WithField("event_inner_type", apiEvent.InnerEvent.Type).Info("Unhandled events API event")
}
