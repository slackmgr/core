package models //nolint:testpackage // Internal test to access unexported fields

import (
	"testing"
	"time"

	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests do not run in parallel, because other tests in this package override the global nowFunc.

func TestAddAlertWithReason(t *testing.T) {
	logger := &mockLogger{}

	t.Run("not newer", func(t *testing.T) {
		issue := NewIssue(createTestAlert(), logger)
		older := createTestAlert()
		older.Timestamp = issue.LastAlert.Timestamp.Add(-time.Second)

		updated, reason := issue.AddAlertWithReason(older, logger)
		assert.False(t, updated)
		assert.Equal(t, types.InsightsReasonNotNewer, reason)
	})

	t.Run("already resolved", func(t *testing.T) {
		first := createTestAlert()
		first.Severity = types.AlertResolved
		issue := NewIssue(first, logger)

		next := createTestAlert()
		next.Severity = types.AlertResolved
		next.Timestamp = first.Timestamp.Add(time.Second)

		updated, reason := issue.AddAlertWithReason(next, logger)
		assert.False(t, updated)
		assert.Equal(t, types.InsightsReasonAlreadyResolved, reason)
	})

	t.Run("follow-up disabled", func(t *testing.T) {
		first := createTestAlert()
		first.IssueFollowUpEnabled = false
		issue := NewIssue(first, logger)

		next := createTestAlert()
		next.Timestamp = first.Timestamp.Add(time.Second)

		updated, reason := issue.AddAlertWithReason(next, logger)
		assert.False(t, updated)
		assert.Equal(t, types.InsightsReasonFollowUpDisabled, reason)
	})

	t.Run("reopen increments reopen count", func(t *testing.T) {
		first := createTestAlert()
		first.Severity = types.AlertResolved
		issue := NewIssue(first, logger)
		require.True(t, issue.IsResolved())

		next := createTestAlert()
		next.Timestamp = first.Timestamp.Add(time.Second)

		updated, reason := issue.AddAlertWithReason(next, logger)
		assert.True(t, updated)
		assert.Empty(t, reason)
		assert.Equal(t, 1, issue.ReopenCount)

		// Updating an unresolved issue does not count as a reopen.
		again := createTestAlert()
		again.Timestamp = next.Timestamp.Add(time.Second)
		assert.True(t, issue.AddAlert(again, logger))
		assert.Equal(t, 1, issue.ReopenCount)
	})
}

func TestIssue_UnresolveAndInvestigateTracking(t *testing.T) {
	logger := &mockLogger{}
	issue := NewIssue(createTestAlert(), logger)

	issue.RegisterUnresolveRequest()
	assert.Equal(t, 0, issue.ReopenCount, "unresolving an unresolved issue is not a reopen")

	now := time.Now()
	restore := setTestTime(now)
	defer restore()

	issue.RegisterResolveRequest("Jane")

	// Resolution takes effect after the resolve time.
	setTestTime(now.Add(time.Second))
	issue.RegisterUnresolveRequest()
	assert.Equal(t, 1, issue.ReopenCount)

	issue.RegisterInvestigateRequest("Jane")
	first := issue.FirstInvestigatedAt
	assert.False(t, first.IsZero())

	issue.RegisterUninvestigateRequest()
	issue.RegisterInvestigateRequest("John")
	assert.Equal(t, first, issue.FirstInvestigatedAt, "first investigation time is never reset")
}

func TestApplyEscalationRules_Changed(t *testing.T) {
	logger := &mockLogger{}
	alert := createTestAlert()
	alert.Severity = types.AlertWarning
	alert.Escalation = []*types.Escalation{{Severity: types.AlertError, DelaySeconds: 0}}
	issue := NewIssue(alert, logger)

	result := issue.ApplyEscalationRules()
	assert.True(t, result.Escalated)
	assert.True(t, result.Changed, "first activation")
	assert.Equal(t, types.AlertWarning, result.PrevSeverity)

	result = issue.ApplyEscalationRules()
	assert.True(t, result.Escalated, "rule is still active")
	assert.False(t, result.Changed, "no change on later attempts")
}

func TestAlert_InsightsEvent(t *testing.T) {
	alert := createTestAlert()
	alert.RouteKey = "rk"
	alert.Host = "h"
	alert.Author = "a"
	alert.Type = "t"

	e := alert.InsightsEvent(types.InsightsEventAlertGrouped, "r", "C12345")
	assert.Equal(t, types.InsightsEvent{
		Type: types.InsightsEventAlertGrouped, Reason: "r", ChannelID: "C12345", CorrelationID: alert.CorrelationID,
		Severity: types.AlertError, RouteKey: "rk", AlertType: "t", Host: "h", Author: "a",
	}, e)

	e = alert.InsightsEvent(types.InsightsEventAlertGrouped, "", "C_MOVED")
	assert.Equal(t, "C12345", e.RelatedChannelID, "original channel when redirected by a move mapping")
}

func TestIssue_Resolution(t *testing.T) {
	logger := &mockLogger{}

	tests := []struct {
		name   string
		mutate func(issue *Issue)
		want   string
	}{
		{"terminated", func(i *Issue) { i.IsEmojiTerminated = true }, types.InsightsReasonTerminated},
		{"fire and forget", func(i *Issue) { i.LastAlert.IssueFollowUpEnabled = false }, types.InsightsReasonFireAndForget},
		{"user resolved", func(i *Issue) { i.IsEmojiResolved = true }, types.InsightsReasonUserResolved},
		{"alert resolved", func(i *Issue) { i.LastAlert.Severity = types.AlertResolved }, types.InsightsReasonAlertResolved},
		{"inconclusive", func(i *Issue) { i.LastAlert.AutoResolveAsInconclusive = true }, types.InsightsReasonAutoResolvedInconclusive},
		{"auto resolved", func(_ *Issue) {}, types.InsightsReasonAutoResolved},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := NewIssue(createTestAlert(), logger)
			tt.mutate(issue)
			assert.Equal(t, tt.want, issue.Resolution())
		})
	}
}

func TestIssue_ArchivedInsightsEvent(t *testing.T) {
	logger := &mockLogger{}
	issue := NewIssue(createTestAlert(), logger)
	created := issue.Created

	issue.FirstInvestigatedAt = created.Add(10 * time.Minute)
	issue.ResolveTime = created.Add(30 * time.Minute)
	issue.IsEmojiResolved = true
	issue.SlackAlertSentAtLeastOnce = true
	issue.ReopenCount = 2

	archivedAt := created.Add(time.Hour)
	e := issue.ArchivedInsightsEvent(archivedAt)

	assert.Equal(t, types.InsightsEventIssueArchived, e.Type)
	assert.Equal(t, types.InsightsReasonUserResolved, e.Reason)
	assert.Equal(t, archivedAt, e.Time)
	assert.Equal(t, issue.ID, e.IssueID)
	assert.InDelta(t, 3600, e.Values[types.InsightsValueOpenSeconds], 0.001)
	assert.InDelta(t, 1800, e.Values[types.InsightsValueTimeToResolveSeconds], 0.001)
	assert.InDelta(t, 600, e.Values[types.InsightsValueTimeToFirstInvestigationSeconds], 0.001)
	assert.InDelta(t, 1, e.Values[types.InsightsValueAlertCount], 0)
	assert.InDelta(t, 2, e.Values[types.InsightsValueReopenCount], 0)
	assert.InDelta(t, 1, e.Values[types.InsightsValueNotified], 0)

	// Terminated issues have no time to resolve; uninvestigated issues no time to first investigation.
	issue.IsEmojiTerminated = true
	issue.FirstInvestigatedAt = time.Time{}
	issue.SlackAlertSentAtLeastOnce = false

	e = issue.ArchivedInsightsEvent(archivedAt)
	assert.NotContains(t, e.Values, types.InsightsValueTimeToResolveSeconds)
	assert.NotContains(t, e.Values, types.InsightsValueTimeToFirstInvestigationSeconds)
	assert.InDelta(t, 0, e.Values[types.InsightsValueNotified], 0)
}

func TestIssue_ResolveTakesEffectImmediately(t *testing.T) {
	restore := setTestTime(time.Now())
	defer restore()

	issue := NewIssue(createTestAlert(), &mockLogger{})
	require.False(t, issue.IsResolved())

	issue.RegisterResolveRequest("Jane")
	assert.True(t, issue.IsResolved(), "resolved at the resolve time, without waiting for the clock to advance")
}
