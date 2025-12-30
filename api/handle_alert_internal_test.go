package api

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReduceAlerts(t *testing.T) {
	t.Parallel()

	limit := 2

	alerts := []*common.Alert{
		{SlackChannelID: "123", Header: "a"},
		{SlackChannelID: "123", Header: "b"},
	}

	// Two alerts is within the limit -> no alerts should be skipped
	keptAlerts, skippedAlerts := reduceAlertCountForChannel("123", alerts, limit)
	assert.Len(t, keptAlerts, 2)
	assert.Empty(t, skippedAlerts)

	alerts = append(alerts, &common.Alert{
		SlackChannelID: "123", Header: "c",
	})

	// Three alerts is over the limit -> one alert should be skipped, and an overflow alert should be added
	keptAlerts, skippedAlerts = reduceAlertCountForChannel("123", alerts, limit)
	assert.Len(t, keptAlerts, 3)
	assert.Len(t, skippedAlerts, 1)
	assert.Equal(t, "a", keptAlerts[0].Header)
	assert.Equal(t, "b", keptAlerts[1].Header)
	assert.Equal(t, ":status: Too many alerts", keptAlerts[2].Header)

	alerts = []*common.Alert{
		{SlackChannelID: "123", Header: "a"},
		{SlackChannelID: "123", Header: "b"},
		{SlackChannelID: "123", Header: "c"},
		{SlackChannelID: "123", Header: "d"},
	}

	// Four alerts is over the limit -> two alerts should be skipped, and an overflow alert should be added
	keptAlerts, skippedAlerts = reduceAlertCountForChannel("123", alerts, limit)
	assert.Len(t, keptAlerts, 3)
	assert.Len(t, skippedAlerts, 2)
	assert.Equal(t, "a", keptAlerts[0].Header)
	assert.Equal(t, "b", keptAlerts[1].Header)
	assert.Equal(t, ":status: Too many alerts", keptAlerts[2].Header)
}

func TestAlertInputParser(t *testing.T) {
	t.Parallel()

	t.Run("array input", func(t *testing.T) {
		t.Parallel()
		input := `[{"header":"foo"}, {"header":"bar"}]`
		alerts, err := parseAlertInput([]byte(input))
		require.NoError(t, err)
		assert.Len(t, alerts, 2)
		assert.Equal(t, "foo", alerts[0].Header)
		assert.Equal(t, "bar", alerts[1].Header)
	})

	t.Run("array input with invalid json should return an error", func(t *testing.T) {
		t.Parallel()
		input := `[{"header":"foo"`
		_, err := parseAlertInput([]byte(input))
		require.Error(t, err)
	})

	t.Run("object input with all fields at root", func(t *testing.T) {
		t.Parallel()
		input := `{"header":"foo", "footer":"bar"}`
		alerts, err := parseAlertInput([]byte(input))
		require.NoError(t, err)
		assert.Len(t, alerts, 1)
		assert.Equal(t, "foo", alerts[0].Header)
		assert.Equal(t, "bar", alerts[0].Footer)
	})

	t.Run("object input with invalid json should return an error", func(t *testing.T) {
		t.Parallel()
		input := `{"header":"foo", `
		_, err := parseAlertInput([]byte(input))
		require.Error(t, err)
	})

	t.Run("object input with array of alerts", func(t *testing.T) {
		t.Parallel()
		input := `{"alerts":[{"header":"foo"}, {"header":"bar"}]}`
		alerts, err := parseAlertInput([]byte(input))
		require.NoError(t, err)
		assert.Len(t, alerts, 2)
		assert.Equal(t, "foo", alerts[0].Header)
		assert.Equal(t, "bar", alerts[1].Header)
	})
}

func TestIgnoreAlert(t *testing.T) {
	t.Parallel()

	t.Run("nil alert returns false", func(t *testing.T) {
		t.Parallel()
		ignore, reason := ignoreAlert(nil)
		assert.False(t, ignore)
		assert.Empty(t, reason)
	})

	t.Run("empty ignore list returns false", func(t *testing.T) {
		t.Parallel()
		alert := &common.Alert{
			Text:                 "some error text",
			IgnoreIfTextContains: []string{},
		}
		ignore, reason := ignoreAlert(alert)
		assert.False(t, ignore)
		assert.Empty(t, reason)
	})

	t.Run("empty text returns false", func(t *testing.T) {
		t.Parallel()
		alert := &common.Alert{
			Text:                 "",
			IgnoreIfTextContains: []string{"error"},
		}
		ignore, reason := ignoreAlert(alert)
		assert.False(t, ignore)
		assert.Empty(t, reason)
	})

	t.Run("matching term returns true", func(t *testing.T) {
		t.Parallel()
		alert := &common.Alert{
			Text:                 "This is an error message",
			IgnoreIfTextContains: []string{"error"},
		}
		ignore, reason := ignoreAlert(alert)
		assert.True(t, ignore)
		assert.NotEmpty(t, reason)
	})

	t.Run("case insensitive matching", func(t *testing.T) {
		t.Parallel()
		alert := &common.Alert{
			Text:                 "This is an ERROR message",
			IgnoreIfTextContains: []string{"error"},
		}
		ignore, reason := ignoreAlert(alert)
		assert.True(t, ignore)
		assert.NotEmpty(t, reason)
	})

	t.Run("non-matching term returns false", func(t *testing.T) {
		t.Parallel()
		alert := &common.Alert{
			Text:                 "This is a warning message",
			IgnoreIfTextContains: []string{"error"},
		}
		ignore, reason := ignoreAlert(alert)
		assert.False(t, ignore)
		assert.Empty(t, reason)
	})

	t.Run("empty ignore term is skipped", func(t *testing.T) {
		t.Parallel()
		alert := &common.Alert{
			Text:                 "This is a warning message",
			IgnoreIfTextContains: []string{"", "  ", "warning"},
		}
		ignore, reason := ignoreAlert(alert)
		assert.True(t, ignore)
		assert.NotEmpty(t, reason)
	})

	t.Run("multiple ignore terms - first match wins", func(t *testing.T) {
		t.Parallel()
		alert := &common.Alert{
			Text:                 "This is an error and warning",
			IgnoreIfTextContains: []string{"fatal", "error", "warning"},
		}
		ignore, reason := ignoreAlert(alert)
		assert.True(t, ignore)
		assert.NotEmpty(t, reason)
	})
}

// Mock types for internal tests

type internalMockLogger struct{}

func (m *internalMockLogger) Debug(msg string)                                       {}
func (m *internalMockLogger) Debugf(format string, args ...interface{})              {}
func (m *internalMockLogger) Info(msg string)                                        {}
func (m *internalMockLogger) Infof(format string, args ...interface{})               {}
func (m *internalMockLogger) Warn(msg string)                                        {}
func (m *internalMockLogger) Warnf(format string, args ...interface{})               {}
func (m *internalMockLogger) Error(msg string)                                       {}
func (m *internalMockLogger) Errorf(format string, args ...interface{})              {}
func (m *internalMockLogger) WithField(key string, value interface{}) common.Logger  { return m }
func (m *internalMockLogger) WithFields(fields map[string]interface{}) common.Logger { return m }
func (m *internalMockLogger) HttpLoggingHandler() io.Writer                          { return nil }

type internalMockQueue struct {
	mu       sync.Mutex
	messages []struct {
		channelID string
		dedupID   string
		body      string
	}
	sendErr error
}

func newInternalMockQueue() *internalMockQueue {
	return &internalMockQueue{
		messages: make([]struct {
			channelID string
			dedupID   string
			body      string
		}, 0),
	}
}

func (m *internalMockQueue) Send(ctx context.Context, slackChannelID, dedupID, body string) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, struct {
		channelID string
		dedupID   string
		body      string
	}{slackChannelID, dedupID, body})
	return nil
}

type internalMockChannelInfoProvider struct {
	channels     map[string]*channelInfo
	channelNames map[string]string
}

func newInternalMockChannelInfoProvider() *internalMockChannelInfoProvider {
	return &internalMockChannelInfoProvider{
		channels:     make(map[string]*channelInfo),
		channelNames: make(map[string]string),
	}
}

func (m *internalMockChannelInfoProvider) GetChannelInfo(ctx context.Context, channelID string) (*channelInfo, error) {
	if info, ok := m.channels[channelID]; ok {
		return info, nil
	}
	return &channelInfo{
		ChannelExists:      false,
		ChannelIsArchived:  false,
		ManagerIsInChannel: false,
		UserCount:          0,
	}, nil
}

func (m *internalMockChannelInfoProvider) MapChannelNameToIDIfNeeded(channelIDOrName string) string {
	if id, ok := m.channelNames[channelIDOrName]; ok {
		return id
	}
	return channelIDOrName
}

func (m *internalMockChannelInfoProvider) ManagedChannels() any {
	return m.channelNames
}

func (m *internalMockChannelInfoProvider) Init(ctx context.Context) error {
	return nil
}

func (m *internalMockChannelInfoProvider) Run(ctx context.Context) error {
	return nil
}

func (m *internalMockChannelInfoProvider) addChannel(id string, info *channelInfo) {
	m.channels[id] = info
}

func internalTestServer(t *testing.T) (*Server, *internalMockQueue, *internalMockChannelInfoProvider) {
	t.Helper()

	queue := newInternalMockQueue()
	logger := &internalMockLogger{}
	metrics := &common.NoopMetrics{}
	cfg := config.NewDefaultAPIConfig()
	cfg.SlackClient.AppToken = "xapp-test"
	cfg.SlackClient.BotToken = "xoxb-test"
	settings := &config.APISettings{}
	settings.RoutingRules = []*config.RoutingRule{}
	_ = settings.InitAndValidate(logger)

	server := New(queue, nil, logger, metrics, cfg, settings)

	mockProvider := newInternalMockChannelInfoProvider()
	server.channelInfoSyncer = mockProvider

	return server, queue, mockProvider
}

func internalValidChannelInfo() *channelInfo {
	return &channelInfo{
		ChannelExists:      true,
		ChannelIsArchived:  false,
		ManagerIsInChannel: true,
		UserCount:          5,
	}
}

func TestProcessQueuedAlert(t *testing.T) {
	t.Parallel()

	t.Run("valid alert is processed and queued", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := internalTestServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, internalValidChannelInfo())

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-123"
		alert.SlackChannelID = channelID

		logger := &internalMockLogger{}
		err := server.processQueuedAlert(context.Background(), alert, logger)

		require.NoError(t, err)
		assert.Len(t, queue.messages, 1)
	})

	t.Run("alert without channel uses routing rules", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := internalTestServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, internalValidChannelInfo())

		// Set up routing rule
		server.apiSettings.RoutingRules = []*config.RoutingRule{
			{
				Name:     "catch-all",
				Channel:  channelID,
				MatchAll: true,
			},
		}
		_ = server.apiSettings.InitAndValidate(&internalMockLogger{})

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-456"
		alert.RouteKey = "my-service"

		logger := &internalMockLogger{}
		err := server.processQueuedAlert(context.Background(), alert, logger)

		require.NoError(t, err)
		assert.Len(t, queue.messages, 1)
	})

	t.Run("alert without channel and no routing rule returns error", func(t *testing.T) {
		t.Parallel()

		server, _, _ := internalTestServer(t)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-789"
		// No channel, no route key

		logger := &internalMockLogger{}
		err := server.processQueuedAlert(context.Background(), alert, logger)

		require.Error(t, err)
	})

	t.Run("alert for non-existent channel returns nil (logged but not error)", func(t *testing.T) {
		t.Parallel()

		server, queue, _ := internalTestServer(t)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-no-channel"
		alert.SlackChannelID = "C999999999"

		logger := &internalMockLogger{}
		err := server.processQueuedAlert(context.Background(), alert, logger)

		// Returns nil because validation error is logged but not returned
		assert.NoError(t, err)
		assert.Empty(t, queue.messages)
	})

	t.Run("ignored alert is not queued", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := internalTestServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, internalValidChannelInfo())

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-ignored"
		alert.SlackChannelID = channelID
		alert.Text = "This contains ignore-pattern"
		alert.IgnoreIfTextContains = []string{"ignore-pattern"}

		logger := &internalMockLogger{}
		err := server.processQueuedAlert(context.Background(), alert, logger)

		require.NoError(t, err)
		assert.Empty(t, queue.messages)
	})

	t.Run("invalid alert returns nil (logged but not error)", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := internalTestServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, internalValidChannelInfo())

		// Alert missing required fields
		alert := &common.Alert{
			SlackChannelID: channelID,
			// Missing Header, CorrelationID
		}

		logger := &internalMockLogger{}
		err := server.processQueuedAlert(context.Background(), alert, logger)

		// Returns nil because validation error is logged but not returned
		assert.NoError(t, err)
		assert.Empty(t, queue.messages)
	})

}

func TestLogAlerts(t *testing.T) {
	t.Parallel()

	t.Run("logs alerts with duration and fields", func(t *testing.T) {
		t.Parallel()

		server, _, _ := internalTestServer(t)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test"
		alert.CorrelationID = "test-log"
		alert.SlackChannelID = "C123"

		// Just ensure it doesn't panic
		assert.NotPanics(t, func() {
			server.logAlerts("Test message", "reason", time.Now(), alert)
		})
	})

	t.Run("logs alerts without reason", func(t *testing.T) {
		t.Parallel()

		server, _, _ := internalTestServer(t)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test"
		alert.CorrelationID = "test-log-no-reason"
		alert.SlackChannelID = "C123"

		assert.NotPanics(t, func() {
			server.logAlerts("Test message", "", time.Now(), alert)
		})
	})

	t.Run("logs multiple alerts", func(t *testing.T) {
		t.Parallel()

		server, _, _ := internalTestServer(t)

		alerts := []*common.Alert{
			{Header: ":status: Alert 1", CorrelationID: "corr-1", SlackChannelID: "C123"},
			{Header: ":status: Alert 2", CorrelationID: "corr-2", SlackChannelID: "C456"},
		}

		assert.NotPanics(t, func() {
			server.logAlerts("Test message", "", time.Now(), alerts...)
		})
	})
}
