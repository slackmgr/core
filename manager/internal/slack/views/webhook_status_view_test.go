package views_test

import (
	"testing"

	"github.com/slack-go/slack"
	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/core/manager/internal/slack/views"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhookStatusText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		outcome     *models.WebhookOutcome
		wantContain []string
		wantAbsent  []string
	}{
		{
			name:        "sending",
			outcome:     &models.WebhookOutcome{ButtonText: "Deploy", Status: models.WebhookOutcomeSending},
			wantContain: []string{`Sending "Deploy"...`},
		},
		{
			name:        "delivered",
			outcome:     &models.WebhookOutcome{ButtonText: "Deploy", Status: models.WebhookOutcomeDelivered},
			wantContain: []string{`"Deploy" was sent.`, "The alert will be updated"},
		},
		{
			name:        "failed retryable",
			outcome:     &models.WebhookOutcome{ButtonText: "Deploy", Status: models.WebhookOutcomeFailed, Reason: models.WebhookReasonTimeout, Retryable: true},
			wantContain: []string{`"Deploy" could not be sent: the request timed out.`, "You can try again."},
		},
		{
			name:        "failed not retryable",
			outcome:     &models.WebhookOutcome{ButtonText: "Deploy", Status: models.WebhookOutcomeFailed, Reason: models.WebhookReasonIssueNotActive},
			wantContain: []string{"the issue is no longer active."},
			wantAbsent:  []string{"try again"},
		},
		{
			name:        "failed without reason",
			outcome:     &models.WebhookOutcome{ButtonText: "Deploy", Status: models.WebhookOutcomeFailed},
			wantContain: []string{"could not be sent: delivery failed."},
		},
		{
			name:        "unknown button text",
			outcome:     &models.WebhookOutcome{Status: models.WebhookOutcomeFailed, Reason: models.WebhookReasonWebhookUnavailable},
			wantContain: []string{"The webhook could not be sent"},
		},
		{
			name:        "unknown button text sending",
			outcome:     &models.WebhookOutcome{Status: models.WebhookOutcomeSending},
			wantContain: []string{"Sending the webhook..."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			text := views.WebhookStatusText(tt.outcome)

			for _, s := range tt.wantContain {
				assert.Contains(t, text, s)
			}

			for _, s := range tt.wantAbsent {
				assert.NotContains(t, text, s)
			}
		})
	}
}

func TestWebhookStatusModal(t *testing.T) {
	t.Parallel()

	outcome := &models.WebhookOutcome{ButtonText: "Deploy", Status: models.WebhookOutcomeDelivered}

	modal := views.WebhookStatusModal(outcome)

	assert.Equal(t, slack.VTModal, modal.Type)
	assert.Nil(t, modal.Submit)
	require.NotNil(t, modal.Close)
	assert.Equal(t, "Close", modal.Close.Text)
	require.NotNil(t, modal.Title)
	assert.LessOrEqual(t, len(modal.Title.Text), 24, "Slack limits modal titles to 24 characters")
	require.Len(t, modal.Blocks.BlockSet, 1)

	section, ok := modal.Blocks.BlockSet[0].(*slack.SectionBlock)
	require.True(t, ok)
	assert.Equal(t, slack.PlainTextType, section.Text.Type)
	assert.Equal(t, views.WebhookStatusText(outcome), section.Text.Text)
}
