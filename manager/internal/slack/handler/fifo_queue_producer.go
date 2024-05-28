package handler

import "context"

type FifoQueueProducer interface {
	Send(ctx context.Context, slackChannelID, dedupID, body string) error
}
