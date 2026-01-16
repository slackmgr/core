package controllers

import (
	"context"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

type eventsAPIController struct {
	logger common.Logger
}

func (c *eventsAPIController) handleEventTypeEventsAPI(_ context.Context, evt *socketmode.Event, clt *socketmode.Client) {
	ack(evt, clt)

	apiEvent, _ := evt.Data.(slackevents.EventsAPIEvent)

	c.logger.WithField("operation", "slack").WithField("envelope_id", evt.Request.EnvelopeID).WithField("event_type", apiEvent.Type).WithField("event_inner_type", apiEvent.InnerEvent.Type).Info("Unhandled events API event")
}
