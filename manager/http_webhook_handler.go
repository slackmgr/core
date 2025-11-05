package manager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	common "github.com/peteraglen/slack-manager-common"
)

// HTTPWebhookHandler is an implementation of the WebhookHandler interface that sends webhooks via HTTP.
// This is the default webhook handler when no other webhook handlers are configured.
type HTTPWebhookHandler struct {
	client *resty.Client
}

// NewHTTPWebhookHandler creates a new HTTPWebhookHandler.
func NewHTTPWebhookHandler(logger common.Logger) *HTTPWebhookHandler {
	restyLogger := newRestyLogger(logger)

	client := resty.New().
		SetRetryCount(2).
		SetRetryWaitTime(time.Second).
		SetRetryMaxWaitTime(time.Second).
		AddRetryCondition(webhookRetryPolicy).
		SetLogger(restyLogger).
		SetTimeout(3 * time.Second)

	return &HTTPWebhookHandler{
		client: client,
	}
}

// WithRequestTimeout sets the timeout for HTTP requests made by the webhook handler.
// The default timeout is 3 seconds.
func (h *HTTPWebhookHandler) WithRequestTimeout(timeout time.Duration) {
	h.client.SetTimeout(timeout)
}

// ShouldHandleWebhook returns true if the target is an HTTP or HTTPS URL.
// If other webhooks should handle certain URLs (e.g. SQS queue URLs), they must be registred *before* this handler.
func (h *HTTPWebhookHandler) ShouldHandleWebhook(_ context.Context, target string) bool {
	return strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
}

// HandleWebhook sends the webhook data to the target URL via an HTTP POST request.
// It expects a successful HTTP status code (2xx) in response.
func (h *HTTPWebhookHandler) HandleWebhook(ctx context.Context, target string, data *common.WebhookCallback, logger common.Logger) error {
	response, err := h.client.R().SetContext(ctx).SetBody(data).Post(target)
	if err != nil {
		return fmt.Errorf("webhook POST %s failed: %w", response.Request.URL, err)
	}

	logger.Debugf("Webhook POST %s %s", response.Request.URL, response.Status())

	if !response.IsSuccess() {
		return fmt.Errorf("webhook POST %s failed with status code %d", response.Request.URL, response.StatusCode())
	}

	return nil
}

// webhookRetryPolicy defines the retry policy for webhook HTTP requests.
// It retries on network errors and on HTTP 429 and 5xx status codes.
func webhookRetryPolicy(r *resty.Response, err error) bool {
	if err != nil {
		return !errors.Is(err, context.Canceled) &&
			!errors.Is(err, context.DeadlineExceeded) &&
			!strings.Contains(err.Error(), "no such host")
	}

	// Retry on 429 and 5xx errors
	return r.StatusCode() == 429 || r.StatusCode() >= 500
}
