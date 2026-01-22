package controllers

import (
	"context"
	"testing"

	"github.com/slack-go/slack/socketmode"
	"github.com/stretchr/testify/mock"
)

func TestInternalEventsController_handleEventTypeClientInternal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		eventType socketmode.EventType
	}{
		{"connecting event", socketmode.EventTypeConnecting},
		{"invalid auth event", socketmode.EventTypeInvalidAuth},
		{"connection error event", socketmode.EventTypeConnectionError},
		{"connected event", socketmode.EventTypeConnected},
		{"incoming error event", socketmode.EventTypeIncomingError},
		{"error write failed event", socketmode.EventTypeErrorWriteFailed},
		{"error bad message event", socketmode.EventTypeErrorBadMessage},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger := &mockLogger{}
			logger.On("Debugf", "Slack client internal event", mock.Anything).Once()

			controller := &internalEventsController{
				logger: logger,
			}

			evt := &socketmode.Event{
				Type: tc.eventType,
			}

			controller.handleEventTypeClientInternal(context.Background(), evt, nil)

			logger.AssertExpectations(t)
		})
	}
}

func TestInternalEventsController_handleEventTypeHello(t *testing.T) {
	t.Parallel()

	logger := &mockLogger{}
	logger.On("Info", "Slack socket mode connected").Once()

	controller := &internalEventsController{
		logger: logger,
	}

	evt := &socketmode.Event{
		Type: socketmode.EventTypeHello,
	}

	controller.handleEventTypeHello(context.Background(), evt, nil)

	logger.AssertExpectations(t)
}

func TestInternalEventsController_handleEventTypeDisconnect(t *testing.T) {
	t.Parallel()

	logger := &mockLogger{}
	logger.On("Info", "Slack socket mode disconnected").Once()

	controller := &internalEventsController{
		logger: logger,
	}

	evt := &socketmode.Event{
		Type: socketmode.EventTypeDisconnect,
	}

	controller.handleEventTypeDisconnect(context.Background(), evt, nil)

	logger.AssertExpectations(t)
}
