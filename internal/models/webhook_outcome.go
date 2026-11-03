package models

// WebhookOutcomeStatus is the result of a user-triggered webhook.
type WebhookOutcomeStatus string

const (
	// WebhookOutcomeSending means the webhook has been accepted and is waiting to be delivered.
	WebhookOutcomeSending = WebhookOutcomeStatus("sending")

	// WebhookOutcomeDelivered means the webhook was delivered to the target.
	WebhookOutcomeDelivered = WebhookOutcomeStatus("delivered")

	// WebhookOutcomeFailed means the webhook could not be delivered, or was never sent.
	WebhookOutcomeFailed = WebhookOutcomeStatus("failed")

	// WebhookOutcomeRejected means the webhook was delivered, but the receiving system rejected the request,
	// e.g. because of invalid input.
	WebhookOutcomeRejected = WebhookOutcomeStatus("rejected")
)

// Default user-facing failure reasons for webhooks. See [WebhookOutcome.Reason].
const (
	WebhookReasonUserLookupFailed   = "your Slack user could not be looked up"
	WebhookReasonQueueFailed        = "the request could not be queued for processing"
	WebhookReasonIssueNotActive     = "the issue is no longer active"
	WebhookReasonWebhookUnavailable = "the button is no longer available on this issue"
	WebhookReasonConfigError        = "the webhook is not configured correctly, please contact an administrator"
	WebhookReasonTimeout            = "the request timed out"
	WebhookReasonUnreachable        = "the receiving system could not be reached"
	WebhookReasonDeliveryFailed     = "delivery failed"
)

// WebhookOutcome describes the result of a user-triggered webhook, for feedback to the user who clicked the button.
type WebhookOutcome struct {
	// ChannelID is the Slack channel where the webhook button was clicked.
	ChannelID string

	// UserID is the Slack user who clicked the webhook button.
	UserID string

	// ViewID is the ID of the confirmation modal, if any. See [WebhookCommandParams.ViewID].
	ViewID string

	// ButtonText is the label of the clicked webhook button. Empty if unknown.
	ButtonText string

	// Status is the webhook result.
	Status WebhookOutcomeStatus

	// Reason is a user-facing explanation of a failure. It must not contain internal details such as target URLs.
	// Empty unless Status is [WebhookOutcomeFailed].
	Reason string

	// Retryable is true if the user may succeed by trying again. Only used when Status is [WebhookOutcomeFailed].
	Retryable bool

	// Message is an optional message from the receiving system (the Message of a types.WebhookResult), shown as plain
	// text. Only used when Status is [WebhookOutcomeDelivered] or [WebhookOutcomeRejected].
	Message string
}

// NewWebhookOutcome creates a [WebhookOutcome] with the given status, for the user and confirmation modal (if any)
// of the given webhook command.
func NewWebhookOutcome(cmd *Command, buttonText string, status WebhookOutcomeStatus) *WebhookOutcome {
	outcome := &WebhookOutcome{
		ChannelID:  cmd.SlackChannelID,
		UserID:     cmd.UserID,
		ButtonText: buttonText,
		Status:     status,
	}

	if cmd.WebhookParameters != nil {
		outcome.ViewID = cmd.WebhookParameters.ViewID
	}

	return outcome
}

// Failed returns a copy of the outcome with status [WebhookOutcomeFailed] and the given reason.
func (o *WebhookOutcome) Failed(reason string, retryable bool) *WebhookOutcome {
	c := *o
	c.Status = WebhookOutcomeFailed
	c.Reason = reason
	c.Retryable = retryable
	c.Message = ""

	return &c
}

// Delivered returns a copy of the outcome with status [WebhookOutcomeDelivered] and the given (optional) message from
// the receiving system.
func (o *WebhookOutcome) Delivered(message string) *WebhookOutcome {
	c := *o
	c.Status = WebhookOutcomeDelivered
	c.Reason = ""
	c.Retryable = false
	c.Message = message

	return &c
}

// Rejected returns a copy of the outcome with status [WebhookOutcomeRejected] and the given (optional) message from
// the receiving system.
func (o *WebhookOutcome) Rejected(message string) *WebhookOutcome {
	c := *o
	c.Status = WebhookOutcomeRejected
	c.Reason = ""
	c.Retryable = false
	c.Message = message

	return &c
}

// NotifyWithoutModal returns true if the user should be informed about the outcome also when there is no confirmation
// modal to update: failures and rejections, and deliveries with a message from the receiving system.
// Plain deliveries are not reported, since webhooks without a confirmation dialog are meant to be lightweight.
func (o *WebhookOutcome) NotifyWithoutModal() bool {
	switch o.Status {
	case WebhookOutcomeFailed, WebhookOutcomeRejected:
		return true
	case WebhookOutcomeDelivered:
		return o.Message != ""
	case WebhookOutcomeSending:
		return false
	default:
		return false
	}
}
