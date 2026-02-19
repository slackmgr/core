package models //nolint:testpackage // Internal test to access unexported fields

import (
	"strings"
	"testing"
	"time"

	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLogger implements types.Logger for testing
type mockLogger struct{}

func (m *mockLogger) Debug(_ string)                           {}
func (m *mockLogger) Debugf(_ string, _ ...any)                {}
func (m *mockLogger) Info(_ string)                            {}
func (m *mockLogger) Infof(_ string, _ ...any)                 {}
func (m *mockLogger) Error(_ string)                           {}
func (m *mockLogger) Errorf(_ string, _ ...any)                {}
func (m *mockLogger) WithField(_ string, _ any) types.Logger   { return m }
func (m *mockLogger) WithFields(_ map[string]any) types.Logger { return m }

// setTestTime sets the nowFunc to return a fixed time for testing
func setTestTime(t time.Time) func() {
	original := nowFunc
	nowFunc = func() time.Time { return t }
	return func() { nowFunc = original }
}

// createTestAlert creates a basic alert for testing
func createTestAlert() *Alert {
	return &Alert{
		Alert: types.Alert{
			SlackChannelID:        "C12345",
			CorrelationID:         "test-correlation-id",
			Severity:              types.AlertError,
			Header:                "Test Alert",
			Text:                  "Test alert text",
			Timestamp:             time.Now().UTC(),
			IssueFollowUpEnabled:  true,
			AutoResolveSeconds:    3600,
			ArchivingDelaySeconds: 300,
		},
		SlackChannelName:       "test-channel",
		OriginalSlackChannelID: "C12345",
		OriginalText:           "Test alert text",
	}
}

func TestNewIssue(t *testing.T) {
	logger := &mockLogger{}

	t.Run("returns nil for nil alert", func(t *testing.T) {
		issue := NewIssue(nil, logger)
		assert.Nil(t, issue)
	})

	t.Run("creates issue with correct fields", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		alert := createTestAlert()
		alert.Timestamp = fixedTime.Add(-time.Minute)

		issue := NewIssue(alert, logger)

		require.NotNil(t, issue)
		assert.NotEmpty(t, issue.ID)
		assert.Equal(t, "test-correlation-id", issue.CorrelationID)
		assert.Equal(t, 1, issue.AlertCount)
		assert.Equal(t, fixedTime, issue.Created)
		assert.Equal(t, fixedTime, issue.LastAlertReceived)
		assert.Same(t, alert, issue.FirstAlert)
		assert.Same(t, alert, issue.LastAlert)
		assert.Equal(t, 3600*time.Second, issue.AutoResolvePeriod)
		assert.Equal(t, 300*time.Second, issue.ArchiveDelay)
		assert.True(t, issue.SlackPostNeedsUpdate)
		assert.False(t, issue.Archived)
	})

	t.Run("sets resolve time for non-resolved alerts", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		alert := createTestAlert()
		alert.Timestamp = fixedTime
		alert.Severity = types.AlertError

		issue := NewIssue(alert, logger)

		require.NotNil(t, issue)
		expectedResolveTime := alert.Timestamp.Add(time.Duration(alert.AutoResolveSeconds) * time.Second)
		assert.Equal(t, expectedResolveTime, issue.ResolveTime)
	})

	t.Run("sets resolve time immediately for resolved alerts", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		alert := createTestAlert()
		alert.Timestamp = fixedTime
		alert.Severity = types.AlertResolved

		issue := NewIssue(alert, logger)

		require.NotNil(t, issue)
		assert.Equal(t, alert.Timestamp, issue.ResolveTime)
	})

	t.Run("disables follow-up for info severity", func(t *testing.T) {
		alert := createTestAlert()
		alert.Severity = types.AlertInfo
		alert.IssueFollowUpEnabled = true

		issue := NewIssue(alert, logger)

		require.NotNil(t, issue)
		assert.False(t, issue.LastAlert.IssueFollowUpEnabled)
	})

	t.Run("sets notification delay", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		alert := createTestAlert()
		alert.NotificationDelaySeconds = 60

		issue := NewIssue(alert, logger)

		require.NotNil(t, issue)
		expectedDelay := fixedTime.Add(60 * time.Second)
		assert.Equal(t, expectedDelay, issue.SlackPostDelayedUntil)
	})
}

func TestIssue_ChannelID(t *testing.T) {
	t.Parallel()

	issue := &Issue{
		LastAlert: &Alert{
			Alert: types.Alert{
				SlackChannelID: "C12345",
			},
		},
	}

	assert.Equal(t, "C12345", issue.ChannelID())
}

func TestIssue_UniqueID(t *testing.T) {
	t.Parallel()

	issue := &Issue{ID: "test-id-123"}
	assert.Equal(t, "test-id-123", issue.UniqueID())
}

func TestIssue_IsOpen(t *testing.T) {
	t.Parallel()

	t.Run("returns true when not archived", func(t *testing.T) {
		t.Parallel()
		issue := &Issue{Archived: false}
		assert.True(t, issue.IsOpen())
	})

	t.Run("returns false when archived", func(t *testing.T) {
		t.Parallel()
		issue := &Issue{Archived: true}
		assert.False(t, issue.IsOpen())
	})
}

func TestIssue_GetCorrelationID(t *testing.T) {
	t.Parallel()

	issue := &Issue{CorrelationID: "corr-123"}
	assert.Equal(t, "corr-123", issue.GetCorrelationID())
}

func TestIssue_CurrentPostID(t *testing.T) {
	t.Parallel()

	issue := &Issue{SlackPostID: "1234567890.123456"}
	assert.Equal(t, "1234567890.123456", issue.CurrentPostID())
}

func TestIssue_FollowUpEnabled(t *testing.T) {
	t.Parallel()

	t.Run("returns true when enabled", func(t *testing.T) {
		t.Parallel()
		issue := &Issue{
			LastAlert: &Alert{Alert: types.Alert{IssueFollowUpEnabled: true}},
		}
		assert.True(t, issue.FollowUpEnabled())
	})

	t.Run("returns false when disabled", func(t *testing.T) {
		t.Parallel()
		issue := &Issue{
			LastAlert: &Alert{Alert: types.Alert{IssueFollowUpEnabled: false}},
		}
		assert.False(t, issue.FollowUpEnabled())
	})
}

func TestIssue_AddAlert(t *testing.T) {
	logger := &mockLogger{}

	t.Run("ignores older alerts", func(t *testing.T) {
		now := time.Now().UTC()
		issue := &Issue{
			AlertCount: 1,
			LastAlert: &Alert{
				Alert: types.Alert{
					Timestamp:            now,
					IssueFollowUpEnabled: true,
				},
			},
		}

		olderAlert := &Alert{
			Alert: types.Alert{
				Timestamp: now.Add(-time.Hour),
			},
		}

		result := issue.AddAlert(olderAlert, logger)

		assert.False(t, result)
		assert.Equal(t, 1, issue.AlertCount)
	})

	t.Run("ignores resolved alert when already resolved", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			AlertCount:  1,
			ResolveTime: fixedTime.Add(-time.Hour),
			LastAlert: &Alert{
				Alert: types.Alert{
					Timestamp:            fixedTime.Add(-2 * time.Hour),
					Severity:             types.AlertResolved,
					IssueFollowUpEnabled: true,
				},
			},
		}

		newAlert := &Alert{
			Alert: types.Alert{
				Timestamp: fixedTime,
				Severity:  types.AlertResolved,
			},
		}

		result := issue.AddAlert(newAlert, logger)

		assert.False(t, result)
	})

	t.Run("ignores alerts when follow-up disabled", func(t *testing.T) {
		now := time.Now().UTC()
		issue := &Issue{
			AlertCount: 1,
			LastAlert: &Alert{
				Alert: types.Alert{
					Timestamp:            now,
					IssueFollowUpEnabled: false,
				},
			},
		}

		newAlert := &Alert{
			Alert: types.Alert{
				Timestamp: now.Add(time.Hour),
			},
		}

		result := issue.AddAlert(newAlert, logger)

		assert.False(t, result)
	})

	t.Run("adds newer alert successfully", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			AlertCount: 1,
			Created:    fixedTime.Add(-time.Hour),
			LastAlert: &Alert{
				Alert: types.Alert{
					Timestamp:            fixedTime.Add(-time.Minute),
					IssueFollowUpEnabled: true,
				},
			},
		}

		newAlert := &Alert{
			Alert: types.Alert{
				Timestamp:             fixedTime,
				Severity:              types.AlertWarning,
				AutoResolveSeconds:    7200,
				ArchivingDelaySeconds: 600,
			},
		}

		result := issue.AddAlert(newAlert, logger)

		assert.True(t, result)
		assert.Equal(t, 2, issue.AlertCount)
		assert.Same(t, newAlert, issue.LastAlert)
		assert.Equal(t, fixedTime, issue.LastAlertReceived)
		assert.Equal(t, 7200*time.Second, issue.AutoResolvePeriod)
		assert.Equal(t, 600*time.Second, issue.ArchiveDelay)
		assert.True(t, issue.SlackPostNeedsUpdate)
		assert.False(t, issue.Archived)
	})

	t.Run("maintains severity during escalation", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			AlertCount:  1,
			Created:     fixedTime.Add(-time.Hour),
			IsEscalated: true,
			LastAlert: &Alert{
				Alert: types.Alert{
					Timestamp:            fixedTime.Add(-time.Minute),
					Severity:             types.AlertPanic,
					IssueFollowUpEnabled: true,
				},
			},
		}

		newAlert := &Alert{
			Alert: types.Alert{
				Timestamp: fixedTime,
				Severity:  types.AlertWarning,
			},
		}

		result := issue.AddAlert(newAlert, logger)

		assert.True(t, result)
		assert.Equal(t, types.AlertPanic, newAlert.Severity)
	})
}

func TestIssue_GetSlackAction(t *testing.T) {
	t.Run("returns none when archived", func(t *testing.T) {
		issue := &Issue{Archived: true}
		assert.Equal(t, ActionNone, issue.GetSlackAction())
	})

	t.Run("returns none when delayed", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			SlackPostDelayedUntil: fixedTime.Add(time.Hour),
		}

		assert.Equal(t, ActionNone, issue.GetSlackAction())
	})

	t.Run("returns none when terminated", func(t *testing.T) {
		issue := &Issue{IsEmojiTerminated: true}
		assert.Equal(t, ActionNone, issue.GetSlackAction())
	})

	t.Run("returns alert for fire-and-forget when needs update", func(t *testing.T) {
		issue := &Issue{
			SlackPostNeedsUpdate: true,
			LastAlert: &Alert{
				Alert: types.Alert{IssueFollowUpEnabled: false},
			},
		}

		assert.Equal(t, ActionAlert, issue.GetSlackAction())
	})

	t.Run("returns alert for active issue needing update", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			SlackPostNeedsUpdate: true,
			ResolveTime:          fixedTime.Add(time.Hour),
			LastAlert: &Alert{
				Alert: types.Alert{
					Severity:             types.AlertError,
					IssueFollowUpEnabled: true,
				},
			},
		}

		assert.Equal(t, ActionAlert, issue.GetSlackAction())
	})

	t.Run("returns resolve for resolved issue", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			SlackPostNeedsUpdate:      true,
			SlackAlertSentAtLeastOnce: true,
			ResolveTime:               fixedTime.Add(-time.Hour),
			SlackPostLastAction:       ActionAlert,
			LastAlert: &Alert{
				Alert: types.Alert{
					Severity:             types.AlertError,
					IssueFollowUpEnabled: true,
				},
			},
		}

		assert.Equal(t, ActionResolve, issue.GetSlackAction())
	})
}

func TestIssue_RegisterMethods(t *testing.T) {
	t.Run("RegisterSlackPostCreatedOrUpdated", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			SlackPostNeedsUpdate: true,
			LastAlert:            &Alert{Alert: types.Alert{Header: "Test", Text: "Body"}},
		}

		issue.RegisterSlackPostCreatedOrUpdated("12345.67890", ActionAlert)

		assert.Equal(t, "12345.67890", issue.SlackPostID)
		assert.Equal(t, fixedTime, issue.SlackPostCreated)
		assert.Equal(t, fixedTime, issue.SlackPostUpdated)
		assert.False(t, issue.SlackPostNeedsUpdate)
		assert.Equal(t, ActionAlert, issue.SlackPostLastAction)
		assert.True(t, issue.SlackAlertSentAtLeastOnce)
	})

	t.Run("RegisterSlackPostDeleted", func(t *testing.T) {
		issue := &Issue{
			SlackPostID:          "12345.67890",
			SlackPostNeedsDelete: true,
			SlackPostCreated:     time.Now(),
			SlackPostUpdated:     time.Now(),
			SlackPostHeader:      "Header",
			SlackPostText:        "Text",
		}

		issue.RegisterSlackPostDeleted()

		assert.Empty(t, issue.SlackPostID)
		assert.True(t, issue.SlackPostNeedsUpdate)
		assert.False(t, issue.SlackPostNeedsDelete)
		assert.True(t, issue.SlackPostCreated.IsZero())
		assert.True(t, issue.SlackPostUpdated.IsZero())
		assert.Empty(t, issue.SlackPostHeader)
		assert.Empty(t, issue.SlackPostText)
	})

	t.Run("RegisterTerminationRequest", func(t *testing.T) {
		issue := &Issue{
			LastAlert: &Alert{Alert: types.Alert{IssueFollowUpEnabled: false}},
		}

		issue.RegisterTerminationRequest("user123")

		assert.True(t, issue.IsEmojiTerminated)
		assert.Equal(t, "user123", issue.TerminatedByUser)
	})

	t.Run("RegisterResolveRequest", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			LastAlert: &Alert{Alert: types.Alert{IssueFollowUpEnabled: true}},
		}

		issue.RegisterResolveRequest("user123")

		assert.True(t, issue.IsEmojiResolved)
		assert.Equal(t, "user123", issue.ResolvedByUser)
		assert.Equal(t, fixedTime, issue.ResolveTime)
		assert.True(t, issue.SlackPostNeedsUpdate)
	})

	t.Run("RegisterResolveRequest ignored when follow-up disabled", func(t *testing.T) {
		issue := &Issue{
			LastAlert: &Alert{Alert: types.Alert{IssueFollowUpEnabled: false}},
		}

		issue.RegisterResolveRequest("user123")

		assert.False(t, issue.IsEmojiResolved)
	})

	t.Run("RegisterInvestigateRequest", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			LastAlert: &Alert{Alert: types.Alert{IssueFollowUpEnabled: true}},
		}

		issue.RegisterInvestigateRequest("user123")

		assert.True(t, issue.IsEmojiInvestigated)
		assert.Equal(t, "user123", issue.InvestigatedByUser)
		assert.Equal(t, fixedTime, issue.InvestigatedSince)
		assert.True(t, issue.SlackPostNeedsUpdate)
	})

	t.Run("RegisterMuteRequest", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			LastAlert: &Alert{Alert: types.Alert{IssueFollowUpEnabled: true}},
		}

		issue.RegisterMuteRequest("user123")

		assert.True(t, issue.IsEmojiMuted)
		assert.Equal(t, "user123", issue.MutedByUser)
		assert.Equal(t, fixedTime, issue.MutedSince)
		assert.True(t, issue.SlackPostNeedsUpdate)
	})

	t.Run("RegisterMoveRequest", func(t *testing.T) {
		issue := &Issue{
			LastAlert: &Alert{
				Alert: types.Alert{
					SlackChannelID: "C11111",
				},
				OriginalSlackChannelID: "C11111",
			},
		}

		issue.RegisterMoveRequest(MoveIssueReasonUserCommand, "user123", "C22222", "new-channel")

		assert.True(t, issue.IsMoved)
		assert.Equal(t, MoveIssueReasonUserCommand, issue.MoveReason)
		assert.Equal(t, "user123", issue.MovedByUser)
		assert.Equal(t, "C22222", issue.LastAlert.SlackChannelID)
		assert.Equal(t, "new-channel", issue.LastAlert.SlackChannelName)
		assert.True(t, issue.SlackPostNeedsUpdate)
	})
}

func TestIssue_HasSlackPost(t *testing.T) {
	t.Parallel()

	t.Run("returns true when has post ID", func(t *testing.T) {
		t.Parallel()
		issue := &Issue{SlackPostID: "12345.67890"}
		assert.True(t, issue.HasSlackPost())
	})

	t.Run("returns false when no post ID", func(t *testing.T) {
		t.Parallel()
		issue := &Issue{SlackPostID: ""}
		assert.False(t, issue.HasSlackPost())
	})
}

func TestIssue_IsLowerPriorityThan(t *testing.T) {
	t.Run("error is lower priority than panic", func(t *testing.T) {
		errorIssue := &Issue{
			LastAlert: &Alert{Alert: types.Alert{Severity: types.AlertError}},
		}
		panicIssue := &Issue{
			LastAlert: &Alert{Alert: types.Alert{Severity: types.AlertPanic}},
		}

		assert.True(t, errorIssue.IsLowerPriorityThan(panicIssue))
		assert.False(t, panicIssue.IsLowerPriorityThan(errorIssue))
	})

	t.Run("muted issues treated as resolved for sorting", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		mutedPanic := &Issue{
			IsEmojiMuted: true,
			ResolveTime:  fixedTime.Add(time.Hour),
			LastAlert:    &Alert{Alert: types.Alert{Severity: types.AlertPanic, IssueFollowUpEnabled: true}},
		}
		normalWarning := &Issue{
			ResolveTime: fixedTime.Add(time.Hour),
			LastAlert:   &Alert{Alert: types.Alert{Severity: types.AlertWarning, IssueFollowUpEnabled: true}},
		}

		assert.True(t, mutedPanic.IsLowerPriorityThan(normalWarning))
	})
}

func TestIssue_IsResolved(t *testing.T) {
	t.Run("returns true for resolved severity", func(t *testing.T) {
		issue := &Issue{
			LastAlert: &Alert{Alert: types.Alert{Severity: types.AlertResolved}},
		}

		assert.True(t, issue.IsResolved())
	})

	t.Run("returns true when past resolve time with follow-up", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			ResolveTime: fixedTime.Add(-time.Hour),
			LastAlert: &Alert{
				Alert: types.Alert{
					Severity:             types.AlertError,
					IssueFollowUpEnabled: true,
				},
			},
		}

		assert.True(t, issue.IsResolved())
	})

	t.Run("returns false when before resolve time", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			ResolveTime: fixedTime.Add(time.Hour),
			LastAlert: &Alert{
				Alert: types.Alert{
					Severity:             types.AlertError,
					IssueFollowUpEnabled: true,
				},
			},
		}

		assert.False(t, issue.IsResolved())
	})
}

func TestIssue_LogFields(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for nil issue", func(t *testing.T) {
		t.Parallel()

		var issue *Issue
		assert.Nil(t, issue.LogFields())
	})

	t.Run("returns fields for valid issue", func(t *testing.T) {
		t.Parallel()

		now := time.Now().UTC()
		issue := &Issue{
			CorrelationID:     "corr-123",
			AlertCount:        5,
			LastAlertReceived: now,
			ResolveTime:       now.Add(time.Hour),
			ArchiveTime:       now.Add(2 * time.Hour),
			SlackPostID:       "12345.67890",
			LastAlert: &Alert{
				Alert: types.Alert{
					SlackChannelID:       "C12345",
					Timestamp:            now,
					IssueFollowUpEnabled: true,
				},
				SlackChannelName: "test-channel",
			},
		}

		fields := issue.LogFields()

		assert.NotNil(t, fields)
		assert.Equal(t, "corr-123", fields["correlation_id"])
		assert.Equal(t, "C12345", fields["channel_id"])
		assert.Equal(t, "test-channel", fields["channel_name"])
		assert.Equal(t, 5, fields["alert_count"])
		assert.Equal(t, "12345.67890", fields["slack_post_id"])
		assert.Equal(t, true, fields["follow_up_enabled"])
	})

	t.Run("truncates long correlation ID", func(t *testing.T) {
		t.Parallel()

		longID := strings.Repeat("a", 150)

		issue := &Issue{
			CorrelationID: longID,
			LastAlert: &Alert{
				Alert: types.Alert{
					Timestamp: time.Now(),
				},
			},
		}

		fields := issue.LogFields()

		corrID, ok := fields["correlation_id"].(string)
		require.True(t, ok)
		assert.Len(t, corrID, 100)
	})
}

func TestIssue_MarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("marshals issue to JSON", func(t *testing.T) {
		t.Parallel()

		issue := &Issue{
			ID:            "test-id",
			CorrelationID: "corr-123",
			AlertCount:    1,
		}

		data, err := issue.MarshalJSON()

		require.NoError(t, err)
		assert.Contains(t, string(data), `"id":"test-id"`)
		assert.Contains(t, string(data), `"correlationId":"corr-123"`)
	})

	t.Run("uses cached body when available", func(t *testing.T) {
		t.Parallel()

		cachedData := []byte(`{"cached":"data"}`)
		issue := &Issue{
			ID:             "test-id",
			cachedJSONBody: cachedData,
		}

		data, err := issue.MarshalJSON()

		require.NoError(t, err)
		assert.Equal(t, cachedData, data)
	})
}

func TestIssue_MarshalJSONAndCache(t *testing.T) {
	t.Parallel()

	issue := &Issue{
		ID:            "test-id",
		CorrelationID: "corr-123",
	}

	data, err := issue.MarshalJSONAndCache()

	require.NoError(t, err)
	assert.NotNil(t, data)
	// Verify the data was cached (use EqualValues since cachedJSONBody is json.RawMessage)
	assert.EqualValues(t, data, issue.cachedJSONBody)

	// Second call should return cached data
	data2, err := issue.MarshalJSON()

	require.NoError(t, err)
	assert.Equal(t, data, data2)
}

func TestIssue_ResetCachedJSONBody(t *testing.T) {
	t.Parallel()

	issue := &Issue{
		ID:             "test-id",
		cachedJSONBody: []byte(`{"cached":"data"}`),
	}

	issue.ResetCachedJSONBody()

	assert.Nil(t, issue.cachedJSONBody)
}

func TestIssue_FindWebhook(t *testing.T) {
	t.Parallel()

	t.Run("finds existing webhook", func(t *testing.T) {
		t.Parallel()

		webhook := &types.Webhook{ID: "hook-1", ButtonText: "Test"}
		issue := &Issue{
			LastAlert: &Alert{
				Alert: types.Alert{
					Webhooks: []*types.Webhook{
						{ID: "hook-0"},
						webhook,
						{ID: "hook-2"},
					},
				},
			},
		}

		found := issue.FindWebhook("hook-1")

		assert.Same(t, webhook, found)
	})

	t.Run("returns nil for non-existent webhook", func(t *testing.T) {
		t.Parallel()

		issue := &Issue{
			LastAlert: &Alert{
				Alert: types.Alert{
					Webhooks: []*types.Webhook{
						{ID: "hook-0"},
					},
				},
			},
		}

		found := issue.FindWebhook("hook-999")

		assert.Nil(t, found)
	})
}

func TestIssue_ApplyEscalationRules(t *testing.T) {
	t.Run("no escalation when resolved", func(t *testing.T) {
		issue := &Issue{
			LastAlert: &Alert{
				Alert: types.Alert{
					Severity: types.AlertResolved,
					Escalation: []*types.Escalation{
						{DelaySeconds: 0, Severity: types.AlertPanic},
					},
				},
			},
		}

		result := issue.ApplyEscalationRules()

		assert.False(t, result.Escalated)
	})

	t.Run("no escalation when archived", func(t *testing.T) {
		issue := &Issue{
			Archived: true,
			LastAlert: &Alert{
				Alert: types.Alert{
					Severity: types.AlertError,
					Escalation: []*types.Escalation{
						{DelaySeconds: 0, Severity: types.AlertPanic},
					},
				},
			},
		}

		result := issue.ApplyEscalationRules()

		assert.False(t, result.Escalated)
	})

	t.Run("no escalation when no rules", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			ResolveTime: fixedTime.Add(time.Hour),
			LastAlert: &Alert{
				Alert: types.Alert{
					Severity:             types.AlertError,
					IssueFollowUpEnabled: true,
					Escalation:           nil,
				},
			},
		}

		result := issue.ApplyEscalationRules()

		assert.False(t, result.Escalated)
	})

	t.Run("applies escalation when delay passed", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			Created:     fixedTime.Add(-2 * time.Hour),
			ResolveTime: fixedTime.Add(time.Hour),
			LastAlert: &Alert{
				Alert: types.Alert{
					Severity:             types.AlertWarning,
					IssueFollowUpEnabled: true,
					Escalation: []*types.Escalation{
						{DelaySeconds: 3600, Severity: types.AlertError, SlackMentions: []string{"<@user1>"}},
					},
					Text: "Original text",
				},
				OriginalText: "Original text",
			},
		}

		result := issue.ApplyEscalationRules()

		assert.True(t, result.Escalated)
		assert.True(t, issue.IsEscalated)
		assert.Equal(t, types.AlertError, issue.LastAlert.Severity)
		assert.Contains(t, issue.LastAlert.Text, "<@user1>")
	})
}

func TestIssue_RegisterSlackPostInvalidBlocks(t *testing.T) {
	t.Parallel()

	issue := &Issue{
		SlackPostID:          "12345.67890",
		SlackPostNeedsUpdate: true,
		SlackPostNeedsDelete: true,
		SlackPostCreated:     time.Now(),
		SlackPostUpdated:     time.Now(),
		SlackPostHeader:      "Header",
		SlackPostText:        "Text",
	}

	issue.RegisterSlackPostInvalidBlocks()

	assert.Empty(t, issue.SlackPostID)
	assert.False(t, issue.SlackPostNeedsUpdate)
	assert.False(t, issue.SlackPostNeedsDelete)
	assert.True(t, issue.SlackPostCreated.IsZero())
	assert.True(t, issue.SlackPostUpdated.IsZero())
	assert.Empty(t, issue.SlackPostHeader)
	assert.Empty(t, issue.SlackPostText)
}

func TestIssue_RegisterUnresolveRequest(t *testing.T) {
	t.Run("unresolves issue when follow-up enabled", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		alertTime := fixedTime.Add(-time.Hour)
		issue := &Issue{
			IsEmojiResolved:   true,
			ResolvedByUser:    "user123",
			ResolveTime:       fixedTime,
			AutoResolvePeriod: 2 * time.Hour,
			LastAlert: &Alert{
				Alert: types.Alert{
					Timestamp:            alertTime,
					IssueFollowUpEnabled: true,
				},
			},
		}

		issue.RegisterUnresolveRequest()

		assert.False(t, issue.IsEmojiResolved)
		assert.Empty(t, issue.ResolvedByUser)
		expectedResolveTime := alertTime.Add(2 * time.Hour)
		assert.Equal(t, expectedResolveTime, issue.ResolveTime)
		assert.True(t, issue.SlackPostNeedsUpdate)
	})

	t.Run("does nothing when follow-up disabled", func(t *testing.T) {
		issue := &Issue{
			IsEmojiResolved: true,
			ResolvedByUser:  "user123",
			LastAlert: &Alert{
				Alert: types.Alert{IssueFollowUpEnabled: false},
			},
		}

		issue.RegisterUnresolveRequest()

		assert.True(t, issue.IsEmojiResolved)
		assert.Equal(t, "user123", issue.ResolvedByUser)
	})
}

func TestIssue_RegisterUninvestigateRequest(t *testing.T) {
	t.Parallel()

	t.Run("uninvestigates issue when follow-up enabled", func(t *testing.T) {
		t.Parallel()

		issue := &Issue{
			IsEmojiInvestigated:  true,
			InvestigatedByUser:   "user123",
			InvestigatedSince:    time.Now(),
			SlackPostNeedsUpdate: false,
			LastAlert: &Alert{
				Alert: types.Alert{IssueFollowUpEnabled: true},
			},
		}

		issue.RegisterUninvestigateRequest()

		assert.False(t, issue.IsEmojiInvestigated)
		assert.Empty(t, issue.InvestigatedByUser)
		assert.True(t, issue.InvestigatedSince.IsZero())
		assert.True(t, issue.SlackPostNeedsUpdate)
	})

	t.Run("does nothing when follow-up disabled", func(t *testing.T) {
		t.Parallel()

		issue := &Issue{
			IsEmojiInvestigated: true,
			InvestigatedByUser:  "user123",
			LastAlert: &Alert{
				Alert: types.Alert{IssueFollowUpEnabled: false},
			},
		}

		issue.RegisterUninvestigateRequest()

		assert.True(t, issue.IsEmojiInvestigated)
		assert.Equal(t, "user123", issue.InvestigatedByUser)
	})
}

func TestIssue_RegisterUnmuteRequest(t *testing.T) {
	t.Parallel()

	t.Run("unmutes issue when follow-up enabled", func(t *testing.T) {
		t.Parallel()

		issue := &Issue{
			IsEmojiMuted:         true,
			MutedByUser:          "user123",
			MutedSince:           time.Now(),
			SlackPostNeedsUpdate: false,
			LastAlert: &Alert{
				Alert: types.Alert{IssueFollowUpEnabled: true},
			},
		}

		issue.RegisterUnmuteRequest()

		assert.False(t, issue.IsEmojiMuted)
		assert.Empty(t, issue.MutedByUser)
		assert.True(t, issue.MutedSince.IsZero())
		assert.True(t, issue.SlackPostNeedsUpdate)
	})

	t.Run("does nothing when follow-up disabled", func(t *testing.T) {
		t.Parallel()

		issue := &Issue{
			IsEmojiMuted: true,
			MutedByUser:  "user123",
			LastAlert: &Alert{
				Alert: types.Alert{IssueFollowUpEnabled: false},
			},
		}

		issue.RegisterUnmuteRequest()

		assert.True(t, issue.IsEmojiMuted)
		assert.Equal(t, "user123", issue.MutedByUser)
	})
}

func TestIssue_RegisterShowOptionButtonsRequest(t *testing.T) {
	t.Parallel()

	issue := &Issue{
		IsEmojiButtonsActivated: false,
		SlackPostNeedsUpdate:    false,
	}

	issue.RegisterShowOptionButtonsRequest()

	assert.True(t, issue.IsEmojiButtonsActivated)
	assert.True(t, issue.SlackPostNeedsUpdate)
}

func TestIssue_RegisterHideOptionButtonsRequest(t *testing.T) {
	t.Parallel()

	issue := &Issue{
		IsEmojiButtonsActivated: true,
		SlackPostNeedsUpdate:    false,
	}

	issue.RegisterHideOptionButtonsRequest()

	assert.False(t, issue.IsEmojiButtonsActivated)
	assert.True(t, issue.SlackPostNeedsUpdate)
}

func TestIssue_IsReadyForArchiving(t *testing.T) {
	t.Run("fire-and-forget issue ready after delay", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			ArchiveTime: fixedTime.Add(-time.Minute),
			LastAlert: &Alert{
				Alert: types.Alert{IssueFollowUpEnabled: false},
			},
		}

		assert.True(t, issue.IsReadyForArchiving())
	})

	t.Run("fire-and-forget issue not ready before delay", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			ArchiveTime: fixedTime.Add(time.Minute),
			LastAlert: &Alert{
				Alert: types.Alert{IssueFollowUpEnabled: false},
			},
		}

		assert.False(t, issue.IsReadyForArchiving())
	})

	t.Run("follow-up issue not ready with unresolved slack post", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			ArchiveTime:         fixedTime.Add(-time.Minute),
			SlackPostID:         "12345.67890",
			SlackPostLastAction: ActionAlert,
			LastAlert: &Alert{
				Alert: types.Alert{IssueFollowUpEnabled: true},
			},
		}

		assert.False(t, issue.IsReadyForArchiving())
	})

	t.Run("follow-up issue ready when resolved and past archive time", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			ArchiveTime:         fixedTime.Add(-time.Minute),
			SlackPostID:         "12345.67890",
			SlackPostLastAction: ActionResolve,
			LastAlert: &Alert{
				Alert: types.Alert{IssueFollowUpEnabled: true},
			},
		}

		assert.True(t, issue.IsReadyForArchiving())
	})

	t.Run("terminated issue ready for archiving", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			IsEmojiTerminated:   true,
			ArchiveTime:         fixedTime.Add(-time.Minute),
			SlackPostID:         "12345.67890",
			SlackPostLastAction: ActionAlert,
			LastAlert: &Alert{
				Alert: types.Alert{IssueFollowUpEnabled: true},
			},
		}

		assert.True(t, issue.IsReadyForArchiving())
	})
}

func TestIssue_RegisterArchiving(t *testing.T) {
	t.Parallel()

	issue := &Issue{Archived: false}

	issue.RegisterArchiving()

	assert.True(t, issue.Archived)
}

func TestIssue_LastAlertHasActiveMentions(t *testing.T) {
	t.Parallel()

	t.Run("returns true when text has mentions", func(t *testing.T) {
		t.Parallel()

		issue := &Issue{
			LastAlert: &Alert{
				Alert: types.Alert{Text: "Hello <@U12345> please check this"},
			},
		}

		assert.True(t, issue.LastAlertHasActiveMentions())
	})

	t.Run("returns true for channel mention", func(t *testing.T) {
		t.Parallel()

		issue := &Issue{
			LastAlert: &Alert{
				Alert: types.Alert{Text: "Hello <!here> please check this"},
			},
		}

		assert.True(t, issue.LastAlertHasActiveMentions())
	})

	t.Run("returns false when no mentions", func(t *testing.T) {
		t.Parallel()

		issue := &Issue{
			LastAlert: &Alert{
				Alert: types.Alert{Text: "Hello world"},
			},
		}

		assert.False(t, issue.LastAlertHasActiveMentions())
	})

	t.Run("returns false for muted mentions", func(t *testing.T) {
		t.Parallel()

		issue := &Issue{
			LastAlert: &Alert{
				Alert: types.Alert{Text: "Hello *U12345* please check this"},
			},
		}

		assert.False(t, issue.LastAlertHasActiveMentions())
	})
}

func TestIssue_IsResolvedAsInconclusive(t *testing.T) {
	t.Parallel()

	t.Run("returns true when auto-resolve as inconclusive is set", func(t *testing.T) {
		t.Parallel()

		issue := &Issue{
			LastAlert: &Alert{
				Alert: types.Alert{
					AutoResolveAsInconclusive: true,
					Severity:                  types.AlertError,
				},
			},
		}

		assert.True(t, issue.IsResolvedAsInconclusive())
	})

	t.Run("returns false when auto-resolve as inconclusive is not set", func(t *testing.T) {
		t.Parallel()

		issue := &Issue{
			LastAlert: &Alert{
				Alert: types.Alert{
					AutoResolveAsInconclusive: false,
					Severity:                  types.AlertError,
				},
			},
		}

		assert.False(t, issue.IsResolvedAsInconclusive())
	})

	t.Run("returns false for info severity", func(t *testing.T) {
		t.Parallel()

		issue := &Issue{
			LastAlert: &Alert{
				Alert: types.Alert{
					AutoResolveAsInconclusive: true,
					Severity:                  types.AlertInfo,
				},
			},
		}

		assert.False(t, issue.IsResolvedAsInconclusive())
	})

	t.Run("returns false for resolved severity", func(t *testing.T) {
		t.Parallel()

		issue := &Issue{
			LastAlert: &Alert{
				Alert: types.Alert{
					AutoResolveAsInconclusive: true,
					Severity:                  types.AlertResolved,
				},
			},
		}

		assert.False(t, issue.IsResolvedAsInconclusive())
	})

	t.Run("returns false when manually resolved", func(t *testing.T) {
		t.Parallel()

		issue := &Issue{
			IsEmojiResolved: true,
			LastAlert: &Alert{
				Alert: types.Alert{
					AutoResolveAsInconclusive: true,
					Severity:                  types.AlertError,
				},
			},
		}

		assert.False(t, issue.IsResolvedAsInconclusive())
	})
}

func TestIssue_IsInfoOrResolved(t *testing.T) {
	t.Run("returns true for info severity", func(t *testing.T) {
		issue := &Issue{
			LastAlert: &Alert{
				Alert: types.Alert{Severity: types.AlertInfo},
			},
		}

		assert.True(t, issue.IsInfoOrResolved())
	})

	t.Run("returns true for resolved severity", func(t *testing.T) {
		issue := &Issue{
			LastAlert: &Alert{
				Alert: types.Alert{Severity: types.AlertResolved},
			},
		}

		assert.True(t, issue.IsInfoOrResolved())
	})

	t.Run("returns true when past resolve time with follow-up", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			ResolveTime: fixedTime.Add(-time.Hour),
			LastAlert: &Alert{
				Alert: types.Alert{
					Severity:             types.AlertError,
					IssueFollowUpEnabled: true,
				},
			},
		}

		assert.True(t, issue.IsInfoOrResolved())
	})

	t.Run("returns false for active error", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setTestTime(fixedTime)
		defer cleanup()

		issue := &Issue{
			ResolveTime: fixedTime.Add(time.Hour),
			LastAlert: &Alert{
				Alert: types.Alert{
					Severity:             types.AlertError,
					IssueFollowUpEnabled: true,
				},
			},
		}

		assert.False(t, issue.IsInfoOrResolved())
	})
}

func TestSlackMentionHandling(t *testing.T) {
	t.Parallel()

	t.Run("texts without mentions are unchanged", func(t *testing.T) {
		t.Parallel()

		issue := Issue{
			LastAlert: &Alert{
				Alert: types.Alert{
					Text: "abc everyone hei",
				},
			},
		}
		issue.sanitizeSlackMentions(false)
		assert.Equal(t, "abc everyone hei", issue.LastAlert.Text)
	})

	t.Run("mentions are only allowed in the text field", func(t *testing.T) {
		t.Parallel()

		issue := Issue{
			LastAlert: &Alert{
				Alert: types.Alert{
					Header: "hei <@a>",
					Author: "hei <@b>",
					Host:   "hei <@c>",
					Footer: "hei <@d>",
					Text:   "hei <@e>",
					Fields: []*types.Field{
						{
							Title: "hei <!here>",
							Value: "hei <@there>",
						},
					},
				},
			},
		}
		issue.sanitizeSlackMentions(false)
		assert.Equal(t, "hei *a*", issue.LastAlert.Header)
		assert.Equal(t, "hei *b*", issue.LastAlert.Author)
		assert.Equal(t, "hei *c*", issue.LastAlert.Host)
		assert.Equal(t, "hei *d*", issue.LastAlert.Footer)
		assert.Equal(t, "hei <@e>", issue.LastAlert.Text)
		assert.Equal(t, "hei *here*", issue.LastAlert.Fields[0].Title)
		assert.Equal(t, "hei *there*", issue.LastAlert.Fields[0].Value)
	})

	t.Run("everyone mention is not allowed in the text field", func(t *testing.T) {
		t.Parallel()

		issue := Issue{
			LastAlert: &Alert{
				Alert: types.Alert{
					Text: "abc <!everyone> hei <@everyone>",
				},
			},
		}
		issue.sanitizeSlackMentions(false)
		assert.Equal(t, "abc *everyone* hei *everyone*", issue.LastAlert.Text)
	})

	t.Run("mentions are muted after first use within threshold", func(t *testing.T) {
		issue := Issue{
			LastAlert: &Alert{
				Alert: types.Alert{
					Text: "abc <!here> hei <@bar>",
				},
			},
		}
		issue.sanitizeSlackMentions(false)
		assert.Equal(t, "abc <!here> hei <@bar>", issue.LastAlert.Text)
		issue.sanitizeSlackMentions(false)
		assert.Equal(t, "abc *here* hei *bar*", issue.LastAlert.Text)
	})

	t.Run("mentions are allowed when text changes", func(t *testing.T) {
		issue := Issue{
			LastAlert: &Alert{
				Alert: types.Alert{
					Text: "abc <!here> hei <@bar>",
				},
			},
		}
		issue.sanitizeSlackMentions(false)
		assert.Equal(t, "abc <!here> hei <@bar>", issue.LastAlert.Text)
		issue.LastAlert.Text = "abc <!channel> hei <@bar>"
		issue.sanitizeSlackMentions(false)
		assert.Equal(t, "abc <!channel> hei <@bar>", issue.LastAlert.Text)
	})

	t.Run("skipMuting allows mentions regardless of previous state", func(t *testing.T) {
		issue := Issue{
			LastSlackMention:     "<!here>,<@bar>",
			LastSlackMentionTime: time.Now(),
			LastAlert: &Alert{
				Alert: types.Alert{
					Text: "abc <!here> hei <@bar>",
				},
			},
		}
		issue.sanitizeSlackMentions(true)
		assert.Equal(t, "abc <!here> hei <@bar>", issue.LastAlert.Text)
	})
}
