package models //nolint:testpackage // Internal test to access unexported fields

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/slackmgr/core/config"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAlertFromQueueItem(t *testing.T) {
	t.Parallel()

	t.Run("returns error for empty body", func(t *testing.T) {
		t.Parallel()

		queueItem := &types.FifoQueueItem{
			Body: "",
		}

		alert, err := NewAlertFromQueueItem(queueItem)

		assert.Nil(t, alert)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "alert body is empty")
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		t.Parallel()

		queueItem := &types.FifoQueueItem{
			Body: "invalid json",
		}

		alert, err := NewAlertFromQueueItem(queueItem)

		assert.Nil(t, alert)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal message body")
	})

	t.Run("creates alert from valid JSON", func(t *testing.T) {
		t.Parallel()

		alertData := types.Alert{
			SlackChannelID: "C12345",
			CorrelationID:  "corr-123",
			Severity:       types.AlertError,
			Header:         "Test Alert",
			Text:           "Alert text",
			Timestamp:      time.Now().UTC(),
		}

		jsonBody, err := json.Marshal(alertData)
		require.NoError(t, err)

		ackCalled := false
		nackCalled := false

		queueItem := &types.FifoQueueItem{
			Body: string(jsonBody),
			Ack:  func() { ackCalled = true },
			Nack: func() { nackCalled = true },
		}

		result, err := NewAlertFromQueueItem(queueItem)

		require.NoError(t, err)
		require.NotNil(t, result)

		alert, ok := result.(*Alert)
		require.True(t, ok)

		assert.Equal(t, "C12345", alert.SlackChannelID)
		assert.Equal(t, "corr-123", alert.CorrelationID)
		assert.Equal(t, types.AlertError, alert.Severity)
		assert.Equal(t, "Test Alert", alert.Header)

		// Verify ack function is set
		alert.Ack()
		assert.True(t, ackCalled)
		assert.False(t, nackCalled)
	})
}

func TestAlert_Ack(t *testing.T) {
	t.Parallel()

	t.Run("calls ack function when set", func(t *testing.T) {
		t.Parallel()

		ackCalled := false
		alert := &Alert{
			ack: func() { ackCalled = true },
		}

		alert.Ack()

		assert.True(t, ackCalled)
		assert.Nil(t, alert.ack)
		assert.Nil(t, alert.nack)
	})

	t.Run("clears both ack and nack", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{
			ack:  func() {},
			nack: func() {},
		}

		alert.Ack()

		assert.Nil(t, alert.ack)
		assert.Nil(t, alert.nack)
	})

	t.Run("does nothing when ack is nil", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{}

		// Should not panic
		alert.Ack()

		assert.Nil(t, alert.ack)
		assert.Nil(t, alert.nack)
	})

	t.Run("subsequent calls have no effect", func(t *testing.T) {
		t.Parallel()

		callCount := 0
		alert := &Alert{
			ack: func() { callCount++ },
		}

		alert.Ack()
		alert.Ack()
		alert.Ack()

		assert.Equal(t, 1, callCount)
	})
}

func TestAlert_Nack(t *testing.T) {
	t.Parallel()

	t.Run("calls nack function when set", func(t *testing.T) {
		t.Parallel()

		nackCalled := false
		alert := &Alert{
			nack: func() { nackCalled = true },
		}

		alert.Nack()

		assert.True(t, nackCalled)
		assert.Nil(t, alert.ack)
		assert.Nil(t, alert.nack)
	})

	t.Run("clears both ack and nack", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{
			ack:  func() {},
			nack: func() {},
		}

		alert.Nack()

		assert.Nil(t, alert.ack)
		assert.Nil(t, alert.nack)
	})

	t.Run("does nothing when nack is nil", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{}

		// Should not panic
		alert.Nack()

		assert.Nil(t, alert.ack)
		assert.Nil(t, alert.nack)
	})

	t.Run("subsequent calls have no effect", func(t *testing.T) {
		t.Parallel()

		callCount := 0
		alert := &Alert{
			nack: func() { callCount++ },
		}

		alert.Nack()
		alert.Nack()
		alert.Nack()

		assert.Equal(t, 1, callCount)
	})
}

func TestAlert_SetDefaultValues(t *testing.T) {
	t.Parallel()

	defaultSettings := &config.ManagerSettings{
		DefaultAlertSeverity:              types.AlertWarning,
		DefaultPostUsername:               "default-bot",
		DefaultPostIconEmoji:              ":robot:",
		DefaultIssueArchivingDelaySeconds: 600,
	}

	t.Run("does nothing for nil alert", func(t *testing.T) {
		t.Parallel()

		var alert *Alert

		// Should not panic
		alert.SetDefaultValues(defaultSettings)
	})

	t.Run("sets default CorrelationID when empty", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{
			Alert: types.Alert{
				SlackChannelID: "C12345",
				Header:         "Test Header",
				Author:         "test-author",
				Host:           "test-host",
				Text:           "Test text",
			},
		}

		alert.SetDefaultValues(defaultSettings)

		assert.NotEmpty(t, alert.CorrelationID)
	})

	t.Run("preserves existing CorrelationID", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{
			Alert: types.Alert{
				CorrelationID: "existing-corr-id",
			},
		}

		alert.SetDefaultValues(defaultSettings)

		assert.Equal(t, "existing-corr-id", alert.CorrelationID)
	})

	t.Run("sets default Severity when empty", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{
			Alert: types.Alert{
				CorrelationID: "corr-123",
			},
		}

		alert.SetDefaultValues(defaultSettings)

		assert.Equal(t, types.AlertWarning, alert.Severity)
	})

	t.Run("preserves existing Severity", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{
			Alert: types.Alert{
				CorrelationID: "corr-123",
				Severity:      types.AlertPanic,
			},
		}

		alert.SetDefaultValues(defaultSettings)

		assert.Equal(t, types.AlertPanic, alert.Severity)
	})

	t.Run("sets default Username when empty", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{
			Alert: types.Alert{
				CorrelationID: "corr-123",
			},
		}

		alert.SetDefaultValues(defaultSettings)

		assert.Equal(t, "default-bot", alert.Username)
	})

	t.Run("preserves existing Username", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{
			Alert: types.Alert{
				CorrelationID: "corr-123",
				Username:      "custom-bot",
			},
		}

		alert.SetDefaultValues(defaultSettings)

		assert.Equal(t, "custom-bot", alert.Username)
	})

	t.Run("sets default IconEmoji when empty", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{
			Alert: types.Alert{
				CorrelationID: "corr-123",
			},
		}

		alert.SetDefaultValues(defaultSettings)

		assert.Equal(t, ":robot:", alert.IconEmoji)
	})

	t.Run("preserves existing IconEmoji", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{
			Alert: types.Alert{
				CorrelationID: "corr-123",
				IconEmoji:     ":fire:",
			},
		}

		alert.SetDefaultValues(defaultSettings)

		assert.Equal(t, ":fire:", alert.IconEmoji)
	})

	t.Run("sets default ArchivingDelaySeconds when zero", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{
			Alert: types.Alert{
				CorrelationID:         "corr-123",
				ArchivingDelaySeconds: 0,
			},
		}

		alert.SetDefaultValues(defaultSettings)

		assert.Equal(t, 600, alert.ArchivingDelaySeconds)
	})

	t.Run("sets default ArchivingDelaySeconds when negative", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{
			Alert: types.Alert{
				CorrelationID:         "corr-123",
				ArchivingDelaySeconds: -100,
			},
		}

		alert.SetDefaultValues(defaultSettings)

		assert.Equal(t, 600, alert.ArchivingDelaySeconds)
	})

	t.Run("preserves positive ArchivingDelaySeconds", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{
			Alert: types.Alert{
				CorrelationID:         "corr-123",
				ArchivingDelaySeconds: 1200,
			},
		}

		alert.SetDefaultValues(defaultSettings)

		assert.Equal(t, 1200, alert.ArchivingDelaySeconds)
	})

	t.Run("sets default webhook AccessLevel when empty", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{
			Alert: types.Alert{
				CorrelationID: "corr-123",
				Webhooks: []*types.Webhook{
					{ID: "hook-1", AccessLevel: ""},
					{ID: "hook-2", AccessLevel: ""},
				},
			},
		}

		alert.SetDefaultValues(defaultSettings)

		assert.Equal(t, types.WebhookAccessLevelGlobalAdmins, alert.Webhooks[0].AccessLevel)
		assert.Equal(t, types.WebhookAccessLevelGlobalAdmins, alert.Webhooks[1].AccessLevel)
	})

	t.Run("preserves existing webhook AccessLevel", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{
			Alert: types.Alert{
				CorrelationID: "corr-123",
				Webhooks: []*types.Webhook{
					{ID: "hook-1", AccessLevel: types.WebhookAccessLevelChannelMembers},
				},
			},
		}

		alert.SetDefaultValues(defaultSettings)

		assert.Equal(t, types.WebhookAccessLevelChannelMembers, alert.Webhooks[0].AccessLevel)
	})
}

func TestAlert_LogFields(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for nil alert", func(t *testing.T) {
		t.Parallel()

		var alert *Alert

		fields := alert.LogFields()

		assert.Nil(t, fields)
	})

	t.Run("returns fields for valid alert", func(t *testing.T) {
		t.Parallel()

		alert := &Alert{
			Alert: types.Alert{
				SlackChannelID: "C12345",
				CorrelationID:  "corr-123",
			},
			SlackChannelName: "test-channel",
		}

		fields := alert.LogFields()

		require.NotNil(t, fields)
		assert.Equal(t, "C12345", fields["channel_id"])
		assert.Equal(t, "test-channel", fields["channel_name"])
		assert.Equal(t, "corr-123", fields["correlation_id"])
	})
}
