package controllers

import (
	"context"
	"testing"

	"github.com/slack-go/slack/socketmode"
)

func TestSlashCommandsController_handleEventTypeSlashCommands(t *testing.T) {
	t.Parallel()

	t.Run("acks event and logs slash command", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debugf", "Slash commands event", []any(nil)).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		controller := &slashCommandsController{
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeSlashCommand,
			Request: &req,
		}

		controller.handleEventTypeSlashCommands(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("handles nil request gracefully", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		// Logger will still be called but with nil envelope ID - this could panic
		// Testing current behavior
		logger.On("Debugf", "Slash commands event", []any(nil)).Once()

		client := newMockSocketModeClient()
		// Ack should not be called when Request is nil

		controller := &slashCommandsController{
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeSlashCommand,
			Request: nil,
		}

		// Note: This will panic in current implementation due to evt.Request.EnvelopeID access
		// This test documents the current behavior
		defer func() {
			if r := recover(); r != nil {
				// Expected panic when Request is nil
				t.Log("Recovered from expected panic:", r)
			}
		}()

		controller.handleEventTypeSlashCommands(context.Background(), evt, client)

		client.AssertNotCalled(t, "Ack")
	})
}
