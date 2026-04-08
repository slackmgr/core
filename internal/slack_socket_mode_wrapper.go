package internal

import (
	"context"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"github.com/slackmgr/types"
)

// SocketModeClientWrapper is a wrapper around the slack-go/socketmode.Client.
type SocketModeClientWrapper struct {
	client *socketmode.Client
	logger types.Logger
}

// NewSocketModeClientWrapper creates a new instance of SocketModeClientWrapper.
func NewSocketModeClientWrapper(client *socketmode.Client, logger types.Logger) *SocketModeClientWrapper {
	return &SocketModeClientWrapper{
		client: client,
		logger: logger,
	}
}

// RunContext is a blocking function that connects the Slack Socket Mode API and handles all incoming
// requests and outgoing responses.
func (s *SocketModeClientWrapper) RunContext(ctx context.Context) error {
	return s.client.RunContext(ctx)
}

// Events returns the channel that receives incoming socketmode events.
func (s *SocketModeClientWrapper) Events() chan socketmode.Event {
	return s.client.Events
}

// Ack acknowledges the given socketmode request.
func (s *SocketModeClientWrapper) Ack(ctx context.Context, req *socketmode.Request) {
	s.AckWithPayload(ctx, req, nil)
}

// AckWithFieldErrorMsg acknowledges the given socketmode request with a field error message.
func (s *SocketModeClientWrapper) AckWithFieldErrorMsg(ctx context.Context, evt *socketmode.Event, fieldName, errMsg string) {
	errors := map[string]string{fieldName: errMsg}
	s.AckWithPayload(ctx, evt.Request, slack.NewErrorsViewSubmissionResponse(errors))
}

// AckWithPayload acknowledges the given socketmode request with a payload.
func (s *SocketModeClientWrapper) AckWithPayload(ctx context.Context, req *socketmode.Request, payload any) {
	if req == nil {
		return
	}

	if err := s.client.AckCtx(ctx, req.EnvelopeID, payload); err != nil {
		s.logger.Errorf("Failed to acknowledge socketmode request: %v", err)
	}
}
