package controllers

import (
	"context"

	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"github.com/slackmgr/types"
)

type eventsAPIController struct {
	clt    SocketModeClient
	logger types.Logger
}

func (c *eventsAPIController) handleEventTypeEventsAPI(ctx context.Context, evt *socketmode.Event) {
	c.clt.Ack(ctx, evt.Request)

	apiEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		c.logger.WithField("operation", "slack").WithField("envelope_id", evt.Request.EnvelopeID).Errorf("Failed to cast EventsAPIEvent, got: %T", evt.Data)
		return
	}

	c.logger.WithField("operation", "slack").WithField("envelope_id", evt.Request.EnvelopeID).WithField("event_type", apiEvent.Type).WithField("event_inner_type", apiEvent.InnerEvent.Type).Info("Unhandled events API event")
}
