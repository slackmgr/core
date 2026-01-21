package internal_test

import (
	"context"
	"testing"
	"time"

	gocache_store "github.com/eko/gocache/store/go_cache/v4"
	gocache "github.com/patrickmn/go-cache"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockLogger struct{}

func (m *mockLogger) Debug(msg string)                               {}
func (m *mockLogger) Debugf(format string, args ...any)              {}
func (m *mockLogger) Info(msg string)                                {}
func (m *mockLogger) Infof(format string, args ...any)               {}
func (m *mockLogger) Error(msg string)                               {}
func (m *mockLogger) Errorf(format string, args ...any)              {}
func (m *mockLogger) WithField(key string, value any) common.Logger  { return m }
func (m *mockLogger) WithFields(fields map[string]any) common.Logger { return m }

type mockMetrics struct {
	counters map[string]int
}

func newMockMetrics() *mockMetrics {
	return &mockMetrics{counters: make(map[string]int)}
}

func (m *mockMetrics) RegisterCounter(name, help string, labelNames ...string)                  {}
func (m *mockMetrics) RegisterGauge(name, help string, labelNames ...string)                    {}
func (m *mockMetrics) RegisterHistogram(name, help string, buckets []float64, labels ...string) {}
func (m *mockMetrics) Inc(name string, labelValues ...string)                                   { m.counters[name]++ }
func (m *mockMetrics) Dec(name string, labelValues ...string)                                   {}
func (m *mockMetrics) Add(name string, val float64, labelValues ...string)                      {}
func (m *mockMetrics) Set(name string, val float64, labelValues ...string)                      {}
func (m *mockMetrics) Observe(name string, val float64, labelValues ...string)                  {}
func (m *mockMetrics) Handler() any                                                             { return nil }

func newTestClient() *internal.SlackAPIClient {
	gocacheClient := gocache.New(5*time.Minute, time.Minute)
	store := gocache_store.NewGoCache(gocacheClient)

	cfg := config.NewDefaultSlackClientConfig()
	cfg.AppToken = "xapp-test"
	cfg.BotToken = "xoxb-test"

	return internal.NewSlackAPIClient(store, "test:", &mockLogger{}, newMockMetrics(), cfg)
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("creates client with valid config", func(t *testing.T) {
		t.Parallel()

		client := newTestClient()
		require.NotNil(t, client)
	})

	t.Run("sets defaults on config", func(t *testing.T) {
		t.Parallel()

		gocacheClient := gocache.New(5*time.Minute, time.Minute)
		store := gocache_store.NewGoCache(gocacheClient)

		cfg := &config.SlackClientConfig{
			AppToken: "xapp-test",
			BotToken: "xoxb-test",
		}

		client := internal.NewSlackAPIClient(store, "test:", &mockLogger{}, newMockMetrics(), cfg)
		require.NotNil(t, client)

		// Verify defaults were set
		assert.Equal(t, 3, cfg.Concurrency)
		assert.Equal(t, 30, cfg.HTTPTimeoutSeconds)
	})
}

func TestClient_BotUserID(t *testing.T) {
	t.Parallel()

	t.Run("returns empty string before connect", func(t *testing.T) {
		t.Parallel()

		client := newTestClient()
		assert.Empty(t, client.BotUserID())
	})
}

func TestClient_API(t *testing.T) {
	t.Parallel()

	t.Run("returns nil before connect", func(t *testing.T) {
		t.Parallel()

		client := newTestClient()
		assert.Nil(t, client.API())
	})
}

func TestClient_ErrNotConnected(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name string
		fn   func(*internal.SlackAPIClient) error
	}{
		{
			name: "ChatPostMessage",
			fn: func(c *internal.SlackAPIClient) error {
				_, err := c.ChatPostMessage(ctx, "channel")
				return err
			},
		},
		{
			name: "ChatUpdateMessage",
			fn: func(c *internal.SlackAPIClient) error {
				_, err := c.ChatUpdateMessage(ctx, "channel")
				return err
			},
		},
		{
			name: "ChatDeleteMessage",
			fn: func(c *internal.SlackAPIClient) error {
				return c.ChatDeleteMessage(ctx, "channel", "ts")
			},
		},
		{
			name: "SendResponse",
			fn: func(c *internal.SlackAPIClient) error {
				return c.SendResponse(ctx, "channel", "url", "type", "text")
			},
		},
		{
			name: "PostEphemeral",
			fn: func(c *internal.SlackAPIClient) error {
				_, err := c.PostEphemeral(ctx, "channel", "user")
				return err
			},
		},
		{
			name: "OpenModal",
			fn: func(c *internal.SlackAPIClient) error {
				return c.OpenModal(ctx, "trigger", slack.ModalViewRequest{})
			},
		},
		{
			name: "MessageHasReplies",
			fn: func(c *internal.SlackAPIClient) error {
				_, err := c.MessageHasReplies(ctx, "channel", "ts")
				return err
			},
		},
		{
			name: "GetChannelInfo",
			fn: func(c *internal.SlackAPIClient) error {
				_, err := c.GetChannelInfo(ctx, "channel")
				return err
			},
		},
		{
			name: "ListBotChannels",
			fn: func(c *internal.SlackAPIClient) error {
				_, err := c.ListBotChannels(ctx)
				return err
			},
		},
		{
			name: "GetUserInfo",
			fn: func(c *internal.SlackAPIClient) error {
				_, err := c.GetUserInfo(ctx, "user")
				return err
			},
		},
		{
			name: "ListUserGroupMembers",
			fn: func(c *internal.SlackAPIClient) error {
				_, err := c.ListUserGroupMembers(ctx, "group")
				return err
			},
		},
		{
			name: "GetUserIDsInChannel",
			fn: func(c *internal.SlackAPIClient) error {
				_, err := c.GetUserIDsInChannel(ctx, "channel")
				return err
			},
		},
		{
			name: "BotIsInChannel",
			fn: func(c *internal.SlackAPIClient) error {
				_, err := c.BotIsInChannel(ctx, "channel")
				return err
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name+" returns ErrNotConnected", func(t *testing.T) {
			t.Parallel()

			client := newTestClient()
			err := tc.fn(client)

			require.Error(t, err)
			assert.ErrorIs(t, err, internal.ErrNotConnected)
		})
	}
}

func TestClient_Connect(t *testing.T) {
	t.Parallel()

	t.Run("fails with nil metrics", func(t *testing.T) {
		t.Parallel()

		gocacheClient := gocache.New(5*time.Minute, time.Minute)
		store := gocache_store.NewGoCache(gocacheClient)

		cfg := config.NewDefaultSlackClientConfig()
		cfg.AppToken = "xapp-test"
		cfg.BotToken = "xoxb-test"

		client := internal.NewSlackAPIClient(store, "test:", &mockLogger{}, nil, cfg)

		_, err := client.Connect(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "metrics cannot be nil")
	})

	t.Run("fails with empty bot token", func(t *testing.T) {
		t.Parallel()

		gocacheClient := gocache.New(5*time.Minute, time.Minute)
		store := gocache_store.NewGoCache(gocacheClient)

		cfg := config.NewDefaultSlackClientConfig()
		cfg.AppToken = "xapp-test"
		cfg.BotToken = ""

		client := internal.NewSlackAPIClient(store, "test:", &mockLogger{}, newMockMetrics(), cfg)

		_, err := client.Connect(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bot token cannot be nil")
	})
}

func TestClient_InputValidation(t *testing.T) {
	t.Parallel()

	// Note: These tests verify input validation happens AFTER connection check.
	// Since we can't easily mock the Slack API to test post-connection validation,
	// we test that the connection check happens first.

	ctx := context.Background()

	t.Run("SendResponse validates channelID after connection check", func(t *testing.T) {
		t.Parallel()

		client := newTestClient()
		err := client.SendResponse(ctx, "", "url", "type", "text")

		// Should fail with ErrNotConnected first, not channelID validation
		assert.ErrorIs(t, err, internal.ErrNotConnected)
	})

	t.Run("PostEphemeral validates channelID after connection check", func(t *testing.T) {
		t.Parallel()

		client := newTestClient()
		_, err := client.PostEphemeral(ctx, "", "user")

		// Should fail with ErrNotConnected first
		assert.ErrorIs(t, err, internal.ErrNotConnected)
	})

	t.Run("OpenModal validates triggerID after connection check", func(t *testing.T) {
		t.Parallel()

		client := newTestClient()
		err := client.OpenModal(ctx, "", slack.ModalViewRequest{})

		// Should fail with ErrNotConnected first
		assert.ErrorIs(t, err, internal.ErrNotConnected)
	})

	t.Run("MessageHasReplies validates channelID after connection check", func(t *testing.T) {
		t.Parallel()

		client := newTestClient()
		_, err := client.MessageHasReplies(ctx, "", "ts")

		// Should fail with ErrNotConnected first
		assert.ErrorIs(t, err, internal.ErrNotConnected)
	})

	t.Run("GetChannelInfo validates channelID after connection check", func(t *testing.T) {
		t.Parallel()

		client := newTestClient()
		_, err := client.GetChannelInfo(ctx, "")

		// Should fail with ErrNotConnected first
		assert.ErrorIs(t, err, internal.ErrNotConnected)
	})

	t.Run("GetUserInfo validates userID after connection check", func(t *testing.T) {
		t.Parallel()

		client := newTestClient()
		_, err := client.GetUserInfo(ctx, "")

		// Should fail with ErrNotConnected first
		assert.ErrorIs(t, err, internal.ErrNotConnected)
	})

	t.Run("ListUserGroupMembers validates groupID after connection check", func(t *testing.T) {
		t.Parallel()

		client := newTestClient()
		_, err := client.ListUserGroupMembers(ctx, "")

		// Should fail with ErrNotConnected first
		assert.ErrorIs(t, err, internal.ErrNotConnected)
	})

	t.Run("GetUserIDsInChannel validates channelID after connection check", func(t *testing.T) {
		t.Parallel()

		client := newTestClient()
		_, err := client.GetUserIDsInChannel(ctx, "")

		// Should fail with ErrNotConnected first
		assert.ErrorIs(t, err, internal.ErrNotConnected)
	})

	t.Run("BotIsInChannel validates channelID after connection check", func(t *testing.T) {
		t.Parallel()

		client := newTestClient()
		_, err := client.BotIsInChannel(ctx, "")

		// Should fail with ErrNotConnected first
		assert.ErrorIs(t, err, internal.ErrNotConnected)
	})
}

func TestConstants(t *testing.T) {
	t.Parallel()

	t.Run("error constants are defined", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "channel_not_found", internal.SlackChannelNotFoundError)
		assert.Equal(t, "thread_not_found", internal.SlackThreadNotFoundError)
		assert.Equal(t, "no_such_subteam", internal.SlackNoSuchSubTeamError)
	})

	t.Run("ErrNotConnected is exported", func(t *testing.T) {
		t.Parallel()

		require.Error(t, internal.ErrNotConnected)
		assert.Contains(t, internal.ErrNotConnected.Error(), "Connect()")
	})
}
