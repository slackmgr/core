package manager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/slackmgr/core/internal/models"
)

// webhookNotifyTimeout is the maximum time spent informing the user about a webhook outcome.
const webhookNotifyTimeout = 10 * time.Second

// webhookFailureReason maps a webhook handler error to a user-facing failure reason, and whether the user may succeed
// by trying again. The reason never includes the error message itself, since it may contain internal details such as
// the target URL.
func webhookFailureReason(err error) (string, bool) {
	if httpErr, ok := errors.AsType[*WebhookHTTPError](err); ok {
		code := httpErr.StatusCode

		switch {
		case code == http.StatusTooManyRequests:
			return fmt.Sprintf("the receiving system is busy (HTTP %d)", code), true
		case code >= 400 && code < 500:
			return fmt.Sprintf("the request was rejected by the receiving system (HTTP %d)", code), false
		default:
			return fmt.Sprintf("the receiving system returned an error (HTTP %d)", code), true
		}
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return models.WebhookReasonTimeout, true
	}

	if netErr, ok := errors.AsType[net.Error](err); ok {
		if netErr.Timeout() {
			return models.WebhookReasonTimeout, true
		}

		return models.WebhookReasonUnreachable, true
	}

	return models.WebhookReasonDeliveryFailed, true
}

// notifyWebhookOutcome informs the user who clicked a webhook button about the outcome.
// It is not cancelled with ctx, so that the user is informed also during shutdown, but it is bounded by [webhookNotifyTimeout].
func notifyWebhookOutcome(ctx context.Context, slackClient SlackClient, outcome *models.WebhookOutcome) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), webhookNotifyTimeout)
	defer cancel()

	slackClient.NotifyWebhookOutcome(ctx, outcome)
}
