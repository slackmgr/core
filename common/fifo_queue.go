package common

import "context"

type FifoQueue interface {
	Send(ctx context.Context, groupID, dedupID, body string) error
}
