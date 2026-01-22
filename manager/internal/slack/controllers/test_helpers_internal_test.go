package controllers

import (
	"context"
	"io"
	"time"

	"github.com/eko/gocache/lib/v4/store"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"github.com/stretchr/testify/mock"
)

// --- Mock implementations ---

// mockSocketModeClient implements SocketModeClient for testing.
type mockSocketModeClient struct {
	mock.Mock

	eventsChan     chan socketmode.Event
	runContextDone chan struct{} // Close to unblock RunContext
}

func newMockSocketModeClient() *mockSocketModeClient {
	return &mockSocketModeClient{
		eventsChan:     make(chan socketmode.Event, 10),
		runContextDone: make(chan struct{}),
	}
}

func (m *mockSocketModeClient) Ack(req socketmode.Request, payload ...any) {
	m.Called(req, payload)
}

func (m *mockSocketModeClient) RunContext(ctx context.Context) error {
	args := m.Called(ctx)
	// Block until context is cancelled or done channel is closed
	select {
	case <-ctx.Done():
	case <-m.runContextDone:
	}
	return args.Error(0)
}

func (m *mockSocketModeClient) Events() chan socketmode.Event {
	return m.eventsChan
}

// mockSlackAPIClient implements SlackAPIClient for testing.
type mockSlackAPIClient struct {
	mock.Mock
}

func (m *mockSlackAPIClient) PostEphemeral(ctx context.Context, channelID, userID string, options ...slack.MsgOption) (string, error) {
	args := m.Called(ctx, channelID, userID, options)
	return args.String(0), args.Error(1)
}

func (m *mockSlackAPIClient) GetUserInfo(ctx context.Context, user string) (*slack.User, error) {
	args := m.Called(ctx, user)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	userInfo, ok := args.Get(0).(*slack.User)
	if !ok {
		return nil, args.Error(1)
	}
	return userInfo, args.Error(1)
}

func (m *mockSlackAPIClient) UserIsInGroup(ctx context.Context, groupID, userID string) bool {
	args := m.Called(ctx, groupID, userID)
	return args.Bool(0)
}

func (m *mockSlackAPIClient) SendResponse(ctx context.Context, channelID, responseURL, responseType, text string) error {
	args := m.Called(ctx, channelID, responseURL, responseType, text)
	return args.Error(0)
}

func (m *mockSlackAPIClient) OpenModal(ctx context.Context, triggerID string, request slack.ModalViewRequest) error {
	args := m.Called(ctx, triggerID, request)
	return args.Error(0)
}

func (m *mockSlackAPIClient) IsAlertChannel(ctx context.Context, channelID string) (bool, string, error) {
	args := m.Called(ctx, channelID)
	return args.Bool(0), args.String(1), args.Error(2)
}

// mockFifoQueueProducer implements FifoQueueProducer for testing.
type mockFifoQueueProducer struct {
	mock.Mock
}

func (m *mockFifoQueueProducer) Send(ctx context.Context, slackChannelID, dedupID, body string) error {
	args := m.Called(ctx, slackChannelID, dedupID, body)
	return args.Error(0)
}

// mockIssueFinder implements IssueFinder for testing.
type mockIssueFinder struct {
	mock.Mock
}

func (m *mockIssueFinder) FindIssueBySlackPost(ctx context.Context, channelID string, slackPostID string, includeArchived bool) *models.Issue {
	args := m.Called(ctx, channelID, slackPostID, includeArchived)
	if args.Get(0) == nil {
		return nil
	}
	issue, ok := args.Get(0).(*models.Issue)
	if !ok {
		return nil
	}
	return issue
}

// mockLogger implements common.Logger for testing.
type mockLogger struct {
	mock.Mock
}

func (m *mockLogger) Debug(msg string)                               { m.Called(msg) }
func (m *mockLogger) Debugf(format string, args ...any)              { m.Called(format, args) }
func (m *mockLogger) Info(msg string)                                { m.Called(msg) }
func (m *mockLogger) Infof(format string, args ...any)               { m.Called(format, args) }
func (m *mockLogger) Error(msg string)                               { m.Called(msg) }
func (m *mockLogger) Errorf(format string, args ...any)              { m.Called(format, args) }
func (m *mockLogger) WithField(key string, value any) common.Logger  { return m }
func (m *mockLogger) WithFields(fields map[string]any) common.Logger { return m }
func (m *mockLogger) HttpLoggingHandler() io.Writer                  { return io.Discard }

// mockCacheStore implements store.StoreInterface for testing.
type mockCacheStore struct {
	mock.Mock
}

func (m *mockCacheStore) Get(ctx context.Context, key any) (any, error) {
	args := m.Called(ctx, key)
	return args.Get(0), args.Error(1)
}

func (m *mockCacheStore) GetWithTTL(ctx context.Context, key any) (any, time.Duration, error) {
	args := m.Called(ctx, key)
	ttl, ok := args.Get(1).(time.Duration)
	if !ok {
		return args.Get(0), 0, args.Error(2)
	}
	return args.Get(0), ttl, args.Error(2)
}

func (m *mockCacheStore) Set(ctx context.Context, key any, value any, options ...store.Option) error {
	args := m.Called(ctx, key, value, options)
	return args.Error(0)
}

func (m *mockCacheStore) Delete(ctx context.Context, key any) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func (m *mockCacheStore) Invalidate(ctx context.Context, options ...store.InvalidateOption) error {
	args := m.Called(ctx, options)
	return args.Error(0)
}

func (m *mockCacheStore) Clear(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *mockCacheStore) GetType() string {
	args := m.Called()
	return args.String(0)
}

// --- Test helper functions ---

func newTestManagerConfig() *config.ManagerConfig {
	cfg := config.NewDefaultManagerConfig()
	cfg.EncryptionKey = "abcdefghijklmnopqrstuvwxyz123456"
	cfg.SlackClient.AppToken = "xapp-test"
	cfg.SlackClient.BotToken = "xoxb-test"
	return cfg
}

func newTestManagerSettings() *models.ManagerSettingsWrapper {
	settings := &config.ManagerSettings{
		AppFriendlyName: "TestApp",
		IssueReactions: &config.IssueReactionSettings{
			TerminateEmojis:         []string{":skull:"},
			ResolveEmojis:           []string{":white_check_mark:"},
			MuteEmojis:              []string{":mute:"},
			InvestigateEmojis:       []string{":eyes:"},
			ShowOptionButtonsEmojis: []string{":gear:"},
		},
	}
	return models.NewManagerSettingsWrapper(settings)
}
