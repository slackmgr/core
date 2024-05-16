package handler

import "context"

type FifoQueueProducer interface {
	Send(ctx context.Context, groupID, dedupID, body string) error
}
