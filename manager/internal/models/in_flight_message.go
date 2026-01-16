package models

import (
	commonlib "github.com/peteraglen/slack-manager-common"
)

// InFlightMessage represents a message that is currently being processed.
// It should be acknowledged (Ack) or negatively acknowledged (Nack) once processing is complete.
//
// Ack and Nack do not accept a context parameter because acknowledgment is a commitment
// that must complete regardless of the caller's context state. Each queue implementation
// is responsible for managing its own timeouts and retry logic internally.
type InFlightMessage interface {
	Ack()
	Nack()
}

// InFlightMsgConstructor is a function type that constructs an InFlightMessage from a FifoQueueItem.
// The actual implementation may vary based on the message type (e.g., Alert, Command).
type InFlightMsgConstructor func(queueItem *commonlib.FifoQueueItem) (InFlightMessage, error)
