package models //nolint:testpackage // Internal test to access unexported fields

import (
	"encoding/json"
	"testing"
	"time"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setCommandTestTime sets commandNowFunc to return a fixed time for testing
func setCommandTestTime(t time.Time) func() {
	original := commandNowFunc
	commandNowFunc = func() time.Time { return t }
	return func() { commandNowFunc = original }
}

func TestNewCommandFromQueueItem(t *testing.T) {
	t.Parallel()

	t.Run("returns error for empty body", func(t *testing.T) {
		t.Parallel()

		queueItem := &common.FifoQueueItem{
			Body: "",
		}

		cmd, err := NewCommandFromQueueItem(queueItem)

		assert.Nil(t, cmd)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "command body is empty")
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		t.Parallel()

		queueItem := &common.FifoQueueItem{
			Body: "invalid json",
		}

		cmd, err := NewCommandFromQueueItem(queueItem)

		assert.Nil(t, cmd)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal message body")
	})

	t.Run("creates command from valid JSON", func(t *testing.T) {
		t.Parallel()

		cmdData := Command{
			SlackChannelID: "C12345",
			SlackPostID:    "1234567890.123456",
			Reaction:       "white_check_mark",
			UserID:         "U12345",
			UserRealName:   "Test User",
			Action:         CommandActionResolveIssue,
		}

		jsonBody, err := json.Marshal(cmdData)
		require.NoError(t, err)

		ackCalled := false
		nackCalled := false

		queueItem := &common.FifoQueueItem{
			Body: string(jsonBody),
			Ack:  func() { ackCalled = true },
			Nack: func() { nackCalled = true },
		}

		result, err := NewCommandFromQueueItem(queueItem)

		require.NoError(t, err)
		require.NotNil(t, result)

		cmd, ok := result.(*Command)
		require.True(t, ok)

		assert.Equal(t, "C12345", cmd.SlackChannelID)
		assert.Equal(t, "1234567890.123456", cmd.SlackPostID)
		assert.Equal(t, "white_check_mark", cmd.Reaction)
		assert.Equal(t, "U12345", cmd.UserID)
		assert.Equal(t, "Test User", cmd.UserRealName)
		assert.Equal(t, CommandActionResolveIssue, cmd.Action)

		// Verify ack function is set
		cmd.Ack()
		assert.True(t, ackCalled)
		assert.False(t, nackCalled)
	})

	t.Run("creates command with parameters", func(t *testing.T) {
		t.Parallel()

		cmdData := Command{
			SlackChannelID: "C12345",
			Action:         CommandActionMoveIssue,
			Parameters: map[string]any{
				"targetChannel": "C67890",
				"count":         float64(10),
				"enabled":       true,
			},
		}

		jsonBody, err := json.Marshal(cmdData)
		require.NoError(t, err)

		queueItem := &common.FifoQueueItem{
			Body: string(jsonBody),
		}

		result, err := NewCommandFromQueueItem(queueItem)

		require.NoError(t, err)
		cmd, ok := result.(*Command)
		require.True(t, ok)

		assert.Equal(t, "C67890", cmd.ParamAsString("targetChannel"))
		assert.Equal(t, 10, cmd.ParamAsInt("count"))
		assert.True(t, cmd.ParamAsBool("enabled"))
	})
}

func TestNewCommand(t *testing.T) {
	t.Run("creates command with provided values", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setCommandTestTime(fixedTime)
		defer cleanup()

		params := map[string]any{"key": "value"}

		cmd := NewCommand("C12345", "1234567890.123456", "eyes", "U12345", "Test User", CommandActionInvestigateIssue, params)

		require.NotNil(t, cmd)
		assert.Equal(t, fixedTime, cmd.Timestamp)
		assert.Equal(t, "C12345", cmd.SlackChannelID)
		assert.Equal(t, "1234567890.123456", cmd.SlackPostID)
		assert.Equal(t, "eyes", cmd.Reaction)
		assert.Equal(t, "U12345", cmd.UserID)
		assert.Equal(t, "Test User", cmd.UserRealName)
		assert.Equal(t, CommandActionInvestigateIssue, cmd.Action)
		assert.Equal(t, params, cmd.Parameters)
	})

	t.Run("creates command with nil parameters", func(t *testing.T) {
		cmd := NewCommand("C12345", "", "", "", "", CommandActionCreateIssue, nil)

		require.NotNil(t, cmd)
		assert.Nil(t, cmd.Parameters)
	})
}

func TestCommand_Ack(t *testing.T) {
	t.Parallel()

	t.Run("calls ack function when set", func(t *testing.T) {
		t.Parallel()

		ackCalled := false
		cmd := &Command{
			ack: func() { ackCalled = true },
		}

		cmd.Ack()

		assert.True(t, ackCalled)
		assert.Nil(t, cmd.ack)
		assert.Nil(t, cmd.nack)
	})

	t.Run("clears both ack and nack", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{
			ack:  func() {},
			nack: func() {},
		}

		cmd.Ack()

		assert.Nil(t, cmd.ack)
		assert.Nil(t, cmd.nack)
	})

	t.Run("does nothing when ack is nil", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{}

		// Should not panic
		cmd.Ack()

		assert.Nil(t, cmd.ack)
		assert.Nil(t, cmd.nack)
	})

	t.Run("subsequent calls have no effect", func(t *testing.T) {
		t.Parallel()

		callCount := 0
		cmd := &Command{
			ack: func() { callCount++ },
		}

		cmd.Ack()
		cmd.Ack()
		cmd.Ack()

		assert.Equal(t, 1, callCount)
	})
}

func TestCommand_Nack(t *testing.T) {
	t.Parallel()

	t.Run("calls nack function when set", func(t *testing.T) {
		t.Parallel()

		nackCalled := false
		cmd := &Command{
			nack: func() { nackCalled = true },
		}

		cmd.Nack()

		assert.True(t, nackCalled)
		assert.Nil(t, cmd.ack)
		assert.Nil(t, cmd.nack)
	})

	t.Run("clears both ack and nack", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{
			ack:  func() {},
			nack: func() {},
		}

		cmd.Nack()

		assert.Nil(t, cmd.ack)
		assert.Nil(t, cmd.nack)
	})

	t.Run("does nothing when nack is nil", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{}

		// Should not panic
		cmd.Nack()

		assert.Nil(t, cmd.ack)
		assert.Nil(t, cmd.nack)
	})

	t.Run("subsequent calls have no effect", func(t *testing.T) {
		t.Parallel()

		callCount := 0
		cmd := &Command{
			nack: func() { callCount++ },
		}

		cmd.Nack()
		cmd.Nack()
		cmd.Nack()

		assert.Equal(t, 1, callCount)
	})
}

func TestCommand_DedupID(t *testing.T) {
	t.Parallel()

	t.Run("generates consistent dedup ID", func(t *testing.T) {
		t.Parallel()

		timestamp := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

		cmd := &Command{
			SlackChannelID: "C12345",
			Action:         CommandActionResolveIssue,
			Timestamp:      timestamp,
		}

		dedupID := cmd.DedupID()

		expected := "command::C12345::resolve_issue::2024-01-15T10:30:00Z"
		assert.Equal(t, expected, dedupID)
	})

	t.Run("different timestamps produce different dedup IDs", func(t *testing.T) {
		t.Parallel()

		timestamp1 := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		timestamp2 := time.Date(2024, 1, 15, 10, 30, 1, 0, time.UTC)

		cmd1 := &Command{
			SlackChannelID: "C12345",
			Action:         CommandActionResolveIssue,
			Timestamp:      timestamp1,
		}

		cmd2 := &Command{
			SlackChannelID: "C12345",
			Action:         CommandActionResolveIssue,
			Timestamp:      timestamp2,
		}

		assert.NotEqual(t, cmd1.DedupID(), cmd2.DedupID())
	})
}

func TestCommand_LogFields(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for nil command", func(t *testing.T) {
		t.Parallel()

		var cmd *Command

		fields := cmd.LogFields()

		assert.Nil(t, fields)
	})

	t.Run("returns fields for valid command", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{
			SlackChannelID: "C12345",
			SlackPostID:    "1234567890.123456",
			Reaction:       "white_check_mark",
			Action:         CommandActionResolveIssue,
			UserID:         "U12345",
			UserRealName:   "Test User",
		}

		fields := cmd.LogFields()

		require.NotNil(t, fields)
		assert.Equal(t, "C12345", fields["channel_id"])
		assert.Equal(t, "1234567890.123456", fields["slack_post_id"])
		assert.Equal(t, "white_check_mark", fields["reaction"])
		assert.Equal(t, CommandActionResolveIssue, fields["action"])
		assert.Equal(t, "U12345", fields["user_id"])
		assert.Equal(t, "Test User", fields["user_name"])
	})

	t.Run("includes parameters when present", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{
			SlackChannelID: "C12345",
			Parameters:     map[string]any{"key": "value"},
		}

		fields := cmd.LogFields()

		require.NotNil(t, fields)
		assert.Contains(t, fields, "params")
	})

	t.Run("excludes parameters when nil", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{
			SlackChannelID: "C12345",
			Parameters:     nil,
		}

		fields := cmd.LogFields()

		require.NotNil(t, fields)
		_, hasParams := fields["params"]
		assert.False(t, hasParams)
	})
}

func TestCommand_ParamAsString(t *testing.T) {
	t.Parallel()

	t.Run("returns empty string for nil parameters", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{Parameters: nil}

		result := cmd.ParamAsString("key")

		assert.Empty(t, result)
	})

	t.Run("returns empty string for missing key", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{Parameters: map[string]any{}}

		result := cmd.ParamAsString("missing")

		assert.Empty(t, result)
	})

	t.Run("returns string value", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{
			Parameters: map[string]any{
				"key": "value",
			},
		}

		result := cmd.ParamAsString("key")

		assert.Equal(t, "value", result)
	})

	t.Run("returns empty string for non-string value", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{
			Parameters: map[string]any{
				"key": 123,
			},
		}

		result := cmd.ParamAsString("key")

		assert.Empty(t, result)
	})
}

func TestCommand_ParamAsBool(t *testing.T) {
	t.Parallel()

	t.Run("returns false for nil parameters", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{Parameters: nil}

		result := cmd.ParamAsBool("key")

		assert.False(t, result)
	})

	t.Run("returns false for missing key", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{Parameters: map[string]any{}}

		result := cmd.ParamAsBool("missing")

		assert.False(t, result)
	})

	t.Run("returns true value", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{
			Parameters: map[string]any{
				"key": true,
			},
		}

		result := cmd.ParamAsBool("key")

		assert.True(t, result)
	})

	t.Run("returns false value", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{
			Parameters: map[string]any{
				"key": false,
			},
		}

		result := cmd.ParamAsBool("key")

		assert.False(t, result)
	})

	t.Run("returns false for non-bool value", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{
			Parameters: map[string]any{
				"key": "true",
			},
		}

		result := cmd.ParamAsBool("key")

		assert.False(t, result)
	})
}

func TestCommand_ParamAsInt(t *testing.T) {
	t.Parallel()

	t.Run("returns zero for nil parameters", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{Parameters: nil}

		result := cmd.ParamAsInt("key")

		assert.Equal(t, 0, result)
	})

	t.Run("returns zero for missing key", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{Parameters: map[string]any{}}

		result := cmd.ParamAsInt("missing")

		assert.Equal(t, 0, result)
	})

	t.Run("returns int value from float64", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{
			Parameters: map[string]any{
				"key": float64(42),
			},
		}

		result := cmd.ParamAsInt("key")

		assert.Equal(t, 42, result)
	})

	t.Run("returns zero for non-float64 value", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{
			Parameters: map[string]any{
				"key": "42",
			},
		}

		result := cmd.ParamAsInt("key")

		assert.Equal(t, 0, result)
	})

	t.Run("truncates float to int", func(t *testing.T) {
		t.Parallel()

		cmd := &Command{
			Parameters: map[string]any{
				"key": float64(42.9),
			},
		}

		result := cmd.ParamAsInt("key")

		assert.Equal(t, 42, result)
	})
}

func TestCommandActionConstants(t *testing.T) {
	t.Parallel()

	// Verify command action constants have expected values
	assert.Equal(t, CommandActionTerminateIssue, CommandAction("terminate_issue"))
	assert.Equal(t, CommandActionResolveIssue, CommandAction("resolve_issue"))
	assert.Equal(t, CommandActionUnresolveIssue, CommandAction("unresolve_issue"))
	assert.Equal(t, CommandActionInvestigateIssue, CommandAction("investigate_issue"))
	assert.Equal(t, CommandActionUninvestigateIssue, CommandAction("uninvestigate_issue"))
	assert.Equal(t, CommandActionMuteIssue, CommandAction("mute_issue"))
	assert.Equal(t, CommandActionUnmuteIssue, CommandAction("unmute_issue"))
	assert.Equal(t, CommandActionMoveIssue, CommandAction("move_issue"))
	assert.Equal(t, CommandActionCreateIssue, CommandAction("create_issue"))
	assert.Equal(t, CommandActionShowIssueOptionButtons, CommandAction("show_issue_option_buttons"))
	assert.Equal(t, CommandActionHideIssueOptionButtons, CommandAction("hide_issue_option_buttons"))
	assert.Equal(t, CommandActionWebhook, CommandAction("webhook"))
}
