package manager

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/slackmgr/core/insights"
	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const insightsTestChannel = "C777777"

// insightsStubDB is a types.DB stub with configurable issue lookups.
type insightsStubDB struct {
	stubDB

	openIssue   json.RawMessage
	postIssue   json.RawMessage
	openIssues  map[string]json.RawMessage
	saveIssueFn func() error
}

func (d *insightsStubDB) FindOpenIssueByCorrelationID(_ context.Context, _, _ string) (string, json.RawMessage, error) {
	return "", d.openIssue, nil
}

func (d *insightsStubDB) FindIssueBySlackPostID(_ context.Context, _, _ string) (string, json.RawMessage, error) {
	return "", d.postIssue, nil
}

func (d *insightsStubDB) LoadOpenIssuesInChannel(_ context.Context, _ string) (map[string]json.RawMessage, error) {
	return d.openIssues, nil
}

func (d *insightsStubDB) SaveIssue(_ context.Context, _ types.Issue) error {
	if d.saveIssueFn != nil {
		return d.saveIssueFn()
	}

	return nil
}

func validInsightsAlert(severity types.AlertSeverity, ts time.Time) types.Alert {
	return types.Alert{
		SlackChannelID:       insightsTestChannel,
		Header:               "Disk full",
		Severity:             severity,
		CorrelationID:        "corr-1",
		RouteKey:             "rk",
		Host:                 "host-1",
		Timestamp:            ts,
		IssueFollowUpEnabled: true,
		AutoResolveSeconds:   3600,
	}
}

// existingIssueJSON returns the JSON of an issue created from an alert with the given severity and timestamp.
func existingIssueJSON(t *testing.T, severity types.AlertSeverity, ts time.Time, mutate func(issue *models.Issue)) json.RawMessage {
	t.Helper()

	data := validInsightsAlert(severity, ts)
	alert := &models.Alert{Alert: data}
	alert.SlackChannelName = insightsTestChannel

	issue := models.NewIssue(alert, &types.NoopLogger{})
	issue.SlackPostID = "1234.5678"

	if mutate != nil {
		mutate(issue)
	}

	body, err := json.Marshal(issue)
	require.NoError(t, err)

	return body
}

func newInsightsChannelManager(t *testing.T, db types.DB, locker ChannelLocker, recorder *insights.Recorder) *channelManager {
	t.Helper()

	cm := newTestChannelManager(t, insightsTestChannel, db, &stubSlackClient{}, locker)
	cm.insights = recorder

	return cm
}

func TestChannelManager_Insights_AlertOutcomes(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	tests := []struct {
		name        string
		db          *insightsStubDB
		locker      ChannelLocker
		alert       types.Alert
		wantOutcome types.InsightsEventType
		wantReason  string
		wantIssue   []types.InsightsEventType
	}{
		{
			name:        "new issue",
			db:          &insightsStubDB{},
			alert:       validInsightsAlert(types.AlertError, now),
			wantOutcome: types.InsightsEventAlertNewIssue,
			wantIssue:   []types.InsightsEventType{types.InsightsEventIssueCreated},
		},
		{
			name:        "grouped into existing issue",
			db:          &insightsStubDB{openIssue: existingIssueJSON(t, types.AlertWarning, now.Add(-time.Minute), nil)},
			alert:       validInsightsAlert(types.AlertError, now),
			wantOutcome: types.InsightsEventAlertGrouped,
			wantIssue:   []types.InsightsEventType{types.InsightsEventIssueUpdated},
		},
		{
			name:        "resolving alert",
			db:          &insightsStubDB{openIssue: existingIssueJSON(t, types.AlertError, now.Add(-time.Minute), nil)},
			alert:       validInsightsAlert(types.AlertResolved, now),
			wantOutcome: types.InsightsEventAlertGrouped,
			wantIssue:   []types.InsightsEventType{types.InsightsEventIssueUpdated, types.InsightsEventIssueResolved},
		},
		{
			name:        "reopening alert",
			db:          &insightsStubDB{openIssue: existingIssueJSON(t, types.AlertResolved, now.Add(-time.Minute), nil)},
			alert:       validInsightsAlert(types.AlertError, now),
			wantOutcome: types.InsightsEventAlertGrouped,
			wantIssue:   []types.InsightsEventType{types.InsightsEventIssueUpdated, types.InsightsEventIssueReopened},
		},
		{
			name:        "ignored older alert",
			db:          &insightsStubDB{openIssue: existingIssueJSON(t, types.AlertError, now, nil)},
			alert:       validInsightsAlert(types.AlertError, now.Add(-time.Minute)),
			wantOutcome: types.InsightsEventAlertIgnored,
			wantReason:  types.InsightsReasonNotNewer,
		},
		{
			name:        "resolved alert without issue",
			db:          &insightsStubDB{},
			alert:       validInsightsAlert(types.AlertResolved, now),
			wantOutcome: types.InsightsEventAlertIgnored,
			wantReason:  types.InsightsReasonResolvedWithoutIssue,
		},
		{
			name:        "invalid alert",
			db:          &insightsStubDB{},
			alert:       types.Alert{SlackChannelID: insightsTestChannel, CorrelationID: "c"},
			wantOutcome: types.InsightsEventAlertDropped,
			wantReason:  types.InsightsReasonInvalid,
		},
		{
			name:        "corrupt issue",
			db:          &insightsStubDB{openIssue: json.RawMessage(`{"id":"x"}`)},
			alert:       validInsightsAlert(types.AlertError, now),
			wantOutcome: types.InsightsEventAlertDropped,
			wantReason:  types.InsightsReasonCorruptIssue,
		},
		{
			name:        "lock timeout",
			db:          &insightsStubDB{},
			locker:      &alwaysFailLocker{},
			alert:       validInsightsAlert(types.AlertError, now),
			wantOutcome: types.InsightsEventAlertRetried,
			wantReason:  types.InsightsReasonLockTimeout,
		},
		{
			name:        "db error",
			db:          &insightsStubDB{saveIssueFn: func() error { return errors.New("db down") }},
			alert:       validInsightsAlert(types.AlertError, now),
			wantOutcome: types.InsightsEventAlertRetried,
			wantReason:  types.InsightsReasonDBError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			locker := tt.locker
			if locker == nil {
				locker = &NoopChannelLocker{}
			}

			recorder, sink := newTestRecorder(t)
			cm := newInsightsChannelManager(t, tt.db, locker, recorder)

			alert, _, _ := newTestAlertFromData(t, tt.alert)
			_ = cm.processAlert(context.Background(), alert)

			flush(recorder)

			outcomes := sink.ofType(tt.wantOutcome)
			require.Len(t, outcomes, 1, "exactly one outcome event")
			assert.Equal(t, tt.wantReason, outcomes[0].Reason)
			assert.Equal(t, insightsTestChannel, outcomes[0].ChannelID)
			assert.Equal(t, tt.alert.CorrelationID, outcomes[0].CorrelationID)
			assert.False(t, outcomes[0].Time.IsZero())

			for _, issueType := range tt.wantIssue {
				events := sink.ofType(issueType)
				require.Len(t, events, 1, "expected one %s event", issueType)
				assert.NotEmpty(t, events[0].IssueID)
				assert.Equal(t, "rk", events[0].RouteKey)
			}
		})
	}
}

func TestChannelManager_Insights_UpdatedIssueCarriesSeverityChange(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	recorder, sink := newTestRecorder(t)
	db := &insightsStubDB{openIssue: existingIssueJSON(t, types.AlertWarning, now.Add(-time.Minute), nil)}
	cm := newInsightsChannelManager(t, db, &NoopChannelLocker{}, recorder)

	alert, _, _ := newTestAlertFromData(t, validInsightsAlert(types.AlertError, now))
	require.NoError(t, cm.processAlert(context.Background(), alert))

	flush(recorder)

	updated := sink.ofType(types.InsightsEventIssueUpdated)
	require.Len(t, updated, 1)
	assert.Equal(t, types.AlertWarning, updated[0].PrevSeverity)
	assert.Equal(t, types.AlertError, updated[0].Severity)
	assert.Equal(t, 2, updated[0].Count, "alert count after grouping")
}

func TestChannelManager_Insights_Commands(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	t.Run("resolve command records executed and resolved", func(t *testing.T) {
		t.Parallel()

		recorder, sink := newTestRecorder(t)
		db := &insightsStubDB{postIssue: existingIssueJSON(t, types.AlertError, now, nil)}
		cm := newInsightsChannelManager(t, db, &NoopChannelLocker{}, recorder)

		cmd := models.NewCommand(insightsTestChannel, "1234.5678", "", "U1", "Jane", models.CommandActionResolveIssue, nil)
		require.NoError(t, cm.processCmd(context.Background(), cmd))

		flush(recorder)

		executed := sink.ofType(types.InsightsEventCommandExecuted)
		require.Len(t, executed, 1)
		assert.Equal(t, string(models.CommandActionResolveIssue), executed[0].Reason)
		assert.Equal(t, "U1", executed[0].ActorID)
		assert.Equal(t, "Jane", executed[0].ActorName)
		assert.NotEmpty(t, executed[0].IssueID)

		resolved := sink.ofType(types.InsightsEventIssueResolved)
		require.Len(t, resolved, 1)
		assert.Equal(t, types.InsightsReasonUser, resolved[0].Reason)
		assert.Equal(t, "U1", resolved[0].ActorID)
	})

	t.Run("command without issue is skipped", func(t *testing.T) {
		t.Parallel()

		recorder, sink := newTestRecorder(t)
		cm := newInsightsChannelManager(t, &insightsStubDB{}, &NoopChannelLocker{}, recorder)

		cmd := models.NewCommand(insightsTestChannel, "1234.5678", "", "U1", "Jane", models.CommandActionMuteIssue, nil)
		require.NoError(t, cm.processCmd(context.Background(), cmd))

		flush(recorder)

		skipped := sink.ofType(types.InsightsEventCommandSkipped)
		require.Len(t, skipped, 1)
		assert.Equal(t, string(models.CommandActionMuteIssue), skipped[0].Reason)
		assert.Empty(t, skipped[0].IssueID)
	})

	t.Run("failing command is recorded as failed", func(t *testing.T) {
		t.Parallel()

		recorder, sink := newTestRecorder(t)
		db := &insightsStubDB{postIssue: existingIssueJSON(t, types.AlertError, now, nil), saveIssueFn: func() error { return errors.New("db down") }}
		cm := newInsightsChannelManager(t, db, &NoopChannelLocker{}, recorder)

		cmd := models.NewCommand(insightsTestChannel, "1234.5678", "", "U1", "Jane", models.CommandActionInvestigateIssue, nil)
		require.Error(t, cm.processCmd(context.Background(), cmd))

		flush(recorder)

		require.Len(t, sink.ofType(types.InsightsEventCommandFailed), 1)
		assert.Empty(t, sink.ofType(types.InsightsEventCommandExecuted))
	})
}

func TestChannelManager_Insights_ProcessActiveIssues(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	archivable := existingIssueJSON(t, types.AlertError, now.Add(-2*time.Hour), func(issue *models.Issue) {
		issue.ID = "archivable"
		issue.CorrelationID = "corr-archivable"
		issue.IsEmojiTerminated = true
		issue.ArchiveTime = now.Add(-time.Minute)
		issue.Created = now.Add(-2 * time.Hour)
	})
	open := existingIssueJSON(t, types.AlertError, now, func(issue *models.Issue) {
		issue.ID = "open"
		issue.CorrelationID = "corr-open"
		issue.IsEmojiMuted = true
	})

	recorder, sink := newTestRecorder(t)
	db := &insightsStubDB{openIssues: map[string]json.RawMessage{"archivable": archivable, "open": open}}
	cm := newInsightsChannelManager(t, db, &NoopChannelLocker{}, recorder)

	_, err := cm.processActiveIssues(context.Background(), time.Second)
	require.NoError(t, err)

	flush(recorder)

	archived := sink.ofType(types.InsightsEventIssueArchived)
	require.Len(t, archived, 1)
	assert.Equal(t, "archivable", archived[0].IssueID)
	assert.Equal(t, types.InsightsReasonTerminated, archived[0].Reason)
	assert.InDelta(t, 7200, archived[0].Values[types.InsightsValueOpenSeconds], 5)

	stats := sink.ofType(types.InsightsEventChannelStats)
	require.Len(t, stats, 1)
	assert.Equal(t, 1, stats[0].Count)
	assert.InDelta(t, 1.0, stats[0].Values[types.InsightsValueOpen], 0)
	assert.InDelta(t, 1.0, stats[0].Values[types.InsightsValueMuted], 0)
	assert.InDelta(t, 1.0, stats[0].Values[types.InsightsValueAlerting], 0)
	assert.Equal(t, insightsTestChannel, stats[0].Detail, "channel name")
}

func TestChannelManager_RecordChannelStats_Interval(t *testing.T) {
	t.Parallel()

	recorder, sink := newTestRecorder(t)
	cm := newInsightsChannelManager(t, &insightsStubDB{}, &NoopChannelLocker{}, recorder)
	cm.channelStatsInterval = time.Hour

	alert := &models.Alert{Alert: validInsightsAlert(types.AlertError, time.Now())}
	issue := models.NewIssue(alert, &types.NoopLogger{})

	now := time.Now()
	cm.recordChannelStats(now, []*models.Issue{issue}, "name")
	cm.recordChannelStats(now.Add(time.Minute), []*models.Issue{issue}, "name") // within interval: skipped
	cm.recordChannelStats(now.Add(2*time.Minute), nil, "name")                  // became empty: recorded
	cm.recordChannelStats(now.Add(3*time.Minute), nil, "name")                  // still empty: skipped

	flush(recorder)

	stats := sink.ofType(types.InsightsEventChannelStats)
	require.Len(t, stats, 2)
	assert.Equal(t, 1, stats[0].Count)
	assert.Equal(t, 0, stats[1].Count)
}

// TestChannelManager_Insights_Isolation proves that a misbehaving insights sink or a full buffer never affects
// alert processing: every alert is processed and acked exactly as without a recorder, in comparable time.
func TestChannelManager_Insights_Isolation(t *testing.T) {
	t.Parallel()

	blockForever := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	tests := []struct {
		name     string
		recorder func(t *testing.T) *insights.Recorder
	}{
		{
			name: "slow sink",
			recorder: func(_ *testing.T) *insights.Recorder {
				return insights.NewRecorder(&memorySink{fn: blockForever}, nil, nil, insights.WithBufferSize(10), insights.WithBatchSize(1), insights.WithWriteTimeout(time.Hour))
			},
		},
		{
			name: "failing sink",
			recorder: func(_ *testing.T) *insights.Recorder {
				return insights.NewRecorder(&memorySink{fn: func(context.Context) error { return errors.New("boom") }}, nil, nil, insights.WithBatchSize(1), insights.WithFlushInterval(time.Millisecond))
			},
		},
		{
			name: "panicking sink",
			recorder: func(_ *testing.T) *insights.Recorder {
				return insights.NewRecorder(&memorySink{fn: func(context.Context) error { panic("boom") }}, nil, nil, insights.WithBatchSize(1), insights.WithFlushInterval(time.Millisecond))
			},
		},
		{
			name: "full buffer",
			recorder: func(_ *testing.T) *insights.Recorder {
				// Never started, so the single-slot buffer stays full.
				return insights.NewRecorder(&memorySink{}, nil, nil, insights.WithBufferSize(1))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder := tt.recorder(t)
			if tt.name != "full buffer" {
				recorder.Start()
			}

			t.Cleanup(func() { recorder.Shutdown(50 * time.Millisecond) })

			db := &insightsStubDB{}
			cm := newInsightsChannelManager(t, db, &NoopChannelLocker{}, recorder)

			started := time.Now()
			base := time.Now().UTC()

			for i := range 50 {
				data := validInsightsAlert(types.AlertError, base.Add(time.Duration(i)*time.Millisecond))
				data.CorrelationID = "corr-" + string(rune('a'+i%26)) + string(rune('a'+i/26))

				alert, ackCalled, nackCalled := newTestAlertFromData(t, data)
				require.NoError(t, cm.processAlert(context.Background(), alert))
				assert.True(t, *ackCalled, "alert %d must be acked", i)
				assert.False(t, *nackCalled, "alert %d must not be nacked", i)
			}

			assert.Less(t, time.Since(started), time.Second, "processing must not be slowed down by insights")
		})
	}
}
