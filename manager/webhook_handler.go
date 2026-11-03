package manager

import (
	"context"

	"github.com/slackmgr/types"
)

// WebhookHandler is an interface for handling alert webhooks (i.e. issue callbacks).
// The webhook may or may not be http based.
// More than one handler can be registered with a Manager.
// The first handler that returns true from ShouldHandleWebhook will be used to handle a given webhook.
type WebhookHandler interface {
	// ShouldHandleWebhook returns true if the handler should handle the specified webhook target.
	ShouldHandleWebhook(ctx context.Context, target string) bool

	// HandleWebhook handles the webhook target, with the specified callback data.
	// If successful, it should DEBUG log the send action and return nil.
	// If unsuccessful, it should return the error (without logging it).
	HandleWebhook(ctx context.Context, target string, data *types.WebhookCallback, logger types.Logger) error
}

// WebhookResultHandler is an optional extension of [WebhookHandler], for synchronous handlers that can give feedback
// on a webhook button click, e.g. a validation error or "3 pods will be restarted". It is detected by type assertion.
// [HTTPWebhookHandler] implements it.
//
// When implemented, the manager calls HandleWebhookWithResult instead of HandleWebhook.
type WebhookResultHandler interface {
	WebhookHandler

	// HandleWebhookWithResult handles the webhook target, with the specified callback data, like
	// [WebhookHandler.HandleWebhook].
	//
	// If the request was accepted, it returns the (optional, possibly nil) result and a nil error. The result message
	// is shown to the user who clicked the button.
	//
	// If the receiving system rejected the request, it returns a (wrapped) [WebhookRejectedError] with an optional
	// message for the user. Other errors are delivery failures, as for [WebhookHandler.HandleWebhook].
	//
	// The manager cleans and validates the result; invalid results are ignored (logged).
	HandleWebhookWithResult(ctx context.Context, target string, data *types.WebhookCallback, logger types.Logger) (*types.WebhookResult, error)
}
