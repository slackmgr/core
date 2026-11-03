package manager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
)

// webhookNotifyTimeout is the maximum time spent informing the user about a webhook outcome.
const webhookNotifyTimeout = 10 * time.Second

// webhookResultSendTimeout is the maximum time spent sending a webhook result command to the command queue.
const webhookResultSendTimeout = 10 * time.Second

// webhookFailureReason maps a webhook handler error to a user-facing failure reason, and whether the user may succeed
// by trying again. The reason never includes the error message itself, since it may contain internal details such as
// the target URL.
func webhookFailureReason(err error) (string, bool) {
	if _, ok := errors.AsType[*WebhookRejectedError](err); ok {
		return "the request was rejected by the receiving system", false
	}

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

// isWebhookTargetError returns true if err is caused by the webhook target, i.e. a non-2xx response, a timeout or
// a network error, rather than by the manager itself.
func isWebhookTargetError(err error) bool {
	if _, ok := errors.AsType[*WebhookRejectedError](err); ok {
		return true
	}

	if _, ok := errors.AsType[*WebhookHTTPError](err); ok {
		return true
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	_, ok := errors.AsType[net.Error](err)

	return ok
}

// notifyWebhookOutcome informs the user who clicked a webhook button about the outcome.
// It is not cancelled with ctx, so that the user is informed also during shutdown, but it is bounded by [webhookNotifyTimeout].
func notifyWebhookOutcome(ctx context.Context, slackClient SlackClient, outcome *models.WebhookOutcome) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), webhookNotifyTimeout)
	defer cancel()

	slackClient.NotifyWebhookOutcome(ctx, outcome)
}

// callWebhookHandler calls [WebhookResultHandler.HandleWebhookWithResult] if the handler implements it, and
// [WebhookHandler.HandleWebhook] otherwise. A returned result is cleaned and validated; invalid results are logged
// and ignored (the webhook was still delivered).
func callWebhookHandler(ctx context.Context, handler WebhookHandler, target string, data *types.WebhookCallback, logger types.Logger) (*types.WebhookResult, error) {
	resultHandler, ok := handler.(WebhookResultHandler)
	if !ok {
		return nil, handler.HandleWebhook(ctx, target, data, logger)
	}

	result, err := resultHandler.HandleWebhookWithResult(ctx, target, data, logger)
	if err != nil || result == nil {
		return nil, err
	}

	result.Clean()

	if err := result.Validate(); err != nil {
		logger.Infof("Ignoring invalid webhook result: %s", err)
		return nil, nil
	}

	return result, nil
}

// webhookRejectedMessage returns the trimmed message of a rejection, or an empty string (with an info log) if the
// message is too long.
func webhookRejectedMessage(rejected *WebhookRejectedError, logger types.Logger) string {
	message := strings.TrimSpace(rejected.Message)

	if len(message) > types.MaxWebhookResultMessageLength {
		logger.Infof("Ignoring webhook rejection message longer than %d characters", types.MaxWebhookResultMessageLength)
		return ""
	}

	return message
}
