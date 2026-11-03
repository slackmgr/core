package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/slackmgr/core/config"
	"github.com/slackmgr/types"
)

// WebhookHTTPError is returned (wrapped) by [HTTPWebhookHandler.HandleWebhook] when the webhook target responds
// with a non-2xx HTTP status code. Custom [WebhookHandler] implementations may return (wrap) it as well, to have
// the status code included in the failure message shown to the user who clicked the webhook button.
type WebhookHTTPError struct {
	// StatusCode is the HTTP status code returned by the webhook target.
	StatusCode int
}

// Error implements the error interface.
func (e *WebhookHTTPError) Error() string {
	return fmt.Sprintf("webhook target responded with HTTP status code %d", e.StatusCode)
}

// WebhookRejectedError is returned (wrapped) by [WebhookResultHandler.HandleWebhookWithResult] when the receiving
// system rejected the request, e.g. because of invalid input. Message is shown to the user who clicked the webhook
// button, instead of a generic failure message.
//
// [HTTPWebhookHandler] returns it for 4xx responses with a [types.WebhookResponse] body. It unwraps to a
// [WebhookHTTPError] when StatusCode is set.
type WebhookRejectedError struct {
	// StatusCode is the HTTP status code returned by the webhook target. 0 for non-HTTP handlers.
	StatusCode int

	// Message is an optional message for the user who clicked the webhook button, shown as plain text.
	// Maximum length: [types.MaxWebhookResultMessageLength] characters (longer messages are ignored).
	Message string
}

// Error implements the error interface.
func (e *WebhookRejectedError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("webhook target rejected the request with HTTP status code %d", e.StatusCode)
	}

	return "webhook target rejected the request"
}

// Unwrap returns a [WebhookHTTPError] with the same status code, or nil if StatusCode is 0.
func (e *WebhookRejectedError) Unwrap() error {
	if e.StatusCode == 0 {
		return nil
	}

	return &WebhookHTTPError{StatusCode: e.StatusCode}
}

// maxWebhookResponseBodySize is the maximum size of a webhook response body that is parsed as a [types.WebhookResponse].
// Larger bodies are ignored (but don't fail the webhook).
const maxWebhookResponseBodySize = 16 * 1024

// HTTPWebhookHandler is an implementation of the [WebhookHandler] and [WebhookResultHandler] interfaces that sends
// webhooks via HTTP. The manager always registers one as the last (fallback) handler, with the timeout from
// [config.ManagerConfig.WebhookTimeoutSeconds]. Register a separate instance with [Manager.RegisterWebhookHandler]
// to override it, e.g. to use a different timeout.
//
// The target may respond with a [types.WebhookResponse] body (content type application/json, max 16 KiB), to give
// feedback to the user who clicked the button. A 2xx response means the request was accepted, a 4xx response with
// a result body means it was rejected (see [WebhookRejectedError]). Other bodies are ignored.
type HTTPWebhookHandler struct {
	client *resty.Client
}

// NewHTTPWebhookHandler creates a new HTTPWebhookHandler, with a request timeout of
// [config.DefaultWebhookTimeoutSeconds] seconds.
func NewHTTPWebhookHandler(logger types.Logger) *HTTPWebhookHandler {
	restyLogger := newRestyLogger(logger)

	client := resty.New().
		SetRetryCount(2).
		SetRetryWaitTime(time.Second).
		SetRetryMaxWaitTime(time.Second).
		AddRetryCondition(webhookRetryPolicy).
		AddRetryHook(closeRetriedResponseBody).
		SetDoNotParseResponse(true).
		SetLogger(restyLogger).
		SetTimeout(config.DefaultWebhookTimeoutSeconds * time.Second)

	return &HTTPWebhookHandler{
		client: client,
	}
}

// WithRequestTimeout sets the timeout of each HTTP request made by the webhook handler. Retried requests get the
// full timeout for each attempt. The timeout is clamped to between [config.MinWebhookTimeoutSeconds] and
// [config.MaxWebhookTimeoutSeconds] seconds. The default is [config.DefaultWebhookTimeoutSeconds] seconds.
func (h *HTTPWebhookHandler) WithRequestTimeout(timeout time.Duration) *HTTPWebhookHandler {
	h.client.SetTimeout(clampWebhookTimeout(timeout))
	return h
}

// clampWebhookTimeout clamps the timeout to the range allowed by [HTTPWebhookHandler.WithRequestTimeout].
func clampWebhookTimeout(timeout time.Duration) time.Duration {
	return min(max(timeout, config.MinWebhookTimeoutSeconds*time.Second), config.MaxWebhookTimeoutSeconds*time.Second)
}

// ShouldHandleWebhook returns true if the target is an HTTP or HTTPS URL.
// If other webhooks should handle certain URLs (e.g. SQS queue URLs), they must be registred *before* this handler.
func (h *HTTPWebhookHandler) ShouldHandleWebhook(_ context.Context, target string) bool {
	return strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
}

// HandleWebhook sends the webhook data to the target URL via an HTTP POST request.
// It expects a successful HTTP status code (2xx) in response. Other status codes result in a (wrapped) [WebhookHTTPError].
// The response body is ignored, see [HTTPWebhookHandler.HandleWebhookWithResult].
func (h *HTTPWebhookHandler) HandleWebhook(ctx context.Context, target string, data *types.WebhookCallback, logger types.Logger) error {
	_, err := h.HandleWebhookWithResult(ctx, target, data, logger)
	return err
}

// HandleWebhookWithResult sends the webhook data to the target URL via an HTTP POST request, and parses the
// [types.WebhookResponse] body, if any.
//
// A 2xx response returns the result (nil without a result body). A 4xx response with a result body returns a
// (wrapped) [WebhookRejectedError] with the result message. Other non-2xx responses return a (wrapped) [WebhookHTTPError].
// Only the final response counts when requests are retried (5xx and 429).
func (h *HTTPWebhookHandler) HandleWebhookWithResult(ctx context.Context, target string, data *types.WebhookCallback, logger types.Logger) (*types.WebhookResult, error) {
	response, err := h.client.R().SetContext(ctx).SetBody(data).Post(target)

	if response != nil && response.RawResponse != nil {
		defer closeResponseBody(response)
	}

	if err != nil {
		return nil, fmt.Errorf("webhook POST %s failed: %w", target, err)
	}

	logger.Debugf("Webhook POST %s %s", target, response.Status())

	code := response.StatusCode()

	if response.IsSuccess() {
		return readWebhookResult(response, logger), nil
	}

	if code >= 400 && code < 500 && code != http.StatusTooManyRequests {
		if result := readWebhookResult(response, logger); result != nil {
			return nil, fmt.Errorf("webhook POST %s failed: %w", target, &WebhookRejectedError{StatusCode: code, Message: result.Message})
		}
	}

	return nil, fmt.Errorf("webhook POST %s failed: %w", target, &WebhookHTTPError{StatusCode: code})
}

// readWebhookResult reads and parses a [types.WebhookResponse] body from the response. It returns nil if the body is
// not a JSON object with the [types.WebhookResultKey] key, is too large, or is invalid. The result is cleaned and
// validated. Unexpected bodies are logged at debug level, invalid results at info level.
func readWebhookResult(response *resty.Response, logger types.Logger) *types.WebhookResult {
	mediaType, _, err := mime.ParseMediaType(response.Header().Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(response.RawBody(), maxWebhookResponseBodySize+1))
	if err != nil {
		logger.Debugf("Failed to read webhook response body: %s", err)
		return nil
	}

	if len(body) > maxWebhookResponseBodySize {
		logger.Infof("Ignoring webhook response body larger than %d bytes", maxWebhookResponseBodySize)
		return nil
	}

	var resp types.WebhookResponse

	if err := json.Unmarshal(body, &resp); err != nil {
		logger.Debugf("Ignoring webhook response body that is not a JSON object: %s", err)
		return nil
	}

	if resp.Result == nil {
		return nil
	}

	resp.Result.Clean()

	if err := resp.Result.Validate(); err != nil {
		logger.Infof("Ignoring invalid webhook result in response body: %s", err)
		return nil
	}

	return resp.Result
}

// closeResponseBody drains (up to a limit, to allow connection reuse) and closes the raw response body.
func closeResponseBody(response *resty.Response) {
	body := response.RawBody()
	if body == nil {
		return
	}

	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxWebhookResponseBodySize))
	_ = body.Close()
}

// closeRetriedResponseBody closes the body of a response that is about to be retried. The handler doesn't let resty
// parse responses (to limit the body size), so resty doesn't close them.
func closeRetriedResponseBody(response *resty.Response, _ error) {
	if response != nil && response.RawResponse != nil {
		closeResponseBody(response)
	}
}

// webhookRetryPolicy determines whether a failed webhook request should be retried.
// It retries on connection errors (except context cancellation, deadline exceeded,
// and DNS resolution failures) and on HTTP 429 (rate limit) or 5xx server errors.
func webhookRetryPolicy(r *resty.Response, err error) bool {
	if err != nil {
		// Don't retry on context cancellation or deadline exceeded
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false
		}

		// Don't retry on DNS resolution errors
		if _, ok := errors.AsType[*net.DNSError](err); ok {
			return false
		}

		// Retry on other connection errors
		return true
	}

	// Retry on 429 (rate limit) and 5xx (server errors)
	return r.StatusCode() == 429 || r.StatusCode() >= 500
}
