package manager

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fireAndForgetIssueJSON returns the JSON of a fire-and-forget issue (follow-up disabled) with the given ID.
func fireAndForgetIssueJSON(t *testing.T, id string, severity types.AlertSeverity, ts time.Time, mutate func(issue *models.Issue)) json.RawMessage {
	t.Helper()

	return existingIssueJSON(t, severity, ts, func(issue *models.Issue) {
		issue.ID = id
		issue.CorrelationID = "corr-" + id
		issue.LastAlert.IssueFollowUpEnabled = false

		if mutate != nil {
			mutate(issue)
		}
	})
}

func TestChannelManager_FireAndForget_NewAlert(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	fireAndForgetError := validInsightsAlert(types.AlertError, now)
	fireAndForgetError.IssueFollowUpEnabled = false

	tests := []struct {
		name  string
		alert types.Alert
	}{
		{"error alert without follow-up", fireAndForgetError},
		{"info alert with follow-up requested", validInsightsAlert(types.AlertInfo, now)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder, sink := newTestRecorder(t)
			metrics := newRecordingMetrics()
			cm := newNamingChannelManager(t, &insightsStubDB{}, newNamingSlackClient("alerts"), metrics)
			cm.insights = recorder
			cm.initChannelName(context.Background())

			alert, acked, _ := newTestAlertFromData(t, tt.alert)
			require.NoError(t, cm.processAlert(context.Background(), alert))

			flush(recorder)

			assert.True(t, *acked)

			outcomes := sink.ofType(types.InsightsEventAlertFireAndForget)
			require.Len(t, outcomes, 1)
			assert.Equal(t, insightsTestChannel, outcomes[0].ChannelID)
			assert.Equal(t, tt.alert.CorrelationID, outcomes[0].CorrelationID)

			assert.Empty(t, sink.ofType(types.InsightsEventAlertNewIssue), "fire-and-forget is not a new issue")
			assert.Empty(t, sink.ofType(types.InsightsEventIssueCreated), "fire-and-forget is not an issue")

			assert.InDelta(t, 1, metrics.counter(fireAndForgetAlertsTotalMetric, "alerts"), 0)
			assert.InDelta(t, 0, metrics.counter(issuesCreatedTotalMetric, "alerts"), 0)
		})
	}
}

func TestChannelManager_FireAndForget_RepeatedAlertIgnored(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	recorder, sink := newTestRecorder(t)
	db := &insightsStubDB{openIssue: fireAndForgetIssueJSON(t, "ff", types.AlertError, now.Add(-time.Second), nil)}
	cm := newInsightsChannelManager(t, db, &NoopChannelLocker{}, recorder)

	data := validInsightsAlert(types.AlertError, now)
	data.CorrelationID = "corr-ff"
	data.IssueFollowUpEnabled = false

	alert, _, _ := newTestAlertFromData(t, data)
	require.NoError(t, cm.processAlert(context.Background(), alert))

	flush(recorder)

	ignored := sink.ofType(types.InsightsEventAlertIgnored)
	require.Len(t, ignored, 1)
	assert.Equal(t, types.InsightsReasonFollowUpDisabled, ignored[0].Reason)
	assert.Empty(t, sink.ofType(types.InsightsEventIssueUpdated))
	assert.Empty(t, sink.ofType(types.InsightsEventAlertFireAndForget))
}

func TestChannelManager_FireAndForget_ProcessActiveIssues(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	issues := map[string]json.RawMessage{
		"open": existingIssueJSON(t, types.AlertError, now, func(issue *models.Issue) {
			issue.ID = "open"
			issue.CorrelationID = "corr-open"
		}),
		"archivable": existingIssueJSON(t, types.AlertError, now.Add(-2*time.Hour), func(issue *models.Issue) {
			issue.ID = "archivable"
			issue.CorrelationID = "corr-archivable"
			issue.IsEmojiTerminated = true
			issue.ArchiveTime = now.Add(-time.Minute)
		}),
		"ff-open":       fireAndForgetIssueJSON(t, "ff-open", types.AlertError, now, nil),
		"ff-info-open":  fireAndForgetIssueJSON(t, "ff-info-open", types.AlertInfo, now, nil),
		"ff-archivable": fireAndForgetIssueJSON(t, "ff-archivable", types.AlertError, now.Add(-time.Hour), func(issue *models.Issue) { issue.ArchiveTime = now.Add(-time.Minute) }),
	}

	recorder, sink := newTestRecorder(t)
	metrics := newRecordingMetrics()
	db := &processingStateDB{insightsStubDB: insightsStubDB{openIssues: issues}}
	cm := newNamingChannelManager(t, db, newNamingSlackClient("alerts"), metrics)
	cm.insights = recorder
	cm.initChannelName(context.Background())

	remaining, err := cm.processActiveIssues(context.Background(), time.Second)
	require.NoError(t, err)

	flush(recorder)

	assert.Equal(t, len(issues), remaining, "fire-and-forget issues keep the processing loop running")

	state, err := db.FindChannelProcessingState(context.Background(), insightsTestChannel)
	require.NoError(t, err)
	assert.Equal(t, 2, state.OpenIssues, "persisted open issues exclude fire-and-forget issues")

	archived := sink.ofType(types.InsightsEventIssueArchived)
	require.Len(t, archived, 1, "fire-and-forget issues have no archived event")
	assert.Equal(t, "archivable", archived[0].IssueID)
	assert.InDelta(t, 1, metrics.counter(issuesArchivedTotalMetric, "alerts"), 0)

	gauge, ok := metrics.openIssuesGauge("alerts")
	require.True(t, ok)
	assert.InDelta(t, 1, gauge, 0, "open issues gauge excludes fire-and-forget issues")

	stats := sink.ofType(types.InsightsEventChannelStats)
	require.Len(t, stats, 1)
	assert.Equal(t, 1, stats[0].Count)
	assert.InDelta(t, 1, stats[0].Values[types.InsightsValueOpen], 0)
	assert.InDelta(t, 1, stats[0].Values[types.InsightsValueAlerting], 0)
	assert.InDelta(t, 0, stats[0].Values[types.InsightsValueResolved], 0)
}

func TestChannelManager_FireAndForget_OnlyFireAndForgetIssues(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	recorder, sink := newTestRecorder(t)
	metrics := newRecordingMetrics()
	db := &processingStateDB{insightsStubDB: insightsStubDB{openIssues: map[string]json.RawMessage{
		"ff": fireAndForgetIssueJSON(t, "ff", types.AlertError, now, nil),
	}}}
	cm := newNamingChannelManager(t, db, newNamingSlackClient("alerts"), metrics)
	cm.insights = recorder
	cm.initChannelName(context.Background())

	remaining, err := cm.processActiveIssues(context.Background(), time.Second)
	require.NoError(t, err)

	flush(recorder)

	assert.Equal(t, 1, remaining, "the processing loop keeps running until the issue is archived")

	state, err := db.FindChannelProcessingState(context.Background(), insightsTestChannel)
	require.NoError(t, err)
	assert.Equal(t, 0, state.OpenIssues)

	gauge, ok := metrics.openIssuesGauge("alerts")
	require.True(t, ok)
	assert.InDelta(t, 0, gauge, 0)

	assert.Empty(t, sink.ofType(types.InsightsEventChannelStats), "a channel without open issues has no snapshot")
}
