package models //nolint:testpackage // Internal test to access unexported fields

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPostClickTestIssue() *Issue {
	alert := &Alert{Alert: types.Alert{
		SlackChannelID:       "C123",
		Header:               "Header",
		Severity:             types.AlertError,
		CorrelationID:        "corr",
		Timestamp:            time.Now(),
		IssueFollowUpEnabled: true,
		AutoResolveSeconds:   3600,
		Webhooks: []*types.Webhook{
			{ID: "restart", ButtonText: "Restart", AfterClick: &types.WebhookAfterClick{
				HideButtons:       types.WebhookHideButtonsClicked,
				HideWebhookIDs:    []string{"rollback"},
				StatusText:        "Restart in progress <!here> <@U999>",
				ResetAfterSeconds: 60,
			}},
			{ID: "rollback", ButtonText: "Rollback"},
			{ID: "ack", ButtonText: "Ack"},
		},
	}}

	issue := NewIssue(alert, &mockLogger{})
	issue.SlackPostNeedsUpdate = false

	return issue
}

func newPostClickTestCommand(webhookID string, clicked time.Time) *Command {
	cmd := NewCommand("C123", "1234.5678", "", "U123", "Jane", CommandActionWebhook, nil)
	cmd.Timestamp = clicked
	cmd.WebhookParameters = &WebhookCommandParams{WebhookID: webhookID}

	return cmd
}

func deliveredResult(cmd *Command) *Command {
	return NewWebhookResultCommand(cmd, WebhookOutcomeDelivered)
}

func deliveredResultWithAfterClick(cmd *Command, afterClick *types.WebhookAfterClick) *Command {
	result := NewWebhookResultCommand(cmd, WebhookOutcomeDelivered)
	result.WebhookResult.AfterClick = afterClick

	return result
}

func TestIssue_RegisterWebhookClick(t *testing.T) {
	t.Parallel()

	clicked := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

	t.Run("webhook without after-click settings", func(t *testing.T) {
		t.Parallel()

		issue := newPostClickTestIssue()

		assert.False(t, issue.RegisterWebhookClick(issue.FindWebhook("ack"), newPostClickTestCommand("ack", clicked)))
		assert.False(t, issue.RegisterWebhookClick(nil, newPostClickTestCommand("ack", clicked)))
		assert.False(t, issue.RegisterWebhookClick(issue.FindWebhook("restart"), nil))
		assert.Nil(t, issue.PendingPostClick)
	})

	t.Run("registers a sanitized copy as pending", func(t *testing.T) {
		t.Parallel()

		issue := newPostClickTestIssue()
		webhook := issue.FindWebhook("restart")

		require.True(t, issue.RegisterWebhookClick(webhook, newPostClickTestCommand("restart", clicked)))

		pending := issue.PendingPostClick
		require.NotNil(t, pending)
		assert.Equal(t, "restart", pending.WebhookID)
		assert.Equal(t, "Restart", pending.ButtonText)
		assert.Equal(t, "U123", pending.UserID)
		assert.Equal(t, "Jane", pending.UserRealName)
		assert.Equal(t, clicked, pending.Clicked)
		assert.Equal(t, "Restart in progress *here* *U999*", pending.AfterClick.StatusText)
		assert.Equal(t, types.WebhookHideButtonsClicked, pending.AfterClick.HideButtons)
		assert.True(t, pending.ExpiresAt.IsZero())
		assert.Nil(t, issue.PostClick)
		assert.False(t, issue.SlackPostNeedsUpdate, "nothing is shown while pending")

		// The state is a copy, not affected by later changes to the webhook
		webhook.AfterClick.HideWebhookIDs[0] = "changed"
		webhook.AfterClick.StatusText = "changed"
		assert.Equal(t, []string{"rollback"}, pending.AfterClick.HideWebhookIDs)
		assert.Equal(t, "Restart in progress *here* *U999*", pending.AfterClick.StatusText)
	})

	t.Run("a new click replaces pending but keeps the active state", func(t *testing.T) {
		t.Parallel()

		issue := newPostClickTestIssue()
		first := newPostClickTestCommand("restart", clicked)

		require.True(t, issue.RegisterWebhookClick(issue.FindWebhook("restart"), first))
		issue.ApplyWebhookResult(deliveredResult(first))
		active := issue.PostClick
		require.NotNil(t, active)

		second := newPostClickTestCommand("restart", clicked.Add(time.Minute))
		require.True(t, issue.RegisterWebhookClick(issue.FindWebhook("restart"), second))

		assert.Same(t, active, issue.PostClick)
		assert.Equal(t, second.Timestamp, issue.PendingPostClick.Clicked)
	})
}

func TestIssue_ApplyWebhookResult(t *testing.T) {
	t.Parallel()

	clicked := time.Now().UTC().Add(-time.Second)

	t.Run("delivered activates the pending state", func(t *testing.T) {
		t.Parallel()

		issue := newPostClickTestIssue()
		cmd := newPostClickTestCommand("restart", clicked)
		issue.RegisterWebhookClick(issue.FindWebhook("restart"), cmd)

		before := time.Now().UTC()
		changed, slackUpdate := issue.ApplyWebhookResult(deliveredResult(cmd))

		assert.True(t, changed)
		assert.True(t, slackUpdate)
		assert.True(t, issue.SlackPostNeedsUpdate)
		assert.Nil(t, issue.PendingPostClick)
		require.NotNil(t, issue.PostClick)
		assert.False(t, issue.PostClick.Expired)
		assert.WithinRange(t, issue.PostClick.ExpiresAt, before.Add(time.Minute), time.Now().UTC().Add(time.Minute))
	})

	t.Run("failed discards the pending state", func(t *testing.T) {
		t.Parallel()

		issue := newPostClickTestIssue()
		cmd := newPostClickTestCommand("restart", clicked)
		issue.RegisterWebhookClick(issue.FindWebhook("restart"), cmd)

		changed, slackUpdate := issue.ApplyWebhookResult(NewWebhookResultCommand(cmd, WebhookOutcomeFailed))

		assert.True(t, changed)
		assert.False(t, slackUpdate)
		assert.Nil(t, issue.PendingPostClick)
		assert.Nil(t, issue.PostClick)
		assert.False(t, issue.SlackPostNeedsUpdate)
	})

	tests := []struct {
		name   string
		result func(cmd *Command) *Command
	}{
		{name: "nil command", result: func(_ *Command) *Command { return nil }},
		{name: "nil result", result: func(cmd *Command) *Command {
			r := deliveredResult(cmd)
			r.WebhookResult = nil
			return r
		}},
		{name: "other webhook", result: func(cmd *Command) *Command {
			r := deliveredResult(cmd)
			r.WebhookResult.WebhookID = "ack"
			return r
		}},
		{name: "other click", result: func(cmd *Command) *Command {
			r := deliveredResult(cmd)
			r.WebhookResult.Clicked = r.WebhookResult.Clicked.Add(-time.Second)
			return r
		}},
		{name: "failed other click", result: func(cmd *Command) *Command {
			r := NewWebhookResultCommand(cmd, WebhookOutcomeFailed)
			r.WebhookResult.Clicked = r.WebhookResult.Clicked.Add(-time.Second)
			return r
		}},
		{name: "older click with after-click", result: func(cmd *Command) *Command {
			r := deliveredResultWithAfterClick(cmd, &types.WebhookAfterClick{StatusText: "older"})
			r.WebhookResult.WebhookID = "ack"
			r.WebhookResult.Clicked = r.WebhookResult.Clicked.Add(-time.Second)
			return r
		}},
	}

	for _, tt := range tests {
		t.Run("ignored: "+tt.name, func(t *testing.T) {
			t.Parallel()

			issue := newPostClickTestIssue()
			cmd := newPostClickTestCommand("restart", clicked)
			issue.RegisterWebhookClick(issue.FindWebhook("restart"), cmd)

			changed, slackUpdate := issue.ApplyWebhookResult(tt.result(cmd))

			assert.False(t, changed)
			assert.False(t, slackUpdate)
			assert.NotNil(t, issue.PendingPostClick)
			assert.Nil(t, issue.PostClick)
		})
	}

	t.Run("ignored: no pending state", func(t *testing.T) {
		t.Parallel()

		issue := newPostClickTestIssue()

		changed, slackUpdate := issue.ApplyWebhookResult(deliveredResult(newPostClickTestCommand("restart", clicked)))

		assert.False(t, changed)
		assert.False(t, slackUpdate)
	})
}

func TestIssue_ApplyWebhookResult_ReceiverAfterClick(t *testing.T) {
	t.Parallel()

	override := &types.WebhookAfterClick{
		HideButtons:       types.WebhookHideButtonsAll,
		StatusText:        "Restarting 3 pods <!channel>",
		ResetAfterSeconds: 120,
	}

	// newIssue returns an issue whose last alert was received well before clicks at time.Now().
	newIssue := func() *Issue {
		issue := newPostClickTestIssue()
		issue.LastAlertReceived = time.Now().UTC().Add(-time.Minute)

		return issue
	}

	t.Run("overrides the pending state settings", func(t *testing.T) {
		t.Parallel()

		issue := newIssue()
		cmd := newPostClickTestCommand("restart", time.Now().UTC())
		issue.RegisterWebhookClick(issue.FindWebhook("restart"), cmd)

		before := time.Now().UTC()
		changed, slackUpdate := issue.ApplyWebhookResult(deliveredResultWithAfterClick(cmd, override))

		assert.True(t, changed)
		assert.True(t, slackUpdate)
		assert.Nil(t, issue.PendingPostClick)
		require.NotNil(t, issue.PostClick)
		assert.Equal(t, "restart", issue.PostClick.WebhookID)
		assert.Equal(t, types.WebhookHideButtonsAll, issue.PostClick.AfterClick.HideButtons)
		assert.Equal(t, "Restarting 3 pods *channel*", issue.PostClick.AfterClick.StatusText)
		assert.Equal(t, "Restarting 3 pods <!channel>", override.StatusText, "the override must not be modified")
		assert.WithinRange(t, issue.PostClick.ExpiresAt, before.Add(2*time.Minute), time.Now().UTC().Add(2*time.Minute))
	})

	t.Run("applied without pending state", func(t *testing.T) {
		t.Parallel()

		issue := newIssue()
		cmd := newPostClickTestCommand("ack", time.Now().UTC())

		changed, slackUpdate := issue.ApplyWebhookResult(deliveredResultWithAfterClick(cmd, override))

		assert.True(t, changed)
		assert.True(t, slackUpdate)
		assert.True(t, issue.SlackPostNeedsUpdate)

		state := issue.PostClick
		require.NotNil(t, state)
		assert.Equal(t, "ack", state.WebhookID)
		assert.Equal(t, "Ack", state.ButtonText)
		assert.Equal(t, "U123", state.UserID)
		assert.Equal(t, "Jane", state.UserRealName)
		assert.Equal(t, cmd.Timestamp, state.Clicked)
		assert.Equal(t, "Restarting 3 pods *channel*", state.AfterClick.StatusText)
		assert.False(t, state.Expired)
		assert.True(t, issue.IsWebhookHidden("rollback"))
	})

	t.Run("replaces an older pending and active state", func(t *testing.T) {
		t.Parallel()

		issue := newIssue()
		now := time.Now().UTC()

		first := newPostClickTestCommand("restart", now.Add(-2*time.Second))
		issue.RegisterWebhookClick(issue.FindWebhook("restart"), first)
		issue.ApplyWebhookResult(deliveredResult(first))

		second := newPostClickTestCommand("restart", now.Add(-time.Second))
		issue.RegisterWebhookClick(issue.FindWebhook("restart"), second)

		cmd := newPostClickTestCommand("ack", now)
		changed, _ := issue.ApplyWebhookResult(deliveredResultWithAfterClick(cmd, override))

		assert.True(t, changed)
		assert.Nil(t, issue.PendingPostClick, "the older pending state is discarded")
		require.NotNil(t, issue.PostClick)
		assert.Equal(t, "ack", issue.PostClick.WebhookID)

		// The result of the older click is now ignored
		changed, _ = issue.ApplyWebhookResult(deliveredResult(second))
		assert.False(t, changed)
	})

	tests := []struct {
		name  string
		setup func(issue *Issue, clicked time.Time) *Command
	}{
		{name: "unknown webhook", setup: func(_ *Issue, clicked time.Time) *Command {
			return newPostClickTestCommand("unknown", clicked)
		}},
		{name: "newer pending click", setup: func(issue *Issue, clicked time.Time) *Command {
			issue.RegisterWebhookClick(issue.FindWebhook("restart"), newPostClickTestCommand("restart", clicked.Add(time.Second)))
			return newPostClickTestCommand("ack", clicked)
		}},
		{name: "same time pending click", setup: func(issue *Issue, clicked time.Time) *Command {
			issue.RegisterWebhookClick(issue.FindWebhook("restart"), newPostClickTestCommand("restart", clicked))
			return newPostClickTestCommand("ack", clicked)
		}},
		{name: "newer active click", setup: func(issue *Issue, clicked time.Time) *Command {
			newer := newPostClickTestCommand("restart", clicked.Add(time.Second))
			issue.RegisterWebhookClick(issue.FindWebhook("restart"), newer)
			issue.ApplyWebhookResult(deliveredResult(newer))
			issue.SlackPostNeedsUpdate = false
			return newPostClickTestCommand("ack", clicked)
		}},
		{name: "alert received after click", setup: func(issue *Issue, clicked time.Time) *Command {
			issue.LastAlertReceived = clicked.Add(time.Second)
			return newPostClickTestCommand("ack", clicked)
		}},
		{name: "alert received within clock skew margin", setup: func(issue *Issue, clicked time.Time) *Command {
			issue.LastAlertReceived = clicked.Add(-unregisteredClickAlertMargin)
			return newPostClickTestCommand("ack", clicked)
		}},
	}

	for _, tt := range tests {
		t.Run("ignored: "+tt.name, func(t *testing.T) {
			t.Parallel()

			issue := newIssue()
			cmd := tt.setup(issue, time.Now().UTC())
			pendingBefore := issue.PendingPostClick
			activeBefore := issue.PostClick

			changed, slackUpdate := issue.ApplyWebhookResult(deliveredResultWithAfterClick(cmd, override))

			assert.False(t, changed)
			assert.False(t, slackUpdate)
			assert.Same(t, pendingBefore, issue.PendingPostClick)
			assert.Same(t, activeBefore, issue.PostClick)
			assert.False(t, issue.SlackPostNeedsUpdate)
		})
	}

	t.Run("ignored: failed with after-click", func(t *testing.T) {
		t.Parallel()

		issue := newIssue()
		result := NewWebhookResultCommand(newPostClickTestCommand("ack", time.Now().UTC()), WebhookOutcomeFailed)
		result.WebhookResult.AfterClick = override

		changed, slackUpdate := issue.ApplyWebhookResult(result)

		assert.False(t, changed)
		assert.False(t, slackUpdate)
		assert.Nil(t, issue.PostClick)
	})
}

func TestIssue_ClearPostClickState(t *testing.T) {
	t.Parallel()

	clicked := time.Now().UTC()

	t.Run("nothing to clear", func(t *testing.T) {
		t.Parallel()

		issue := newPostClickTestIssue()

		changed, visible := issue.ClearPostClickState()

		assert.False(t, changed)
		assert.False(t, visible)
		assert.False(t, issue.SlackPostNeedsUpdate)
	})

	t.Run("pending only", func(t *testing.T) {
		t.Parallel()

		issue := newPostClickTestIssue()
		issue.RegisterWebhookClick(issue.FindWebhook("restart"), newPostClickTestCommand("restart", clicked))

		changed, visible := issue.ClearPostClickState()

		assert.True(t, changed)
		assert.False(t, visible)
		assert.Nil(t, issue.PendingPostClick)
		assert.False(t, issue.SlackPostNeedsUpdate)
	})

	t.Run("active and pending", func(t *testing.T) {
		t.Parallel()

		issue := newPostClickTestIssue()
		cmd := newPostClickTestCommand("restart", clicked)
		issue.RegisterWebhookClick(issue.FindWebhook("restart"), cmd)
		issue.ApplyWebhookResult(deliveredResult(cmd))
		issue.RegisterWebhookClick(issue.FindWebhook("restart"), newPostClickTestCommand("restart", clicked.Add(time.Second)))
		issue.SlackPostNeedsUpdate = false

		changed, visible := issue.ClearPostClickState()

		assert.True(t, changed)
		assert.True(t, visible)
		assert.Nil(t, issue.PendingPostClick)
		assert.Nil(t, issue.PostClick)
		assert.True(t, issue.SlackPostNeedsUpdate)
	})
}

func TestIssue_ExpirePostClickState(t *testing.T) {
	t.Parallel()

	clicked := time.Now().UTC()

	newActive := func(t *testing.T) *Issue {
		t.Helper()

		issue := newPostClickTestIssue()
		cmd := newPostClickTestCommand("restart", clicked)
		issue.RegisterWebhookClick(issue.FindWebhook("restart"), cmd)
		issue.ApplyWebhookResult(deliveredResult(cmd))
		issue.SlackPostNeedsUpdate = false

		return issue
	}

	t.Run("nothing to expire", func(t *testing.T) {
		t.Parallel()

		assert.False(t, newPostClickTestIssue().ExpirePostClickState(time.Now()))
	})

	t.Run("active state before expiry", func(t *testing.T) {
		t.Parallel()

		issue := newActive(t)

		assert.False(t, issue.ExpirePostClickState(issue.PostClick.ExpiresAt.Add(-time.Second)))
		assert.False(t, issue.PostClick.Expired)
		assert.False(t, issue.SlackPostNeedsUpdate)
	})

	t.Run("active state expires once", func(t *testing.T) {
		t.Parallel()

		issue := newActive(t)
		expiresAt := issue.PostClick.ExpiresAt

		assert.True(t, issue.ExpirePostClickState(expiresAt))
		assert.True(t, issue.PostClick.Expired)
		assert.True(t, issue.SlackPostNeedsUpdate)

		issue.SlackPostNeedsUpdate = false
		assert.False(t, issue.ExpirePostClickState(expiresAt.Add(time.Hour)))
		assert.False(t, issue.SlackPostNeedsUpdate)
	})

	t.Run("stale pending state is discarded silently", func(t *testing.T) {
		t.Parallel()

		issue := newPostClickTestIssue()
		issue.RegisterWebhookClick(issue.FindWebhook("restart"), newPostClickTestCommand("restart", clicked))

		// Reset delay (1 minute) plus grace (1 minute)
		assert.False(t, issue.ExpirePostClickState(clicked.Add(2*time.Minute)), "not stale yet")
		assert.NotNil(t, issue.PendingPostClick)

		assert.True(t, issue.ExpirePostClickState(clicked.Add(2*time.Minute+time.Second)))
		assert.Nil(t, issue.PendingPostClick)
		assert.False(t, issue.SlackPostNeedsUpdate)
	})
}

func TestIssue_IsWebhookHidden(t *testing.T) {
	t.Parallel()

	issue := newPostClickTestIssue()
	cmd := newPostClickTestCommand("restart", time.Now().UTC())

	// Nothing is hidden while pending
	issue.RegisterWebhookClick(issue.FindWebhook("restart"), cmd)
	assert.False(t, issue.IsWebhookHidden("restart"))

	issue.ApplyWebhookResult(deliveredResult(cmd))
	assert.True(t, issue.IsWebhookHidden("restart"))
	assert.True(t, issue.IsWebhookHidden("rollback"))
	assert.False(t, issue.IsWebhookHidden("ack"))

	// Buttons are shown again when expired
	issue.ExpirePostClickState(issue.PostClick.ExpiresAt)
	assert.False(t, issue.IsWebhookHidden("restart"))
	assert.False(t, issue.IsWebhookHidden("rollback"))
}

func TestIssue_PostClickStateJSON(t *testing.T) {
	t.Parallel()

	issue := newPostClickTestIssue()

	// No state is omitted
	body, err := json.Marshal(issue)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "postClick")
	assert.NotContains(t, string(body), "pendingPostClick")

	first := newPostClickTestCommand("restart", time.Now().UTC().Truncate(time.Second))
	issue.RegisterWebhookClick(issue.FindWebhook("restart"), first)
	issue.ApplyWebhookResult(deliveredResult(first))
	issue.RegisterWebhookClick(issue.FindWebhook("restart"), newPostClickTestCommand("restart", first.Timestamp.Add(time.Second)))

	body, err = json.Marshal(issue)
	require.NoError(t, err)

	decoded := &Issue{}
	require.NoError(t, json.Unmarshal(body, decoded))
	assert.Equal(t, issue.PostClick.WebhookID, decoded.PostClick.WebhookID)
	assert.True(t, issue.PostClick.Clicked.Equal(decoded.PostClick.Clicked))
	assert.True(t, issue.PostClick.ExpiresAt.Equal(decoded.PostClick.ExpiresAt))
	assert.Equal(t, issue.PostClick.AfterClick, decoded.PostClick.AfterClick)
	assert.True(t, issue.PendingPostClick.Clicked.Equal(decoded.PendingPostClick.Clicked))
}
