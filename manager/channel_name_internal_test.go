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

	name    atomic.Value // string
	lookups atomic.Int32

	// lookupHasDeadline records whether the last lookup context had a deadline.
	lookupHasDeadline atomic.Bool
}

func newNamingSlackClient(name string) *namingSlackClient {
	c := &namingSlackClient{}
	c.name.Store(name)
	return c
}

func (n *namingSlackClient) GetChannelName(ctx context.Context, _ string) string {
	n.lookups.Add(1)
	_, hasDeadline := ctx.Deadline()
	n.lookupHasDeadline.Store(hasDeadline)

	name, _ := n.name.Load().(string)

	return name
}

func TestNewChannelManager_ChannelName(t *testing.T) {
	t.Parallel()

	t.Run("known name", func(t *testing.T) {
		t.Parallel()

		slackClient := newNamingSlackClient("ignored")
		cm := newChannelManager("C1", "alerts", slackClient, nil, &NoopChannelLocker{}, &mockLogger{}, &types.NoopMetrics{}, nil,
			&config.ManagerConfig{SlackClient: config.NewDefaultSlackClientConfig()}, nil, &NoopRateLimitGate{})

		assert.Equal(t, "alerts", cm.channelName)
		assert.Equal(t, "alerts", cm.currentSlackChannelName(context.Background()))
		assert.Equal(t, int32(0), slackClient.lookups.Load(), "a known name is not looked up again before the refresh interval")
	})

	t.Run("unknown name", func(t *testing.T) {
		t.Parallel()

		slackClient := newNamingSlackClient("alerts")
		cm := newChannelManager("C1", "", slackClient, nil, &NoopChannelLocker{}, &mockLogger{}, &types.NoopMetrics{}, nil,
			&config.ManagerConfig{SlackClient: config.NewDefaultSlackClientConfig()}, nil, &NoopRateLimitGate{})

		assert.Equal(t, "C1", cm.channelName, "the channel ID is the metrics label")
		assert.Equal(t, "alerts", cm.currentSlackChannelName(context.Background()), "an unknown name is looked up on first use")
		assert.Equal(t, "C1", cm.channelName, "the metrics label never changes")
	})
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
