package internal

import (
	"context"

	"github.com/slack-go/slack/socketmode"
)

// SocketModeClientWrapper is a wrapper around the slack-go/socketmode.Client.
// It is primarily created to wrap the Events channel in a function, which allows
// using interfaces more easily.
type SocketModeClientWrapper struct {
	client *socketmode.Client
}

// NewSocketModeClientWrapper creates a new instance of SocketModeClientWrapper.
func NewSocketModeClientWrapper(client *socketmode.Client) *SocketModeClientWrapper {
	return &SocketModeClientWrapper{
		client: client,
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

// Ack acknowledges the given socketmode request with an optional payload.
func (s *SocketModeClientWrapper) Ack(req socketmode.Request, payload ...any) {
	s.client.Ack(req, payload...)
}
