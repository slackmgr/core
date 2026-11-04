package manager

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// namingSlackClient returns a configurable channel name and counts the lookups.
type namingSlackClient struct {
	updateRecordingSlackClient

	name     atomic.Value // string
	lookups  atomic.Int32
	returned atomic.Int32

	// lookupHasDeadline records whether the last lookup context had a deadline.
	lookupHasDeadline atomic.Bool

	// failFirst is the number of initial lookups that fail (return an empty name).
	failFirst atomic.Int32

	// block, if not nil, blocks lookups until it is closed or the lookup context is done.
	block chan struct{}
}

func newNamingSlackClient(name string) *namingSlackClient {
	c := &namingSlackClient{}
	c.name.Store(name)
	return c
}

func (n *namingSlackClient) GetChannelName(ctx context.Context, _ string) string {
	n.lookups.Add(1)
	defer n.returned.Add(1)
	_, hasDeadline := ctx.Deadline()
	n.lookupHasDeadline.Store(hasDeadline)

	if n.block != nil {
		select {
		case <-n.block:
		case <-ctx.Done():
			return ""
		}
	}

	if n.failFirst.Add(-1) >= 0 {
		return ""
	}

	name, _ := n.name.Load().(string)

	return name
}

// perChannelCounters are the counters labelled by channel.
var perChannelCounters = []string{
	alertsTotal,
	processActiveIssuesSkippedMetric,
	issuesArchivedTotalMetric,
	issuesEscalatedTotalMetric,
	issuesCreatedTotalMetric,
	channelLockAcquireFailuresTotalMetric,
	alertsGroupedTotalMetric,
	alertsIgnoredTotalMetric,
	alertsResolvedWithoutIssueTotalMetric,
}

func newNamingChannelManager(t *testing.T, db types.DB, slackClient SlackClient, metrics types.Metrics) *channelManager {
	t.Helper()

	cm := newTestChannelManager(t, insightsTestChannel, db, slackClient, &NoopChannelLocker{})
	cm.metrics = metrics

	return cm
}

func TestChannelManager_InitChannelName(t *testing.T) {
	t.Parallel()

	t.Run("resolved name is the label", func(t *testing.T) {
		t.Parallel()

		slackClient := newNamingSlackClient("alerts")
		metrics := newRecordingMetrics()
		cm := newNamingChannelManager(t, nil, slackClient, metrics)

		assert.Equal(t, insightsTestChannel, cm.channelName, "the channel ID is the label until the name is resolved")

		cm.initChannelName(context.Background())

		assert.Equal(t, "alerts", cm.channelName)
		assert.False(t, cm.channelNameIsFallback)
		assert.True(t, slackClient.lookupHasDeadline.Load(), "the lookup is bounded")

		for _, name := range perChannelCounters {
			assert.True(t, metrics.hasCounter(name, "alerts"), "%s is pre-warmed with the name", name)
			assert.False(t, metrics.hasCounter(name, insightsTestChannel), "%s has no series under the channel ID", name)
		}

		_, ok := metrics.openIssuesGauge(insightsTestChannel)
		assert.False(t, ok, "no open issues series under the channel ID")

		assert.Equal(t, "alerts", cm.currentSlackChannelName(context.Background()))
		assert.Equal(t, int32(1), slackClient.lookups.Load(), "a resolved name is not looked up again before the refresh interval")
	})

	t.Run("unknown name falls back to the channel ID", func(t *testing.T) {
		t.Parallel()

		slackClient := newNamingSlackClient("")
		metrics := newRecordingMetrics()
		cm := newNamingChannelManager(t, nil, slackClient, metrics)

		cm.initChannelName(context.Background())

		assert.Equal(t, insightsTestChannel, cm.channelName)
		assert.True(t, cm.channelNameIsFallback)
		assert.True(t, metrics.hasCounter(alertsTotal, insightsTestChannel), "series are pre-warmed with the channel ID")
		assert.True(t, cm.channelNameNextRefresh.IsZero(), "an unknown name is looked up again on first use")

		slackClient.name.Store("alerts")

		assert.Equal(t, "alerts", cm.currentSlackChannelName(context.Background()), "the first use looks up the name")
		assert.Equal(t, "alerts", cm.channelName, "the label switches at the first successful lookup")
		assert.Equal(t, int32(2), slackClient.lookups.Load())
	})
}

func TestChannelManager_SwitchMetricsLabel(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	body := existingIssueJSON(t, types.AlertError, now, nil)

	slackClient := newNamingSlackClient("")
	metrics := newRecordingMetrics()
	cm := newNamingChannelManager(t, &insightsStubDB{openIssues: map[string]json.RawMessage{"i1": body}}, slackClient, metrics)

	cm.initChannelName(context.Background())

	// The name lookup fails (e.g. conversations.info is backing off): metrics use the channel ID.
	_, err := cm.processActiveIssues(context.Background(), time.Second)
	require.NoError(t, err)

	open, _ := metrics.openIssuesGauge(insightsTestChannel)
	assert.InDelta(t, 1, open, 0)

	// The next refresh resolves the name.
	slackClient.name.Store("alerts")
	cm.channelNameNextRefresh = time.Now().Add(-time.Second)

	_, err = cm.processActiveIssues(context.Background(), time.Second)
	require.NoError(t, err)

	assert.Equal(t, "alerts", cm.channelName)
	assert.False(t, cm.channelNameIsFallback)

	open, ok := metrics.openIssuesGauge(insightsTestChannel)
	require.True(t, ok)
	assert.InDelta(t, 0, open, 0, "the open issues gauge under the channel ID is not left at its last value")

	open, ok = metrics.openIssuesGauge("alerts")
	require.True(t, ok)
	assert.InDelta(t, 1, open, 0, "the open issues gauge continues under the name")

	for _, name := range perChannelCounters {
		assert.True(t, metrics.hasCounter(name, "alerts"), "%s is pre-warmed with the name", name)
	}

	// A later rename does not change the label.
	slackClient.name.Store("renamed")
	cm.channelNameNextRefresh = time.Now().Add(-time.Second)

	assert.Equal(t, "renamed", cm.currentSlackChannelName(context.Background()))
	assert.Equal(t, "alerts", cm.channelName, "the label is not changed by a rename")
}

func TestChannelManager_SwitchMetricsLabel_CarriesOpenIssues(t *testing.T) {
	t.Parallel()

	metrics := newRecordingMetrics()
	cm := newNamingChannelManager(t, nil, newNamingSlackClient(""), metrics)
	cm.openIssues = 3

	cm.switchMetricsLabel("alerts")

	open, _ := metrics.openIssuesGauge(insightsTestChannel)
	assert.InDelta(t, 0, open, 0)

	open, _ = metrics.openIssuesGauge("alerts")
	assert.InDelta(t, 3, open, 0, "the last open issues value is carried over before the next processing run")
}

func TestChannelManager_ProcessAlert_SwitchesLabelFirst(t *testing.T) {
	t.Parallel()

	slackClient := newNamingSlackClient("alerts")
	metrics := newRecordingMetrics()
	cm := newNamingChannelManager(t, &insightsStubDB{}, slackClient, metrics)

	alert, ackCalled, _ := newTestAlertFromData(t, validInsightsAlert(types.AlertError, time.Now()))
	require.NoError(t, cm.processAlert(context.Background(), alert))
	assert.True(t, *ackCalled)

	assert.Equal(t, "alerts", alert.SlackChannelName)
	assert.InDelta(t, 1, metrics.counter(alertsTotal, "alerts"), 0, "the alert that resolves the name is counted under the name")
	assert.InDelta(t, 1, metrics.counter(issuesCreatedTotalMetric, "alerts"), 0)
	assert.InDelta(t, 0, metrics.counter(alertsTotal, insightsTestChannel), 0)
}

// TestChannelManager_ChannelName_NoRace runs a channel manager whose name is resolved while alerts are processed and
// metrics are read concurrently. Run with -race: the label must only be accessed by the channel manager goroutine.
func TestChannelManager_ChannelName_NoRace(t *testing.T) {
	t.Parallel()

	// The lookup when the channel manager starts fails, the lookup at first use (the first alert) succeeds.
	slackClient := newNamingSlackClient("alerts")
	slackClient.failFirst.Store(1)
	metrics := newRecordingMetrics()
	cm := newNamingChannelManager(t, &insightsStubDB{}, slackClient, metrics)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)
		cm.run(ctx)
	}()

	acked := make(chan struct{}, 20)

	for range 20 {
		body, err := json.Marshal(validInsightsAlert(types.AlertError, time.Now()))
		require.NoError(t, err)

		msg, err := models.NewAlertFromQueueItem(&types.FifoQueueItem{
			Body: string(body),
			Ack:  func() { acked <- struct{}{} },
			Nack: func() { acked <- struct{}{} },
		})
		require.NoError(t, err)

		alert, ok := msg.(*models.Alert)
		require.True(t, ok)
		require.NoError(t, cm.queueAlert(ctx, alert))

		_ = metrics.counter(alertsTotal, "alerts")
	}

	for range 20 {
		select {
		case <-acked:
		case <-time.After(2 * time.Second):
			t.Fatal("alert not processed")
		}
	}

	cancel()
	<-done

	assert.Equal(t, "alerts", cm.channelName)
	assert.InDelta(t, 20, metrics.counter(alertsTotal, "alerts"), 0)
	assert.InDelta(t, 0, metrics.counter(alertsTotal, insightsTestChannel), 0)

	open, _ := metrics.openIssuesGauge(insightsTestChannel)
	assert.InDelta(t, 0, open, 0)
}

func TestChannelManager_CurrentSlackChannelName(t *testing.T) {
	t.Parallel()

	t.Run("looked up at most once per refresh interval", func(t *testing.T) {
		t.Parallel()

		slackClient := newNamingSlackClient("alerts")
		cm := newTestChannelManager(t, "C1", nil, slackClient, &NoopChannelLocker{})

		for range 5 {
			assert.Equal(t, "alerts", cm.currentSlackChannelName(context.Background()))
		}

		assert.Equal(t, int32(1), slackClient.lookups.Load())
		assert.True(t, slackClient.lookupHasDeadline.Load(), "lookups are bounded")

		nextRefresh := time.Until(cm.channelNameNextRefresh)
		assert.Greater(t, nextRefresh, 4*time.Minute)
		assert.LessOrEqual(t, nextRefresh, 5*time.Minute)
	})

	t.Run("refreshed after the interval", func(t *testing.T) {
		t.Parallel()

		slackClient := newNamingSlackClient("alerts")
		cm := newTestChannelManager(t, "C1", nil, slackClient, &NoopChannelLocker{})

		assert.Equal(t, "alerts", cm.currentSlackChannelName(context.Background()))

		slackClient.name.Store("renamed")
		cm.channelNameNextRefresh = time.Now().Add(-time.Second)

		assert.Equal(t, "renamed", cm.currentSlackChannelName(context.Background()))
		assert.Equal(t, int32(2), slackClient.lookups.Load())
	})

	t.Run("last known name is kept when a lookup fails", func(t *testing.T) {
		t.Parallel()

		slackClient := newNamingSlackClient("alerts")
		cm := newTestChannelManager(t, "C1", nil, slackClient, &NoopChannelLocker{})

		assert.Equal(t, "alerts", cm.currentSlackChannelName(context.Background()))

		slackClient.name.Store("")
		cm.channelNameNextRefresh = time.Now().Add(-time.Second)

		assert.Equal(t, "alerts", cm.currentSlackChannelName(context.Background()))

		nextRefresh := time.Until(cm.channelNameNextRefresh)
		assert.LessOrEqual(t, nextRefresh, channelNameRetryInterval, "a failed lookup is retried sooner")
		assert.Greater(t, nextRefresh, channelNameRetryInterval-10*time.Second)

		assert.Equal(t, "alerts", cm.currentSlackChannelName(context.Background()))
		assert.Equal(t, int32(2), slackClient.lookups.Load(), "a failed lookup is not retried immediately")
	})

	t.Run("never resolved", func(t *testing.T) {
		t.Parallel()

		slackClient := newNamingSlackClient("")
		cm := newTestChannelManager(t, "C1", nil, slackClient, &NoopChannelLocker{})

		assert.Empty(t, cm.currentSlackChannelName(context.Background()))
	})

	t.Run("refresh interval follows the channel info cache TTL", func(t *testing.T) {
		t.Parallel()

		slackCfg := config.NewDefaultSlackClientConfig()
		slackCfg.ChannelInfoCacheTTLSeconds = 30

		assert.Equal(t, 30*time.Second, channelNameRefreshInterval(&config.ManagerConfig{SlackClient: slackCfg}))
		assert.Equal(t, 5*time.Minute, channelNameRefreshInterval(&config.ManagerConfig{}))
		assert.Equal(t, 5*time.Minute, channelNameRefreshInterval(nil))

		slackClient := newNamingSlackClient("")
		cm := newTestChannelManager(t, "C1", nil, slackClient, &NoopChannelLocker{})
		cm.cfg = &config.ManagerConfig{SlackClient: slackCfg}

		cm.currentSlackChannelName(context.Background())
		assert.LessOrEqual(t, time.Until(cm.channelNameNextRefresh), 30*time.Second, "retry interval is capped by the refresh interval")
	})
}

func TestChannelManager_ProcessActiveIssues_ChannelName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		lookupName string
		wantName   string
	}{
		{name: "renamed channel updates issues", lookupName: "renamed", wantName: "renamed"},
		{name: "failed lookup keeps the issue's name", lookupName: "", wantName: "original"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			now := time.Now().UTC()

			body := existingIssueJSON(t, types.AlertError, now, func(issue *models.Issue) {
				issue.LastAlert.SlackChannelName = "original"
			})

			slackClient := newNamingSlackClient(tc.lookupName)
			cm := newTestChannelManager(t, insightsTestChannel, &insightsStubDB{openIssues: map[string]json.RawMessage{"i1": body}}, slackClient, &NoopChannelLocker{})

			for range 3 {
				_, err := cm.processActiveIssues(context.Background(), time.Second)
				require.NoError(t, err)
			}

			require.NotEmpty(t, slackClient.updated)
			assert.Equal(t, tc.wantName, slackClient.updated[len(slackClient.updated)-1].LastAlert.SlackChannelName)
			assert.Equal(t, int32(1), slackClient.lookups.Load(), "the name is not looked up every cycle")
		})
	}
}

func TestChannelManager_ChannelStats_ChannelName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		lookups    []string
		wantDetail []string
	}{
		{name: "resolved name", lookups: []string{"alerts"}, wantDetail: []string{"alerts"}},
		{name: "never resolved", lookups: []string{""}, wantDetail: []string{insightsTestChannel}},
		{name: "resolved after the channel ID fallback", lookups: []string{"", "alerts"}, wantDetail: []string{insightsTestChannel, "alerts"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body := existingIssueJSON(t, types.AlertError, time.Now().UTC(), nil)
			slackClient := newNamingSlackClient("")
			recorder, sink := newTestRecorder(t)

			cm := newNamingChannelManager(t, &insightsStubDB{openIssues: map[string]json.RawMessage{"i1": body}}, slackClient, &types.NoopMetrics{})
			cm.insights = recorder

			for _, name := range tt.lookups {
				slackClient.name.Store(name)
				cm.channelNameNextRefresh = time.Time{}

				// The stub DB does not persist the processing state, so every run is the first run and records stats.
				_, err := cm.processActiveIssues(context.Background(), time.Second)
				require.NoError(t, err)
			}

			flush(recorder)

			stats := sink.ofType(types.InsightsEventChannelStats)
			require.Len(t, stats, len(tt.wantDetail))

			for i, want := range tt.wantDetail {
				assert.Equal(t, want, stats[i].Detail)
			}
		})
	}
}
