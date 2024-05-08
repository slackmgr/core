package client

// WebhookDisplayMode represents a display mode for a webhook button
type WebhookDisplayMode string

const (
	// WebhookDisplayModeAlways means that the webhook button is always displayed, regardless of issue state.
	WebhookDisplayModeAlways WebhookDisplayMode = "always"

	// WebhookDisplayModeOpenIssue means that the webhook button is displayed for open issues only (error, warning etc).
	WebhookDisplayModeOpenIssue WebhookDisplayMode = "open_issue"

	// WebhookDisplayModeResolvedIssue means that the webhook button is displayed for resolved issues only.
	WebhookDisplayModeResolvedIssue WebhookDisplayMode = "resolved_issue"
)

var validWebhookDisplayModes map[WebhookDisplayMode]struct{}

func init() {
	validWebhookDisplayModes = map[WebhookDisplayMode]struct{}{
		WebhookDisplayModeAlways:        {},
		WebhookDisplayModeOpenIssue:     {},
		WebhookDisplayModeResolvedIssue: {},
	}
}

func WebhookDisplayModeIsValid(s WebhookDisplayMode) bool {
	_, ok := validWebhookDisplayModes[s]
	return ok
}

func ValidWebhookDisplayModes() []string {
	r := make([]string, len(validWebhookDisplayModes))
	i := 0

	for s := range validWebhookDisplayModes {
		r[i] = string(s)
		i++
	}

	return r
}
