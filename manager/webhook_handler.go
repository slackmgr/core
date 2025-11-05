package manager

import (
	"context"

	common "github.com/peteraglen/slack-manager-common"
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
	HandleWebhook(ctx context.Context, target string, data *common.WebhookCallback, logger common.Logger) error
}
