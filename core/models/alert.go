package models

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	commonlib "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/client"
	"github.com/peteraglen/slack-manager/core/config"
	"github.com/peteraglen/slack-manager/internal"
)

type Alert struct {
	ID string `json:"-"`
	client.Alert
	DBTimestamp            time.Time `json:"@timestamp"`
	SlackChannelName       string    `json:"slackChannelName"`
	OriginalSlackChannelID string    `json:"originalSlackChannelID"`
	OriginalText           string    `json:"originalText"`

	// waitForDBWriteDone is used to wait for the alert to be persisted before it is acked.
	waitForDBWriteDone Future

	message
}

func NewAlert(queueItem *commonlib.QueueItem) (Message, error) {
	if len(queueItem.Body) == 0 {
		return nil, fmt.Errorf("alert body is empty")
	}

	var alert client.Alert

	if err := json.Unmarshal([]byte(queueItem.Body), &alert); err != nil {
		return nil, fmt.Errorf("failed to unmarshal message body: %w", err)
	}

	return &Alert{
		ID:          internal.Hash(alert.SlackChannelID, alert.CorrelationID, alert.Timestamp.Format(time.RFC3339Nano)),
		DBTimestamp: alert.Timestamp,
		Alert:       alert,
		message:     newMessage(queueItem),
	}, nil
}

// InitWaitForDBWriteDone initializes the wait block which ensures that the alert is persisted before it is acked.
func (a *Alert) InitWaitForDBWriteDone(f Future) {
	a.waitForDBWriteDone = f
}

// WaitForDBWriteDone blocks until the alert has been written to an issue and persisted, or the context is cancelled.
func (a *Alert) WaitForDBWriteDone(ctx context.Context) error {
	if a.waitForDBWriteDone == nil {
		return nil
	}

	err := a.waitForDBWriteDone.Wait(ctx)

	a.waitForDBWriteDone = nil

	return err
}

func (a *Alert) SetDefaultValues(conf *config.Config) {
	if a == nil {
		return
	}

	if a.CorrelationID == "" {
		h := sha256.New()
		h.Write([]byte(a.Header + a.Author + a.Host + a.Text + a.SlackChannelID))
		bs := h.Sum(nil)
		a.CorrelationID = base64.URLEncoding.EncodeToString(bs)
	}

	if a.Severity == "" {
		a.Severity = "error"
	}

	if a.Username == "" {
		a.Username = "SUDO Slack Manager"
	}

	if a.IconEmoji == "" {
		a.IconEmoji = ":female-detective:"
	}

	if a.ArchivingDelaySeconds <= 0 {
		a.ArchivingDelaySeconds = int(conf.DefaultArchivingDelay.Seconds())
	}

	for _, w := range a.Webhooks {
		if w.AccessLevel == "" {
			w.AccessLevel = client.WebhookAccessLevelGlobalAdmins
		}
	}
}
