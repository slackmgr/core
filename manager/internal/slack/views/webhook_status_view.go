package views

import (
	"fmt"

	"github.com/slack-go/slack"
	"github.com/slackmgr/core/internal/models"
)

// WebhookStatusModal returns a modal view showing the status of a user-triggered webhook.
// The modal has no submit button, only a close button.
func WebhookStatusModal(outcome *models.WebhookOutcome) slack.ModalViewRequest {
	return slack.ModalViewRequest{
		Type:   slack.VTModal,
		Title:  slack.NewTextBlockObject(slack.PlainTextType, "Webhook", false, false),
		Close:  slack.NewTextBlockObject(slack.PlainTextType, "Close", false, false),
		Blocks: slack.Blocks{BlockSet: []slack.Block{webhookStatusSection(outcome)}},
	}
}

// WebhookStatusText returns a plain text message describing the status of a user-triggered webhook,
// e.g. for an ephemeral message.
func WebhookStatusText(outcome *models.WebhookOutcome) string {
	name := webhookDisplayName(outcome)

	switch outcome.Status {
	case models.WebhookOutcomeSending:
		return fmt.Sprintf(":hourglass_flowing_sand: Sending %s...", name)
	case models.WebhookOutcomeDelivered:
		return fmt.Sprintf(":white_check_mark: %s was sent.\n\nThe alert will be updated when the receiving system has processed the request. This may take a while.", capitalizeFirst(name))
	case models.WebhookOutcomeFailed:
		return webhookFailedText(name, outcome)
	default:
		return webhookFailedText(name, outcome)
	}
}

func webhookFailedText(name string, outcome *models.WebhookOutcome) string {
	reason := outcome.Reason
	if reason == "" {
		reason = models.WebhookReasonDeliveryFailed
	}

	text := fmt.Sprintf(":x: %s could not be sent: %s.", capitalizeFirst(name), reason)

	if outcome.Retryable {
		text += "\n\nYou can try again."
	}

	return text
}

func webhookStatusSection(outcome *models.WebhookOutcome) *slack.SectionBlock {
	text := slack.NewTextBlockObject(slack.PlainTextType, WebhookStatusText(outcome), true, false)
	return slack.NewSectionBlock(text, nil, nil)
}

// webhookDisplayName returns the quoted button text, or a generic name if the button text is unknown.
func webhookDisplayName(outcome *models.WebhookOutcome) string {
	if outcome.ButtonText == "" {
		return "the webhook"
	}

	return "\"" + outcome.ButtonText + "\""
}

func capitalizeFirst(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}

	return string(s[0]-'a'+'A') + s[1:]
}
