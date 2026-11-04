package manager

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gaugeRecordingMetrics is a thread-safe types.Metrics implementation that records gauge values.
type gaugeRecordingMetrics struct {
	types.NoopMetrics

	mu     sync.Mutex
	gauges map[string]float64
}

func newGaugeRecordingMetrics() *gaugeRecordingMetrics {
	return &gaugeRecordingMetrics{gauges: make(map[string]float64)}
}

func (g *gaugeRecordingMetrics) GaugeSet(name string, value float64, _ ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.gauges[name] = value
}

func (g *gaugeRecordingMetrics) GaugeAdd(name string, value float64, _ ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.gauges[name] += value
}

func (g *gaugeRecordingMetrics) gauge(name string) float64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.gauges[name]
}

// activeChannelsDB is a stubDB returning a fixed list of active channels, or an error.
type activeChannelsDB struct {
	stubDB

	channels []string
	err      error
}

func (d *activeChannelsDB) FindActiveChannels(_ context.Context) ([]string, error) {
	return d.channels, d.err
}

func newTestCoordinator(t *testing.T, db types.DB, slackClient SlackClient, metrics types.Metrics) *coordinator {
	t.Helper()

	cfg := &config.ManagerConfig{
		SlackClient:                  config.NewDefaultSlackClientConfig(),
		ChannelManagerDrainTimeoutMs: 100,
	}

	settings := models.NewManagerSettingsWrapper(&config.ManagerSettings{
		DefaultAlertSeverity:              types.AlertError,
		DefaultPostUsername:               "testbot",
		DefaultPostIconEmoji:              ":robot:",
		DefaultIssueArchivingDelaySeconds: 600,
	})

	return newCoordinator(db, &stubFifoQueue{name: "alerts"}, slackClient, nil, &NoopChannelLocker{}, &mockLogger{},
		metrics, nil, &NoopRateLimitGate{}, cfg, settings)
}

// stopChannelManagers cancels the context and waits for all channel manager goroutines to exit.
func stopChannelManagers(t *testing.T, cancel context.CancelFunc, c *coordinator) {
	t.Helper()

	cancel()

	done := make(chan struct{})

	go func() {
		c.channelManagersWaitGroup.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("channel managers did not exit after context cancellation")
	}
}

func TestCoordinator_CreateChannelManager(t *testing.T) {
	t.Parallel()

	slackClient := newNamingSlackClient("alerts")
	metrics := newGaugeRecordingMetrics()
	c := newTestCoordinator(t, &stubDB{}, slackClient, metrics)

	recorder, _ := newTestRecorder(t)
	commandQueue := &stubFifoQueue{name: "commands"}
	c.insights = recorder
	c.commandQueue = commandQueue
	c.channelStatsInterval = 42 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cm, err := c.createChannelManager(ctx, "C1")
	require.NoError(t, err)
	require.NotNil(t, cm)

	assert.Equal(t, "C1", cm.channelID)
	assert.Equal(t, "alerts", cm.slackChannelName, "the looked up name is passed to the channel manager")
	assert.Equal(t, int32(1), slackClient.lookups.Load())
	assert.True(t, slackClient.lookupHasDeadline.Load(), "the name lookup is bounded")

	assert.Same(t, recorder, cm.insights)
	assert.Same(t, commandQueue, cm.commandQueue)
	assert.Equal(t, 42*time.Second, cm.channelStatsInterval)

	assert.Same(t, cm, c.channelManagers["C1"])
	assert.InDelta(t, 1, metrics.gauge(activeChannelManagersMetric), 0)

	// The channel manager goroutine is running, and exits when the context is cancelled.
	stopChannelManagers(t, cancel, c)
}

func TestCoordinator_CreateChannelManager_UnknownName(t *testing.T) {
	t.Parallel()

	slackClient := newNamingSlackClient("")
	c := newTestCoordinator(t, &stubDB{}, slackClient, &types.NoopMetrics{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cm, err := c.createChannelManager(ctx, "C1")
	require.NoError(t, err)

	assert.Empty(t, cm.slackChannelName)
	assert.Equal(t, "C1", cm.channelName, "the channel ID is the metrics label when the name is unknown")

	stopChannelManagers(t, cancel, c)
}

func TestCoordinator_FindOrCreateChannelManager(t *testing.T) {
	t.Parallel()

	slackClient := newNamingSlackClient("alerts")
	metrics := newGaugeRecordingMetrics()
	c := newTestCoordinator(t, &stubDB{}, slackClient, metrics)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first, err := c.findOrCreateChannelManager(ctx, "C1")
	require.NoError(t, err)

	second, err := c.findOrCreateChannelManager(ctx, "C1")
	require.NoError(t, err)

	other, err := c.findOrCreateChannelManager(ctx, "C2")
	require.NoError(t, err)

	assert.Same(t, first, second, "an existing channel manager is reused")
	assert.NotSame(t, first, other)
	assert.Len(t, c.channelManagers, 2)
	assert.Equal(t, int32(2), slackClient.lookups.Load(), "the name is looked up once per new channel manager")
	assert.InDelta(t, 2, metrics.gauge(activeChannelManagersMetric), 0)

	stopChannelManagers(t, cancel, c)
}

func TestCoordinator_RefreshChannelManagers(t *testing.T) {
	t.Parallel()

	t.Run("creates missing and keeps existing", func(t *testing.T) {
		t.Parallel()

		db := &activeChannelsDB{channels: []string{"C1", "C2"}}
		metrics := newGaugeRecordingMetrics()
		c := newTestCoordinator(t, db, newNamingSlackClient("alerts"), metrics)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		existing, err := c.createChannelManager(ctx, "C1")
		require.NoError(t, err)

		require.NoError(t, c.refreshChannelManagers(ctx))

		assert.Len(t, c.channelManagers, 2)
		assert.Same(t, existing, c.channelManagers["C1"], "an existing channel manager is not replaced")
		assert.NotNil(t, c.channelManagers["C2"])
		assert.InDelta(t, 2, metrics.gauge(activeChannelManagersMetric), 0)

		stopChannelManagers(t, cancel, c)
	})

	t.Run("db error", func(t *testing.T) {
		t.Parallel()

		db := &activeChannelsDB{err: errors.New("boom")}
		c := newTestCoordinator(t, db, newNamingSlackClient("alerts"), &types.NoopMetrics{})

		err := c.refreshChannelManagers(context.Background())
		require.ErrorContains(t, err, "boom")
		assert.Empty(t, c.channelManagers)
	})
}
