package models

import (
	"context"

	commonlib "github.com/peteraglen/slack-manager-common"
)

// InFlightMessage represents a message that is currently being processed.
// It should be acknowledged (Ack) or negatively acknowledged (Nack) once processing is complete.
type InFlightMessage interface {
	Ack(ctx context.Context)
	Nack(ctx context.Context)
}

// InFlightMsgConstructor is a function type that constructs an InFlightMessage from a FifoQueueItem.
// The actual implementation may vary based on the message type (e.g., Alert, Command).
type InFlightMsgConstructor func(queueItem *commonlib.FifoQueueItem) (InFlightMessage, error)
