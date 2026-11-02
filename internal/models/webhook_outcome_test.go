package models //nolint:testpackage // Internal test, consistent with the other tests in this package

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewWebhookOutcome(t *testing.T) {
	t.Parallel()

	t.Run("with webhook parameters", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{SlackChannelID: "C1", UserID: "U1", WebhookParameters: &WebhookCommandParams{ViewID: "V1"}}

		outcome := NewWebhookOutcome(cmd, "Deploy", WebhookOutcomeSending)

		assert.Equal(t, &WebhookOutcome{ChannelID: "C1", UserID: "U1", ViewID: "V1", ButtonText: "Deploy", Status: WebhookOutcomeSending}, outcome)
	})

	t.Run("without webhook parameters", func(t *testing.T) {
		t.Parallel()

		outcome := NewWebhookOutcome(&Command{SlackChannelID: "C1", UserID: "U1"}, "", WebhookOutcomeFailed)

		assert.Empty(t, outcome.ViewID)
		assert.Equal(t, WebhookOutcomeFailed, outcome.Status)
	})
}

func TestWebhookOutcome_FailedAndDelivered(t *testing.T) {
	t.Parallel()

	base := &WebhookOutcome{ChannelID: "C1", UserID: "U1", ViewID: "V1", ButtonText: "Deploy", Status: WebhookOutcomeSending}

	failed := base.Failed(WebhookReasonTimeout, true)

	assert.Equal(t, WebhookOutcomeFailed, failed.Status)
	assert.Equal(t, WebhookReasonTimeout, failed.Reason)
	assert.True(t, failed.Retryable)
	assert.Equal(t, "V1", failed.ViewID)
	assert.Equal(t, WebhookOutcomeSending, base.Status, "Failed must not modify the original")

	delivered := failed.Delivered()

	assert.Equal(t, WebhookOutcomeDelivered, delivered.Status)
	assert.Empty(t, delivered.Reason)
	assert.False(t, delivered.Retryable)
	assert.Equal(t, WebhookOutcomeFailed, failed.Status, "Delivered must not modify the original")
}
