package manager

import (
	"context"

	"github.com/slackmgr/types"
)

// FifoQueue is an interface for interacting with a fifo queue.
type FifoQueue interface {
	// Name returns the name of the queue.
	Name() string

	// Send sends a single message to the queue.
	//
	// slackChannelID is the Slack channel to which the message belongs.
	// A queue implementation should use this value to partition the queue (i.e. group ID in an AWS SQS Fifo queue),
	// but it is not required.
	//
	// dedupID is a unique identifier for the message.
	// A queue implementation should use this value to deduplicate messages, but it is not required.
	//
	// body is the json formatted message body.
	Send(ctx context.Context, slackChannelID, dedupID, body string) error

	// Receive receives messages from the queue, until the context is cancelled.
	// Messages are sent to the provided channel.
	// The channel must be closed by the implementation before returning, typically when the context is cancelled or a fatal error occurs.
	Receive(ctx context.Context, sinkCh chan<- *types.FifoQueueItem) error
}
