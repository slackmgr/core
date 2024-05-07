package common

import (
	"context"
	"time"
)

type FifoQueueConsumer interface {
	Receive(ctx context.Context, sinkCh chan<- *QueueItem) error
}

type QueueItem struct {
	MessageID         string
	GroupID           string
	ReceiveTimestamp  time.Time
	VisibilityTimeout time.Duration
	Body              string
	Ack               func(ctx context.Context)
	Extend            func(ctx context.Context)
}
