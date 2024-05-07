package manager

import (
	"context"
	"fmt"

	"github.com/peteraglen/slack-manager/lib/common"
)

func errorHandler(ctx context.Context, beefyBulkErrCh <-chan error, logger common.Logger) error {
	logger.Debug("errorHandler started")
	defer logger.Debug("errorHandler exited")

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-beefyBulkErrCh:
		return fmt.Errorf("beefy bulk failed with error: %w", err)
	}
}
