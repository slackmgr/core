package controllers

import (
	"context"
	"testing"

	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func TestEventsAPIController_handleEventTypeEventsAPI(t *testing.T) {
	t.Parallel()

	t.Run("acks event and logs unhandled event type", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Info", "Unhandled events API event").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		controller := &eventsAPIController{
			logger: logger,
		}

		innerEvent := slackevents.EventsAPIInnerEvent{
			Type: "some_inner_type",
		}
		apiEvent := slackevents.EventsAPIEvent{
			Type:       "some_event_type",
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.handleEventTypeEventsAPI(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("handles non-EventsAPIEvent data gracefully", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Info", "Unhandled events API event").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		controller := &eventsAPIController{
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    "invalid data type",
			Request: &req,
		}

		controller.handleEventTypeEventsAPI(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})
}
