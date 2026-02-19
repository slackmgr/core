package models

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/internal"
	"github.com/slackmgr/types"
)

// Alert represents an alert message to be sent to a Slack channel.
// It extends the types.Alert struct with additional fields needed in this package.
type Alert struct {
	types.Alert

	SlackChannelName       string `json:"slackChannelName"`
	OriginalSlackChannelID string `json:"originalSlackChannelId"`
	OriginalText           string `json:"originalText"`

	ack  func()
	nack func()
}

// NewAlertFromQueueItem creates a new Alert instance from a FifoQueueItem.
// It unmarshals the JSON body of the queue item into an Alert struct.
func NewAlertFromQueueItem(queueItem *types.FifoQueueItem) (InFlightMessage, error) { //nolint:ireturn
	if len(queueItem.Body) == 0 {
		return nil, errors.New("alert body is empty")
	}

	var alert types.Alert

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
func (a *Alert) Ack() {
	if a.ack != nil {
		a.ack()
	}

	a.ack = nil
	a.nack = nil
}

// Nack negatively acknowledges the alert message, indicating processing failure and requesting re-delivery.
// Any subsequent calls to Ack or Nack will have no effect.
func (a *Alert) Nack() {
	if a.nack != nil {
		a.nack()
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
			w.AccessLevel = types.WebhookAccessLevelGlobalAdmins
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
