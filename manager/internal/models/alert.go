package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	commonlib "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal"
)

// Alert represents an alert message to be sent to a Slack channel.
// It extends the commonlib.Alert struct with additional fields needed in this package.
type Alert struct {
	commonlib.Alert

	SlackChannelName       string `json:"slackChannelName"`
	OriginalSlackChannelID string `json:"originalSlackChannelId"`
	OriginalText           string `json:"originalText"`

	ack  func(ctx context.Context)
	nack func(ctx context.Context)
}

// NewAlertFromQueueItem creates a new Alert instance from a FifoQueueItem.
// It unmarshals the JSON body of the queue item into an Alert struct.
func NewAlertFromQueueItem(queueItem *commonlib.FifoQueueItem) (InFlightMessage, error) { //nolint:ireturn
	if len(queueItem.Body) == 0 {
		return nil, errors.New("alert body is empty")
	}

	var alert commonlib.Alert

	if err := json.Unmarshal([]byte(queueItem.Body), &alert); err != nil {
		return nil, fmt.Errorf("failed to unmarshal message body: %w", err)
	}

	return &Alert{
		Alert: alert,
		ack:   queueItem.Ack,
		nack:  queueItem.Nack,
	}, nil
}

// Ack acknowledges the alert message, indicating successful processing.
// Any subsequent calls to Ack or Nack will have no effect.
func (a *Alert) Ack(ctx context.Context) {
	if a.ack != nil {
		a.ack(ctx)
	}

	a.ack = nil
	a.nack = nil
}

// Nack negatively acknowledges the alert message, indicating processing failure and requesting re-delivery.
// Any subsequent calls to Ack or Nack will have no effect.
func (a *Alert) Nack(ctx context.Context) {
	if a.ack != nil {
		a.nack(ctx)
	}

	a.ack = nil
	a.nack = nil
}

// SetDefaultValues sets default values for the Alert fields based on the provided ManagerSettings.
func (a *Alert) SetDefaultValues(settings *config.ManagerSettings) {
	if a == nil {
		return
	}

	if a.CorrelationID == "" {
		a.CorrelationID = internal.Hash(a.Header, a.Author, a.Host, a.Text, a.SlackChannelID)
	}

	if a.Severity == "" {
		a.Severity = settings.DefaultAlertSeverity
	}

	if a.Username == "" {
		a.Username = settings.DefaultPostUsername
	}

	if a.IconEmoji == "" {
		a.IconEmoji = settings.DefaultPostIconEmoji
	}

	if a.ArchivingDelaySeconds <= 0 {
		a.ArchivingDelaySeconds = settings.DefaultIssueArchivingDelaySeconds
	}

	for _, w := range a.Webhooks {
		if w.AccessLevel == "" {
			w.AccessLevel = commonlib.WebhookAccessLevelGlobalAdmins
		}
	}
}

// LogFields returns a map of key-value pairs representing the Alert's important fields for logging purposes.
func (a *Alert) LogFields() map[string]any {
	if a == nil {
		return nil
	}

	return map[string]any{
		"channel_id":     a.SlackChannelID,
		"channel_name":   a.SlackChannelName,
		"correlation_id": a.CorrelationID,
	}
}
