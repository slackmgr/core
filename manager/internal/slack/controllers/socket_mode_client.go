package controllers

import (
	"context"

	"github.com/slack-go/slack/socketmode"
)

// SocketModeClient defines the interface for the SocketMode client used by the handlers.
type SocketModeClient interface {
	Ack(ctx context.Context, req *socketmode.Request)
	AckWithPayload(ctx context.Context, req *socketmode.Request, payload any)
	AckWithFieldErrorMsg(ctx context.Context, evt *socketmode.Event, fieldName, errMsg string)
	RunContext(ctx context.Context) error
	Events() chan socketmode.Event
}
