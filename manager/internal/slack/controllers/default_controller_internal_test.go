package controllers

import (
	"context"
	"testing"

	"github.com/slack-go/slack/socketmode"
	"github.com/stretchr/testify/mock"
)

func TestDefaultController_handle(t *testing.T) {
	t.Parallel()

	t.Run("acks event and logs unhandled type", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Info", "Unhandled Slack event type received").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		controller := &defaultController{
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    "unknown_event_type",
			Request: &req,
		}

		controller.handle(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("handles nil request gracefully", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Info", "Unhandled Slack event type received").Once()

		client := newMockSocketModeClient()
		// Ack should not be called when Request is nil

		controller := &defaultController{
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    "unknown_event_type",
			Request: nil,
		}

		controller.handle(context.Background(), evt, client)

		client.AssertNotCalled(t, "Ack", mock.Anything, mock.Anything)
		logger.AssertExpectations(t)
	})
}
