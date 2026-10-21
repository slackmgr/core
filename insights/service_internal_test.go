package insights

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeReader is a types.InsightsReader returning canned results and recording queries.
type fakeReader struct {
	countFn func(q *types.InsightsCountQuery) []*types.InsightsCountRow
	statsFn func(q *types.InsightsStatsQuery) []*types.InsightsStatsRow
	listFn  func(q *types.InsightsListQuery) *types.InsightsEventPage
	issues  map[string]json.RawMessage
	err     error

	countQueries []*types.InsightsCountQuery
	statsQueries []*types.InsightsStatsQuery
	loads        atomic.Int32
}

func (f *fakeReader) CountInsightsEvents(_ context.Context, q *types.InsightsCountQuery) ([]*types.InsightsCountRow, error) {
	f.countQueries = append(f.countQueries, q)
	if f.err != nil {
		return nil, f.err
	}

	if f.countFn == nil {
		return nil, nil
	}

	return f.countFn(q), nil
}

func (f *fakeReader) InsightsValueStats(_ context.Context, q *types.InsightsStatsQuery) ([]*types.InsightsStatsRow, error) {
	f.statsQueries = append(f.statsQueries, q)
	if f.err != nil {
		return nil, f.err
	}

	if f.statsFn == nil {
		return nil, nil
	}

	return f.statsFn(q), nil
}

func (f *fakeReader) ListInsightsEvents(_ context.Context, q *types.InsightsListQuery) (*types.InsightsEventPage, error) {
	if f.err != nil {
		return nil, f.err
	}

	if f.listFn == nil {
		return &types.InsightsEventPage{}, nil
	}

	return f.listFn(q), nil
}

func (f *fakeReader) LoadOpenIssues(_ context.Context, _ []string) (map[string]json.RawMessage, error) {
	f.loads.Add(1)

	if f.err != nil {
		return nil, f.err
	}

	return f.issues, nil
}

var (
	testFrom = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	testTo   = testFrom.Add(24 * time.Hour)
)

func testFilter() Filter {
	return Filter{From: testFrom, To: testTo, ChannelIDs: []string{"C1"}}
}

func row(t types.InsightsEventType, reason string, n int64) *types.InsightsCountRow {
	return &types.InsightsCountRow{InsightsGroupKey: types.InsightsGroupKey{Type: t, Reason: reason}, Count: n}
}

func TestService_NotSupported(t *testing.T) {
	t.Parallel()

	s := New(nil)
	ctx := context.Background()
	f := testFilter()

	assert.False(t, s.Supported())

	_, err := s.Summary(ctx, f)
	require.ErrorIs(t, err, types.ErrInsightsNotSupported)
	_, err = s.Series(ctx, SeriesQuery{Filter: f, Bucket: time.Hour, Metrics: []Metric{{Type: types.InsightsEventIssueCreated}}})
	require.ErrorIs(t, err, types.ErrInsightsNotSupported)
	_, err = s.OpenIssuesSeries(ctx, f, time.Hour)
	require.ErrorIs(t, err, types.ErrInsightsNotSupported)
	_, err = s.Breakdown(ctx, f, Metric{Type: types.InsightsEventIssueCreated}, types.InsightsDimensionRouteKey, 0)
	require.ErrorIs(t, err, types.ErrInsightsNotSupported)
	_, err = s.Lifecycle(ctx, f)
	require.ErrorIs(t, err, types.ErrInsightsNotSupported)
	_, err = s.Flapping(ctx, f, 2, 0)
	require.ErrorIs(t, err, types.ErrInsightsNotSupported)
	_, err = s.MoveFlows(ctx, f)
	require.ErrorIs(t, err, types.ErrInsightsNotSupported)
	_, err = s.IssueTimeline(ctx, TimelineQuery{IssueID: "i"})
	require.ErrorIs(t, err, types.ErrInsightsNotSupported)
	_, err = s.Events(ctx, &types.InsightsListQuery{})
	require.ErrorIs(t, err, types.ErrInsightsNotSupported)
	_, err = s.CurrentState(ctx, CurrentStateQuery{})
	require.ErrorIs(t, err, types.ErrInsightsNotSupported)
}

func TestService_Summary(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		countFn: func(q *types.InsightsCountQuery) []*types.InsightsCountRow {
			if q.Distinct == types.InsightsDimensionIssue {
				return []*types.InsightsCountRow{{Count: 3}}
			}

			return []*types.InsightsCountRow{
				row(types.InsightsEventAlertNewIssue, "", 5),
				row(types.InsightsEventAlertGrouped, "", 7),
				row(types.InsightsEventAlertIgnored, types.InsightsReasonNotNewer, 2),
				row(types.InsightsEventAlertDropped, types.InsightsReasonInvalid, 1),
				row(types.InsightsEventAlertRetried, types.InsightsReasonDBError, 4),
				row(types.InsightsEventAlertQueued, "", 20),
				row(types.InsightsEventAlertFiltered, types.InsightsReasonIgnoreTerm, 1),
				row(types.InsightsEventAlertRejected, "rate_limited", 6),
				row(types.InsightsEventIssueCreated, "", 5),
				row(types.InsightsEventIssueUpdated, "", 7),
				row(types.InsightsEventIssueResolved, types.InsightsReasonUser, 2),
				row(types.InsightsEventIssueReopened, types.InsightsReasonAlert, 1),
				row(types.InsightsEventIssueEscalated, "", 1),
				row(types.InsightsEventIssueMoved, types.InsightsReasonEscalation, 1),
				row(types.InsightsEventIssueArchived, types.InsightsReasonAutoResolved, 4),
				row(types.InsightsEventCommandExecuted, "resolve_issue", 2),
				row(types.InsightsEventCommandFailed, "resolve_issue", 1),
				row(types.InsightsEventCommandSkipped, "mute_issue", 1),
				row(types.InsightsEventWebhookSucceeded, "", 3),
				row(types.InsightsEventWebhookFailed, "", 1),
				row(types.InsightsEventSlackPostCreated, "alert", 5),
				row(types.InsightsEventSlackPostUpdated, "alert", 9),
				row(types.InsightsEventSlackPostDeleted, "", 2),
				row(types.InsightsEventSlackPostThrottled, "alert", 8),
				row(types.InsightsEventSlackPostFailed, types.InsightsReasonInvalidBlocks, 1),
				row(types.InsightsEventChannelStats, "", 100),
				row("future.type", "", 1),
			}
		},
		statsFn: func(_ *types.InsightsStatsQuery) []*types.InsightsStatsRow {
			return []*types.InsightsStatsRow{{Count: 2, Values: map[types.InsightsAggregate]float64{types.InsightsAggregateSum: 42}}}
		},
	}

	sum, err := New(reader).Summary(context.Background(), testFilter())
	require.NoError(t, err)

	assert.Equal(t, int64(14), sum.Alerts.Accepted)
	assert.Equal(t, int64(2), sum.Alerts.IgnoredByReason[types.InsightsReasonNotNewer])
	assert.Equal(t, int64(1), sum.Alerts.DroppedByReason[types.InsightsReasonInvalid])
	assert.Equal(t, int64(4), sum.Alerts.RetriedByReason[types.InsightsReasonDBError])
	assert.Equal(t, int64(20), sum.Alerts.Queued)
	assert.Equal(t, int64(1), sum.Alerts.FilteredByReason[types.InsightsReasonIgnoreTerm])
	assert.Equal(t, int64(6), sum.Alerts.RejectedByReason["rate_limited"])

	assert.Equal(t, int64(5), sum.Issues.Created)
	assert.Equal(t, int64(7), sum.Issues.Updates)
	assert.Equal(t, int64(3), sum.Issues.UpdatedIssues)
	assert.Equal(t, int64(2), sum.Issues.ResolvedByReason[types.InsightsReasonUser])
	assert.Equal(t, int64(1), sum.Issues.ReopenedByReason[types.InsightsReasonAlert])
	assert.Equal(t, int64(1), sum.Issues.Escalated)
	assert.Equal(t, int64(1), sum.Issues.MovedByReason[types.InsightsReasonEscalation])
	assert.Equal(t, int64(4), sum.Issues.ArchivedByReason[types.InsightsReasonAutoResolved])

	assert.Equal(t, CommandCounts{Executed: 2, Failed: 1}, sum.Commands["resolve_issue"])
	assert.Equal(t, CommandCounts{Skipped: 1}, sum.Commands["mute_issue"])
	assert.Equal(t, WebhookSummary{Succeeded: 3, Failed: 1}, sum.Webhooks)
	assert.Equal(t, int64(8), sum.SlackPosts.Throttled)
	assert.Equal(t, int64(1), sum.SlackPosts.FailedByReason[types.InsightsReasonInvalidBlocks])
	assert.Equal(t, int64(42), sum.LostEvents)

	// The gap query must not be restricted to the selected channels.
	require.Len(t, reader.statsQueries, 1)
	assert.Empty(t, reader.statsQueries[0].ChannelIDs)
	assert.Equal(t, []types.InsightsEventType{types.InsightsEventGap}, reader.statsQueries[0].Types)
}

func TestService_Series(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{countFn: func(_ *types.InsightsCountQuery) []*types.InsightsCountRow {
		return []*types.InsightsCountRow{
			{InsightsGroupKey: types.InsightsGroupKey{Bucket: testFrom, Type: types.InsightsEventAlertIgnored, Reason: types.InsightsReasonNotNewer, ChannelID: "C1"}, Count: 2},
			{InsightsGroupKey: types.InsightsGroupKey{Bucket: testFrom, Type: types.InsightsEventAlertIgnored, Reason: types.InsightsReasonAlreadyResolved, ChannelID: "C1"}, Count: 1},
			{InsightsGroupKey: types.InsightsGroupKey{Bucket: testFrom.Add(2 * time.Hour), Type: types.InsightsEventIssueCreated, ChannelID: "C2"}, Count: 5},
			{InsightsGroupKey: types.InsightsGroupKey{Bucket: testTo.Add(time.Hour), Type: types.InsightsEventIssueCreated}, Count: 9}, // outside range: ignored
		}
	}}

	s := New(reader)
	ctx := context.Background()

	_, err := s.Series(ctx, SeriesQuery{Filter: testFilter(), Bucket: time.Hour})
	require.Error(t, err, "metrics are required")

	series, err := s.Series(ctx, SeriesQuery{
		Filter: testFilter(),
		Bucket: time.Hour,
		Metrics: []Metric{
			{Type: types.InsightsEventAlertIgnored},
			{Type: types.InsightsEventAlertIgnored, Reason: types.InsightsReasonNotNewer},
			{Type: types.InsightsEventIssueCreated},
			{Type: types.InsightsEventIssueArchived},
		},
	})
	require.NoError(t, err)

	require.Len(t, series.Buckets, 24)
	assert.Equal(t, testFrom, series.Buckets[0])
	assert.InDelta(t, 3, series.Values["alert.ignored"][0], 0)
	assert.InDelta(t, 2, series.Values["alert.ignored/not_newer"][0], 0)
	assert.InDelta(t, 5, series.Values["issue.created"][2], 0)
	assert.Len(t, series.Values["issue.archived"], 24, "metrics without data are zero-filled")

	q := reader.countQueries[len(reader.countQueries)-1]
	assert.ElementsMatch(t, []types.InsightsEventType{types.InsightsEventAlertIgnored, types.InsightsEventIssueCreated, types.InsightsEventIssueArchived}, q.Types)

	series, err = s.Series(ctx, SeriesQuery{Filter: testFilter(), Bucket: time.Hour, ByChannel: true, Metrics: []Metric{{Type: types.InsightsEventIssueCreated}}})
	require.NoError(t, err)
	assert.InDelta(t, 5, series.Values["issue.created@C2"][2], 0)
	assert.Contains(t, reader.countQueries[len(reader.countQueries)-1].GroupBy, types.InsightsDimensionChannel)
}

func TestAlignBucket(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 10, 1, 10, 47, 13, 0, time.FixedZone("X", 3600))
	assert.Equal(t, time.Date(2026, 10, 1, 9, 45, 0, 0, time.UTC), alignBucket(ts, 15*time.Minute))
	assert.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), alignBucket(ts, 24*time.Hour))
	assert.Equal(t, ts.UTC(), alignBucket(ts, time.Millisecond))
}

func TestService_OpenIssuesSeries(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{statsFn: func(q *types.InsightsStatsQuery) []*types.InsightsStatsRow {
		assert.Equal(t, types.InsightsValueOpen, q.ValueKey)
		return []*types.InsightsStatsRow{
			{InsightsGroupKey: types.InsightsGroupKey{Bucket: testFrom.Add(time.Hour), ChannelID: "C1"}, Values: map[types.InsightsAggregate]float64{types.InsightsAggregateMax: 4}},
		}
	}}

	series, err := New(reader).OpenIssuesSeries(context.Background(), testFilter(), time.Hour)
	require.NoError(t, err)
	assert.InDelta(t, 4, series.Values["C1"][1], 0)
	assert.InDelta(t, 0, series.Values["C1"][0], 0)
}

func TestService_Breakdown(t *testing.T) {
	t.Parallel()

	dims := []types.InsightsDimension{
		types.InsightsDimensionChannel, types.InsightsDimensionRelatedChannel, types.InsightsDimensionType,
		types.InsightsDimensionReason, types.InsightsDimensionSeverity, types.InsightsDimensionRouteKey,
		types.InsightsDimensionAlertType, types.InsightsDimensionHost, types.InsightsDimensionAuthor,
		types.InsightsDimensionActor, types.InsightsDimensionCorrelationID, types.InsightsDimensionIssue,
	}

	key := types.InsightsGroupKey{
		ChannelID: "channel", RelatedChannelID: "related_channel", Type: "type", Reason: "reason", Severity: "severity",
		RouteKey: "route_key", AlertType: "alert_type", Host: "host", Author: "author", ActorID: "actor",
		CorrelationID: "correlation_id", IssueID: "issue",
	}

	reader := &fakeReader{countFn: func(_ *types.InsightsCountQuery) []*types.InsightsCountRow {
		return []*types.InsightsCountRow{{InsightsGroupKey: key, Count: 7}}
	}}
	s := New(reader)

	for _, d := range dims {
		counts, err := s.Breakdown(context.Background(), testFilter(), Metric{Type: types.InsightsEventAlertDropped, Reason: "x"}, d, 0)
		require.NoError(t, err)
		require.Len(t, counts, 1)
		assert.Equal(t, Count{Key: string(d), Count: 7}, counts[0])

		q := reader.countQueries[len(reader.countQueries)-1]
		assert.Equal(t, DefaultTopN, q.Limit)
		assert.True(t, q.OrderByCountDesc)
		assert.Equal(t, []string{"x"}, q.Reasons)
	}

	_, err := s.Breakdown(context.Background(), testFilter(), Metric{Type: types.InsightsEventAlertDropped}, types.InsightsDimensionBucket, 5)
	require.Error(t, err)
	assert.Empty(t, groupKeyValue(key, types.InsightsDimensionBucket))
}

func TestService_Lifecycle(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		countFn: func(_ *types.InsightsCountQuery) []*types.InsightsCountRow {
			return []*types.InsightsCountRow{
				row("", types.InsightsReasonAutoResolved, 6),
				row("", types.InsightsReasonUserResolved, 4),
			}
		},
		statsFn: func(q *types.InsightsStatsQuery) []*types.InsightsStatsRow {
			if q.ValueKey == types.InsightsValueNotified {
				return []*types.InsightsStatsRow{{Count: 10, Values: map[types.InsightsAggregate]float64{types.InsightsAggregateSum: 8}}}
			}

			if q.ValueKey == types.InsightsValueTimeToFirstInvestigationSeconds {
				return nil
			}

			return []*types.InsightsStatsRow{{Count: 10, Values: map[types.InsightsAggregate]float64{
				types.InsightsAggregateAvg: 1, types.InsightsAggregateP50: 2, types.InsightsAggregateP90: 3,
				types.InsightsAggregateP99: 4, types.InsightsAggregateMax: 5,
			}}}
		},
	}

	stats, err := New(reader).Lifecycle(context.Background(), testFilter())
	require.NoError(t, err)

	assert.Equal(t, int64(10), stats.Archived)
	assert.Equal(t, int64(6), stats.ByResolution[types.InsightsReasonAutoResolved])
	assert.Equal(t, ValueStats{Count: 10, Avg: 1, P50: 2, P90: 3, P99: 4, Max: 5}, stats.OpenSeconds)
	assert.Equal(t, ValueStats{Count: 10, Avg: 1, P50: 2, P90: 3, P99: 4, Max: 5}, stats.TimeToResolveSeconds)
	assert.Equal(t, ValueStats{}, stats.TimeToFirstInvestigationSeconds)
	assert.Equal(t, int64(8), stats.Notified)

	for _, q := range reader.statsQueries {
		assert.Equal(t, []types.InsightsEventType{types.InsightsEventIssueArchived}, q.Types)
	}
}

func TestService_Flapping(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{countFn: func(q *types.InsightsCountQuery) []*types.InsightsCountRow {
		assert.Equal(t, []types.InsightsEventType{types.InsightsEventIssueReopened}, q.Types)
		return []*types.InsightsCountRow{
			{InsightsGroupKey: types.InsightsGroupKey{IssueID: "a", ChannelID: "C1", CorrelationID: "ca"}, Count: 5},
			{InsightsGroupKey: types.InsightsGroupKey{IssueID: "b"}, Count: 2},
			{InsightsGroupKey: types.InsightsGroupKey{IssueID: "c"}, Count: 1},
		}
	}}

	result, err := New(reader).Flapping(context.Background(), testFilter(), 2, 3)
	require.NoError(t, err)
	assert.Equal(t, []IssueCount{{IssueID: "a", ChannelID: "C1", CorrelationID: "ca", Count: 5}, {IssueID: "b", Count: 2}}, result)

	result, err = New(reader).Flapping(context.Background(), testFilter(), 0, 0)
	require.NoError(t, err)
	assert.Len(t, result, 3, "minimum is at least one reopen")
}

func TestService_MoveFlows(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{countFn: func(q *types.InsightsCountQuery) []*types.InsightsCountRow {
		if len(q.RelatedChannelIDs) > 0 {
			// Incoming moves to C1, including the C1 -> C1-like duplicate between selected channels.
			return []*types.InsightsCountRow{
				{InsightsGroupKey: types.InsightsGroupKey{ChannelID: "C9", RelatedChannelID: "C1", Reason: types.InsightsReasonUser}, Count: 1},
			}
		}

		return []*types.InsightsCountRow{
			{InsightsGroupKey: types.InsightsGroupKey{ChannelID: "C1", RelatedChannelID: "C2", Reason: types.InsightsReasonEscalation}, Count: 3},
			{InsightsGroupKey: types.InsightsGroupKey{ChannelID: "C9", RelatedChannelID: "C1", Reason: types.InsightsReasonUser}, Count: 1},
		}
	}}

	flows, err := New(reader).MoveFlows(context.Background(), testFilter())
	require.NoError(t, err)
	assert.Equal(t, []MoveFlow{
		{SourceChannelID: "C1", TargetChannelID: "C2", Reason: types.InsightsReasonEscalation, Count: 3},
		{SourceChannelID: "C9", TargetChannelID: "C1", Reason: types.InsightsReasonUser, Count: 1},
	}, flows)
	assert.Len(t, reader.countQueries, 2)

	// Without channel filter, only one query is needed.
	reader.countQueries = nil
	_, err = New(reader).MoveFlows(context.Background(), Filter{From: testFrom, To: testTo})
	require.NoError(t, err)
	assert.Len(t, reader.countQueries, 1)
}

func TestService_IssueTimelineAndEvents(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

	var got *types.InsightsListQuery

	reader := &fakeReader{listFn: func(q *types.InsightsListQuery) *types.InsightsEventPage {
		got = q
		return &types.InsightsEventPage{Events: []*types.InsightsEvent{{Type: types.InsightsEventIssueCreated}}}
	}}
	s := New(reader, WithClock(func() time.Time { return now }))

	_, err := s.IssueTimeline(context.Background(), TimelineQuery{})
	require.Error(t, err)

	page, err := s.IssueTimeline(context.Background(), TimelineQuery{IssueID: "issue-1", Limit: 5, Cursor: "c"})
	require.NoError(t, err)
	assert.Len(t, page.Events, 1)
	assert.Equal(t, "issue-1", got.IssueID)
	assert.Equal(t, now.Add(time.Minute), got.To)
	assert.Equal(t, now.Add(time.Minute).Add(-DefaultTimelineRange), got.From)
	assert.Equal(t, 5, got.Limit)
	assert.Equal(t, "c", got.Cursor)
	assert.False(t, got.Descending)

	_, err = s.Events(context.Background(), &types.InsightsListQuery{})
	require.ErrorContains(t, err, "invalid query")
}

func TestService_ReaderErrorsPropagate(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	s := New(&fakeReader{err: boom})
	ctx := context.Background()
	f := testFilter()

	_, err := s.Summary(ctx, f)
	require.ErrorIs(t, err, boom)
	_, err = s.Series(ctx, SeriesQuery{Filter: f, Bucket: time.Hour, Metrics: []Metric{{Type: types.InsightsEventIssueCreated}}})
	require.ErrorIs(t, err, boom)
	_, err = s.OpenIssuesSeries(ctx, f, time.Hour)
	require.ErrorIs(t, err, boom)
	_, err = s.Lifecycle(ctx, f)
	require.ErrorIs(t, err, boom)
	_, err = s.Flapping(ctx, f, 1, 1)
	require.ErrorIs(t, err, boom)
	_, err = s.MoveFlows(ctx, f)
	require.ErrorIs(t, err, boom)
	_, err = s.Breakdown(ctx, f, Metric{Type: types.InsightsEventIssueCreated}, types.InsightsDimensionHost, 1)
	require.ErrorIs(t, err, boom)
	_, err = s.IssueTimeline(ctx, TimelineQuery{IssueID: "x"})
	require.ErrorIs(t, err, boom)
	_, err = s.CurrentState(ctx, CurrentStateQuery{})
	require.ErrorIs(t, err, boom)

	// Invalid queries are rejected before reaching the reader.
	_, err = s.Summary(ctx, Filter{})
	require.ErrorContains(t, err, "invalid query")
	_, err = s.OpenIssuesSeries(ctx, Filter{}, time.Hour)
	require.ErrorContains(t, err, "invalid query")
}

// --- current state ---

func issueJSON(t *testing.T, mutate func(i *models.Issue)) json.RawMessage {
	t.Helper()

	alert := &models.Alert{Alert: types.Alert{
		SlackChannelID:       "C1",
		CorrelationID:        "corr",
		Header:               "Header",
		Severity:             types.AlertError,
		RouteKey:             "rk",
		Timestamp:            testFrom,
		IssueFollowUpEnabled: true,
		AutoResolveSeconds:   3600,
	}, SlackChannelName: "alerts"}

	issue := models.NewIssue(alert, &types.NoopLogger{})
	issue.Created = testFrom
	issue.LastAlertReceived = testFrom
	issue.ResolveTime = testFrom.Add(100 * 24 * time.Hour)

	if mutate != nil {
		mutate(issue)
	}

	body, err := json.Marshal(issue)
	require.NoError(t, err)

	return body
}

func TestService_CurrentState(t *testing.T) {
	t.Parallel()

	now := testFrom.Add(48 * time.Hour)

	reader := &fakeReader{issues: map[string]json.RawMessage{
		"old": issueJSON(t, func(i *models.Issue) {
			i.ID = "old"
			i.Created = testFrom.Add(-time.Hour)
			i.AlertCount = 3
		}),
		"fresh": issueJSON(t, func(i *models.Issue) {
			i.ID = "fresh"
			i.LastAlert.Severity = types.AlertWarning
			i.LastAlertReceived = now.Add(-time.Minute)
			i.Created = now.Add(-time.Hour)
			i.AlertCount = 10
			i.IsEmojiMuted = true
			i.IsEmojiInvestigated = true
			i.InvestigatedByUser = "Jane"
			i.LastAlert.SlackChannelName = "alerts-renamed"
		}),
		"auto-resolved": issueJSON(t, func(i *models.Issue) {
			i.ID = "auto-resolved"
			i.ResolveTime = now.Add(-time.Hour)
			i.LastAlert.SlackChannelID = "C2"
			i.IsEscalated = true
			i.IsMoved = true
		}),
		"archived": issueJSON(t, func(i *models.Issue) {
			i.ID = "archived"
			i.Archived = true
		}),
	}}

	s := New(reader, WithClock(func() time.Time { return now }), WithCurrentStateCacheTTL(time.Minute))

	state, err := s.CurrentState(context.Background(), CurrentStateQuery{IncludeIssues: true, TopN: 1})
	require.NoError(t, err)

	assert.Equal(t, now, state.GeneratedAt)
	assert.Equal(t, 3, state.Totals.Open)
	assert.Equal(t, 2, state.Totals.Alerting)
	assert.Equal(t, 1, state.Totals.Resolved)
	assert.Equal(t, 1, state.Totals.Muted)
	assert.Equal(t, 1, state.Totals.Investigated)
	assert.Equal(t, 1, state.Totals.Escalated)
	assert.Equal(t, 1, state.Totals.Moved)
	assert.Equal(t, 1, state.Totals.Stale, "the old issue has had no alerts for 48h")
	assert.Equal(t, map[types.AlertSeverity]int{types.AlertError: 1, types.AlertWarning: 1}, state.Totals.BySeverity)

	require.Len(t, state.Channels, 2)
	assert.Equal(t, "C1", state.Channels[0].ChannelID, "channel with most alerting issues first")
	assert.Equal(t, 2, state.Channels[0].Alerting)
	assert.Contains(t, []string{"alerts", "alerts-renamed"}, state.Channels[0].ChannelName)
	assert.Equal(t, "C2", state.Channels[1].ChannelID)
	assert.Equal(t, 1, state.Channels[1].Resolved)

	require.Len(t, state.Oldest, 1)
	assert.Equal(t, "old", state.Oldest[0].ID)
	require.Len(t, state.Stale, 1)
	assert.Equal(t, "old", state.Stale[0].ID)
	require.Len(t, state.MostAlerts, 1)
	assert.Equal(t, "fresh", state.MostAlerts[0].ID)
	assert.Equal(t, "Jane", state.MostAlerts[0].InvestigatedBy)

	require.Len(t, state.Issues, 3)

	var resolved OpenIssue
	for _, i := range state.Issues {
		if i.ID == "auto-resolved" {
			resolved = i
		}
	}

	assert.True(t, resolved.Resolved)
	assert.Equal(t, types.InsightsReasonAutoResolved, resolved.ResolvedBy)

	// Cached: the reader is not called again within the TTL.
	_, err = s.CurrentState(context.Background(), CurrentStateQuery{IncludeIssues: true, TopN: 1})
	require.NoError(t, err)
	assert.Equal(t, int32(1), reader.loads.Load())

	// A different query is not served from the cache.
	_, err = s.CurrentState(context.Background(), CurrentStateQuery{ChannelIDs: []string{"C1"}})
	require.NoError(t, err)
	assert.Equal(t, int32(2), reader.loads.Load())
}

func TestService_CurrentState_CacheExpiry(t *testing.T) {
	t.Parallel()

	now := testFrom
	reader := &fakeReader{issues: map[string]json.RawMessage{}}
	s := New(reader, WithClock(func() time.Time { return now }), WithCurrentStateCacheTTL(time.Second))

	_, err := s.CurrentState(context.Background(), CurrentStateQuery{})
	require.NoError(t, err)

	now = now.Add(2 * time.Second)

	state, err := s.CurrentState(context.Background(), CurrentStateQuery{})
	require.NoError(t, err)
	assert.Equal(t, int32(2), reader.loads.Load())
	assert.Empty(t, state.Channels)
	assert.NotNil(t, state.Oldest)

	// Caching disabled.
	s = New(reader, WithCurrentStateCacheTTL(0))
	_, _ = s.CurrentState(context.Background(), CurrentStateQuery{})
	_, _ = s.CurrentState(context.Background(), CurrentStateQuery{})
	assert.Equal(t, int32(4), reader.loads.Load())
}

func TestService_CurrentState_DecodeError(t *testing.T) {
	t.Parallel()

	s := New(&fakeReader{issues: map[string]json.RawMessage{"bad": json.RawMessage(`{"id":"bad"}`)}})
	_, err := s.CurrentState(context.Background(), CurrentStateQuery{})
	require.ErrorContains(t, err, "no last alert")

	s = New(&fakeReader{issues: map[string]json.RawMessage{"bad": json.RawMessage(`{`)}})
	_, err = s.CurrentState(context.Background(), CurrentStateQuery{})
	require.Error(t, err)
}

// stubDB is a types.DB with open issues per channel, for the current state fallback.
type stubDB struct {
	types.InMemoryDB

	channels []string
	issues   map[string]map[string]json.RawMessage
	err      error
}

func (d *stubDB) FindActiveChannels(_ context.Context) ([]string, error) {
	return d.channels, d.err
}

func (d *stubDB) LoadOpenIssuesInChannel(_ context.Context, channelID string) (map[string]json.RawMessage, error) {
	return d.issues[channelID], d.err
}

func TestService_CurrentState_DBFallback(t *testing.T) {
	t.Parallel()

	db := &stubDB{
		channels: []string{"C1", "C2"},
		issues: map[string]map[string]json.RawMessage{
			"C1": {"a": issueJSON(t, func(i *models.Issue) { i.ID = "a" })},
			"C2": {"b": issueJSON(t, func(i *models.Issue) { i.ID = "b"; i.LastAlert.SlackChannelID = "C2" })},
		},
	}

	s := NewFromDB(db, WithClock(func() time.Time { return testFrom.Add(time.Hour) }))
	assert.False(t, s.Supported(), "the stub DB has no insights reader")

	state, err := s.CurrentState(context.Background(), CurrentStateQuery{})
	require.NoError(t, err)
	assert.Equal(t, 2, state.Totals.Open)

	state, err = s.CurrentState(context.Background(), CurrentStateQuery{ChannelIDs: []string{"C2"}})
	require.NoError(t, err)
	assert.Equal(t, 1, state.Totals.Open)

	db.err = errors.New("boom")
	_, err = New(nil, WithDB(db), WithCurrentStateCacheTTL(0)).CurrentState(context.Background(), CurrentStateQuery{})
	require.ErrorIs(t, err, db.err)
	_, err = New(nil, WithDB(db), WithCurrentStateCacheTTL(0)).CurrentState(context.Background(), CurrentStateQuery{ChannelIDs: []string{"C1"}})
	require.ErrorIs(t, err, db.err)
}

// providerDB is a types.DB that provides an insights reader.
type providerDB struct {
	types.InMemoryDB

	reader types.InsightsReader
}

func (d *providerDB) InsightsReader() types.InsightsReader { //nolint:ireturn // Mandated by types.InsightsReaderProvider
	return d.reader
}

func TestNewFromDB_UsesProvidedReader(t *testing.T) {
	t.Parallel()

	s := NewFromDB(&providerDB{reader: &fakeReader{}})
	assert.True(t, s.Supported())

	s = NewFromDB(&providerDB{})
	assert.False(t, s.Supported(), "a nil reader from the provider means not supported")
}

func TestMetric_String(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "issue.created", Metric{Type: types.InsightsEventIssueCreated}.String())
	assert.Equal(t, "alert.ignored/not_newer", Metric{Type: types.InsightsEventAlertIgnored, Reason: types.InsightsReasonNotNewer}.String())
}
