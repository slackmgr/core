package common

import (
	"context"

	commonlib "github.com/peteraglen/slack-manager-common"
)

type FifoQueueConsumer interface {
	Receive(ctx context.Context, sinkCh chan<- *commonlib.QueueItem) error
}
