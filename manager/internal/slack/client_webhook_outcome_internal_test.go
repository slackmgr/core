package slack

import (
	"context"
	"errors"
	"sync"
	"testing"

	slackapi "github.com/slack-go/slack"
	"github.com/slackmgr/core/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// webhookOutcomeSlackAPI is a [slackAPI] that records modal updates and ephemeral messages.
type webhookOutcomeSlackAPI struct {
	slackAPI // Unused methods panic

	updateModalErr error

	mu           sync.Mutex
	modalUpdates []slackapi.ModalViewRequest
	modalViewIDs []string
	ephemerals   []string
}

func (f *webhookOutcomeSlackAPI) UpdateModal(_ context.Context, viewID string, request slackapi.ModalViewRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.modalViewIDs = append(f.modalViewIDs, viewID)
	f.modalUpdates = append(f.modalUpdates, request)

	return f.updateModalErr
}

func (f *webhookOutcomeSlackAPI) PostEphemeral(_ context.Context, channelID, userID string, options ...slackapi.MsgOption) (string, error) {
	_, values, err := slackapi.UnsafeApplyMsgOptions("", channelID, "", options...)
	if err != nil {
		return "", err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.ephemerals = append(f.ephemerals, userID+":"+values.Get("text"))

	return "ts", nil
}

func TestClient_NotifyWebhookOutcome(t *testing.T) {
	t.Parallel()

	const (
		channelID = "C123"
		userID    = "U123"
		viewID    = "V123"
	)

	delivered := &models.WebhookOutcome{ChannelID: channelID, UserID: userID, ButtonText: "Deploy", Status: models.WebhookOutcomeDelivered}
	failed := &models.WebhookOutcome{ChannelID: channelID, UserID: userID, ButtonText: "Deploy", Status: models.WebhookOutcomeFailed, Reason: models.WebhookReasonTimeout, Retryable: true}
	deliveredMsg := &models.WebhookOutcome{ChannelID: channelID, UserID: userID, ButtonText: "Deploy", Status: models.WebhookOutcomeDelivered, Message: "Deploying 3 services"}
	rejected := &models.WebhookOutcome{ChannelID: channelID, UserID: userID, ButtonText: "Deploy", Status: models.WebhookOutcomeRejected, Message: "Unknown version <!channel>"}

	withView := func(o *models.WebhookOutcome) *models.WebhookOutcome {
		c := *o
		c.ViewID = viewID
		return &c
	}

	tests := []struct {
		name             string
		outcome          *models.WebhookOutcome
		updateModalErr   error
		wantModalUpdates int
		wantEphemerals   int
		wantText         string
	}{
		{name: "nil outcome", outcome: nil},
		{name: "modal delivered", outcome: withView(delivered), wantModalUpdates: 1},
		{name: "modal failed", outcome: withView(failed), wantModalUpdates: 1},
		{name: "closed modal delivered is silent", outcome: withView(delivered), updateModalErr: errors.New("not_found"), wantModalUpdates: 1},
		{name: "closed modal failed falls back to ephemeral", outcome: withView(failed), updateModalErr: errors.New("not_found"), wantModalUpdates: 1, wantEphemerals: 1, wantText: models.WebhookReasonTimeout},
		{name: "closed modal delivered with message falls back to ephemeral", outcome: withView(deliveredMsg), updateModalErr: errors.New("not_found"), wantModalUpdates: 1, wantEphemerals: 1, wantText: "Deploying 3 services"},
		{name: "no modal delivered is silent", outcome: delivered},
		{name: "no modal failed posts ephemeral", outcome: failed, wantEphemerals: 1, wantText: models.WebhookReasonTimeout},
		{name: "no modal delivered with message posts ephemeral", outcome: deliveredMsg, wantEphemerals: 1, wantText: "Deploying 3 services"},
		{name: "no modal rejected posts escaped ephemeral", outcome: rejected, wantEphemerals: 1, wantText: "Unknown version &lt;!channel&gt;"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			api := &webhookOutcomeSlackAPI{updateModalErr: tt.updateModalErr}
			c := newUpdateTestClient(t, &fakeSlackAPI{})
			c.api = api

			c.NotifyWebhookOutcome(context.Background(), tt.outcome)

			require.Len(t, api.modalUpdates, tt.wantModalUpdates)
			require.Len(t, api.ephemerals, tt.wantEphemerals)

			if tt.wantModalUpdates > 0 {
				assert.Equal(t, viewID, api.modalViewIDs[0])
				assert.Nil(t, api.modalUpdates[0].Submit)
			}

			if tt.wantEphemerals > 0 {
				assert.Contains(t, api.ephemerals[0], userID+":")
				assert.Contains(t, api.ephemerals[0], tt.wantText)
			}
		})
	}
}
