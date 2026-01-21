package restapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// --- Mocks ---

type mockLogger struct {
	mock.Mock
}

func (m *mockLogger) Debug(msg string)                               {}
func (m *mockLogger) Debugf(format string, args ...any)              {}
func (m *mockLogger) Info(msg string)                                {}
func (m *mockLogger) Infof(format string, args ...any)               {}
func (m *mockLogger) Error(msg string)                               {}
func (m *mockLogger) Errorf(format string, args ...any)              {}
func (m *mockLogger) WithField(key string, value any) common.Logger  { return m }
func (m *mockLogger) WithFields(fields map[string]any) common.Logger { return m }

type mockFifoQueueProducer struct {
	mock.Mock
}

func (m *mockFifoQueueProducer) Send(ctx context.Context, slackChannelID, dedupID, body string) error {
	args := m.Called(ctx, slackChannelID, dedupID, body)
	return args.Error(0)
}

type mockChannelInfoProvider struct {
	mock.Mock
}

type mockSlackClient struct {
	mock.Mock
}

func (m *mockSlackClient) GetChannelInfo(ctx context.Context, channelID string) (*slack.Channel, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	channel, ok := args.Get(0).(*slack.Channel)
	if !ok {
		return nil, args.Error(1)
	}

	return channel, args.Error(1)
}

func (m *mockSlackClient) GetUserIDsInChannel(ctx context.Context, channelID string) (map[string]struct{}, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	userIDs, ok := args.Get(0).(map[string]struct{})
	if !ok {
		return nil, args.Error(1)
	}

	return userIDs, args.Error(1)
}

func (m *mockSlackClient) BotIsInChannel(ctx context.Context, channelID string) (bool, error) {
	args := m.Called(ctx, channelID)
	return args.Bool(0), args.Error(1)
}

func (m *mockSlackClient) ListBotChannels(ctx context.Context) ([]*internal.ChannelSummary, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	channels, ok := args.Get(0).([]*internal.ChannelSummary)
	if !ok {
		return nil, args.Error(1)
	}

	return channels, args.Error(1)
}

func (m *mockChannelInfoProvider) GetChannelInfo(ctx context.Context, channel string) (*ChannelInfo, error) {
	args := m.Called(ctx, channel)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	info, ok := args.Get(0).(*ChannelInfo)
	if !ok {
		return nil, args.Error(1)
	}

	return info, args.Error(1)
}

func (m *mockChannelInfoProvider) MapChannelNameToIDIfNeeded(channelName string) string {
	args := m.Called(channelName)
	return args.String(0)
}

func (m *mockChannelInfoProvider) ManagedChannels() []*internal.ChannelSummary {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}

	channels, ok := args.Get(0).([]*internal.ChannelSummary)
	if !ok {
		return nil
	}

	return channels
}

// --- Test Helpers ---

// assertJSONError verifies that the response is a JSON error with the expected content-type
// and that the error message contains the expected substring.
func assertJSONError(t *testing.T, w *httptest.ResponseRecorder, expectedContains string) {
	t.Helper()

	// Verify Content-Type header
	contentType := w.Header().Get("Content-Type")
	assert.Contains(t, contentType, "application/json", "expected JSON content-type")

	// Parse the JSON response
	var errResp errorResponse
	err := json.Unmarshal(w.Body.Bytes(), &errResp)
	require.NoError(t, err, "response should be valid JSON")

	// Verify the error field contains the expected substring
	assert.Contains(t, errResp.Error, expectedContains, "error message should contain expected text")
}

func newTestServer(t *testing.T) (*Server, *mockFifoQueueProducer, *mockChannelInfoProvider) {
	t.Helper()

	logger := &mockLogger{}
	queue := &mockFifoQueueProducer{}
	channelInfo := &mockChannelInfoProvider{}

	cfg := &config.APIConfig{
		RestPort:               "8080",
		MaxUsersInAlertChannel: 1000,
		RateLimitPerAlertChannel: &config.RateLimitConfig{
			AlertsPerSecond:    10,
			AllowedBurst:       100,
			MaxRequestWaitTime: 5 * time.Second,
		},
	}

	server := New(queue, nil, logger, nil, cfg, nil)
	server.setChannelInfoProvider(channelInfo)
	server.apiSettings = &config.APISettings{}

	return server, queue, channelInfo
}

// --- Tests ---

func TestServer_HandlePing(t *testing.T) {
	t.Parallel()

	t.Run("returns 200 OK", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		router := gin.New()
		router.GET("/ping", server.ping)

		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("returns custom status code", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		router := gin.New()
		router.GET("/ping", server.ping)

		req := httptest.NewRequest(http.MethodGet, "/ping?status=503", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})

	t.Run("ignores invalid status code", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		router := gin.New()
		router.GET("/ping", server.ping)

		req := httptest.NewRequest(http.MethodGet, "/ping?status=9999", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestServer_HandleChannels(t *testing.T) {
	t.Parallel()

	t.Run("returns channel list", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServer(t)

		channels := []*internal.ChannelSummary{
			{ID: "C123", Name: "general"},
			{ID: "C456", Name: "random"},
		}
		channelInfo.On("ManagedChannels").Return(channels)

		router := gin.New()
		router.GET("/channels", server.handleChannels)

		req := httptest.NewRequest(http.MethodGet, "/channels", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

		var result []*internal.ChannelSummary
		err := json.Unmarshal(w.Body.Bytes(), &result)
		require.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, "C123", result[0].ID)
	})

	t.Run("returns empty array when no channels", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServer(t)
		channelInfo.On("ManagedChannels").Return([]*internal.ChannelSummary{})

		router := gin.New()
		router.GET("/channels", server.handleChannels)

		req := httptest.NewRequest(http.MethodGet, "/channels", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "[]", w.Body.String())
	})
}

func TestServer_HandleMappings(t *testing.T) {
	t.Parallel()

	t.Run("returns routing rules", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		server.apiSettings = &config.APISettings{
			RoutingRules: []*config.RoutingRule{
				{Name: "test-rule", Equals: []string{"test"}, Channel: "C123"},
			},
		}

		router := gin.New()
		router.GET("/mappings", server.handleMappings)

		req := httptest.NewRequest(http.MethodGet, "/mappings", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "test")
		assert.Contains(t, w.Body.String(), "C123")
	})
}

func TestServer_HandleAlerts_Validation(t *testing.T) {
	t.Parallel()

	t.Run("returns 400 for empty body", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		router := gin.New()
		router.POST("/alert", server.handleAlerts)

		req := httptest.NewRequest(http.MethodPost, "/alert", nil)
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = 0
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertJSONError(t, w, "POST body")
	})

	t.Run("returns 400 for invalid JSON", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		router := gin.New()
		router.POST("/alert", server.handleAlerts)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewReader([]byte("invalid json")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertJSONError(t, w, "parse")
	})

	t.Run("returns 204 for empty alerts array", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		router := gin.New()
		router.POST("/alert", server.handleAlerts)

		body := `[]`
		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
	})
}

func TestServer_HandleAlerts_Success(t *testing.T) {
	t.Parallel()

	t.Run("accepts valid alert and queues it", func(t *testing.T) {
		t.Parallel()

		server, queue, channelInfo := newTestServer(t)

		channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
		channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(&ChannelInfo{
			ChannelExists:      true,
			ManagerIsInChannel: true,
			UserCount:          10,
		}, nil)
		queue.On("Send", mock.Anything, "C123", mock.Anything, mock.Anything).Return(nil)

		router := gin.New()
		router.POST("/alert", server.handleAlerts)

		alert := common.Alert{
			SlackChannelID: "C123",
			Header:         "Test Alert",
		}
		body, _ := json.Marshal(alert)
		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusAccepted, w.Code)
		queue.AssertCalled(t, "Send", mock.Anything, "C123", mock.Anything, mock.Anything)
	})

	t.Run("uses channel ID from URL parameter", func(t *testing.T) {
		t.Parallel()

		server, queue, channelInfo := newTestServer(t)

		channelInfo.On("MapChannelNameToIDIfNeeded", "C456").Return("C456")
		channelInfo.On("GetChannelInfo", mock.Anything, "C456").Return(&ChannelInfo{
			ChannelExists:      true,
			ManagerIsInChannel: true,
			UserCount:          10,
		}, nil)
		queue.On("Send", mock.Anything, "C456", mock.Anything, mock.Anything).Return(nil)

		router := gin.New()
		router.POST("/alert/:slackChannelId", server.handleAlerts)

		alert := common.Alert{Header: "Test Alert"}
		body, _ := json.Marshal(alert)
		req := httptest.NewRequest(http.MethodPost, "/alert/C456", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusAccepted, w.Code)
		queue.AssertCalled(t, "Send", mock.Anything, "C456", mock.Anything, mock.Anything)
	})
}

func TestServer_HandleAlerts_ChannelValidation(t *testing.T) {
	t.Parallel()

	t.Run("returns 400 for non-existent channel", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServer(t)

		channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
		channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(&ChannelInfo{
			ChannelExists: false,
		}, nil)

		router := gin.New()
		router.POST("/alert", server.handleAlerts)

		alert := common.Alert{SlackChannelID: "C123", Header: "Test"}
		body, _ := json.Marshal(alert)
		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertJSONError(t, w, "Unable to find channel")
	})

	t.Run("returns 400 for archived channel", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServer(t)

		channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
		channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(&ChannelInfo{
			ChannelExists:     true,
			ChannelIsArchived: true,
		}, nil)

		router := gin.New()
		router.POST("/alert", server.handleAlerts)

		alert := common.Alert{SlackChannelID: "C123", Header: "Test"}
		body, _ := json.Marshal(alert)
		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertJSONError(t, w, "archived")
	})

	t.Run("returns 400 when bot not in channel", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServer(t)

		channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
		channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(&ChannelInfo{
			ChannelExists:      true,
			ManagerIsInChannel: false,
		}, nil)

		router := gin.New()
		router.POST("/alert", server.handleAlerts)

		alert := common.Alert{SlackChannelID: "C123", Header: "Test"}
		body, _ := json.Marshal(alert)
		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertJSONError(t, w, "not in channel")
	})

	t.Run("returns 400 when channel has too many users", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServer(t)
		server.cfg.MaxUsersInAlertChannel = 100

		channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
		channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(&ChannelInfo{
			ChannelExists:      true,
			ManagerIsInChannel: true,
			UserCount:          500,
		}, nil)

		router := gin.New()
		router.POST("/alert", server.handleAlerts)

		alert := common.Alert{SlackChannelID: "C123", Header: "Test"}
		body, _ := json.Marshal(alert)
		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertJSONError(t, w, "exceeds the limit")
	})
}

func TestServer_HandleAlerts_RateLimiting(t *testing.T) {
	t.Parallel()

	t.Run("returns 400 when too many alerts for channel", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServer(t)
		server.cfg.RateLimitPerAlertChannel.AllowedBurst = 3 // Only allow 3 alerts per channel

		channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
		channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(&ChannelInfo{
			ChannelExists:      true,
			ManagerIsInChannel: true,
			UserCount:          10,
		}, nil)

		router := gin.New()
		router.POST("/alert", server.handleAlerts)

		// Send 5 alerts which exceeds AllowedBurst of 3
		alerts := []common.Alert{
			{SlackChannelID: "C123", Header: "Alert 1"},
			{SlackChannelID: "C123", Header: "Alert 2"},
			{SlackChannelID: "C123", Header: "Alert 3"},
			{SlackChannelID: "C123", Header: "Alert 4"},
			{SlackChannelID: "C123", Header: "Alert 5"},
		}
		body, _ := json.Marshal(alerts)
		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertJSONError(t, w, "Too many alerts")
	})

	t.Run("returns 429 when rate limit exceeded", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServer(t)
		server.cfg.RateLimitPerAlertChannel.AlertsPerSecond = 0.001 // Very slow
		server.cfg.RateLimitPerAlertChannel.AllowedBurst = 1
		server.cfg.RateLimitPerAlertChannel.MaxRequestWaitTime = 100 * time.Millisecond

		channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
		channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(&ChannelInfo{
			ChannelExists:      true,
			ManagerIsInChannel: true,
			UserCount:          10,
		}, nil)

		// Exhaust the rate limit
		limiter := server.getRateLimiter("C123")
		limiter.AllowN(time.Now(), 1)

		router := gin.New()
		router.POST("/alert", server.handleAlerts)

		alert := common.Alert{
			SlackChannelID: "C123",
			Header:         "Test Alert",
		}
		body, _ := json.Marshal(alert)
		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusTooManyRequests, w.Code)
		assertJSONError(t, w, "Rate limit exceeded")
	})

	t.Run("accepts alerts within burst limit", func(t *testing.T) {
		t.Parallel()

		server, queue, channelInfo := newTestServer(t)
		server.cfg.RateLimitPerAlertChannel.AlertsPerSecond = 100
		server.cfg.RateLimitPerAlertChannel.AllowedBurst = 10

		channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
		channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(&ChannelInfo{
			ChannelExists:      true,
			ManagerIsInChannel: true,
			UserCount:          10,
		}, nil)

		queueCallCount := 0
		queue.On("Send", mock.Anything, "C123", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			queueCallCount++
		}).Return(nil)

		router := gin.New()
		router.POST("/alert", server.handleAlerts)

		// Send 5 alerts which is within AllowedBurst of 10
		alerts := []common.Alert{
			{SlackChannelID: "C123", Header: "Alert 1"},
			{SlackChannelID: "C123", Header: "Alert 2"},
			{SlackChannelID: "C123", Header: "Alert 3"},
			{SlackChannelID: "C123", Header: "Alert 4"},
			{SlackChannelID: "C123", Header: "Alert 5"},
		}
		body, _ := json.Marshal(alerts)
		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusAccepted, w.Code)
		assert.Equal(t, 5, queueCallCount, "expected all 5 alerts to be queued")
	})

	t.Run("handles multiple channels independently", func(t *testing.T) {
		t.Parallel()

		server, queue, channelInfo := newTestServer(t)
		server.cfg.RateLimitPerAlertChannel.AlertsPerSecond = 100
		server.cfg.RateLimitPerAlertChannel.AllowedBurst = 100

		channelInfo.On("MapChannelNameToIDIfNeeded", "C111").Return("C111")
		channelInfo.On("MapChannelNameToIDIfNeeded", "C222").Return("C222")
		channelInfo.On("GetChannelInfo", mock.Anything, "C111").Return(&ChannelInfo{
			ChannelExists:      true,
			ManagerIsInChannel: true,
			UserCount:          10,
		}, nil)
		channelInfo.On("GetChannelInfo", mock.Anything, "C222").Return(&ChannelInfo{
			ChannelExists:      true,
			ManagerIsInChannel: true,
			UserCount:          10,
		}, nil)

		channelCounts := make(map[string]int)
		queue.On("Send", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			channelID := args.String(1)
			channelCounts[channelID]++
		}).Return(nil)

		router := gin.New()
		router.POST("/alert", server.handleAlerts)

		// Send alerts to two different channels
		alerts := []common.Alert{
			{SlackChannelID: "C111", Header: "Alert 1"},
			{SlackChannelID: "C111", Header: "Alert 2"},
			{SlackChannelID: "C222", Header: "Alert 3"},
		}
		body, _ := json.Marshal(alerts)
		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusAccepted, w.Code)
		assert.Equal(t, 2, channelCounts["C111"], "expected 2 alerts for C111")
		assert.Equal(t, 1, channelCounts["C222"], "expected 1 alert for C222")
	})
}

func TestServer_ValidateChannelInfo(t *testing.T) {
	t.Parallel()

	server, _, _ := newTestServer(t)
	server.cfg.MaxUsersInAlertChannel = 100

	tests := []struct {
		name        string
		channelInfo *ChannelInfo
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid channel",
			channelInfo: &ChannelInfo{
				ChannelExists:      true,
				ManagerIsInChannel: true,
				UserCount:          50,
			},
			expectError: false,
		},
		{
			name: "channel does not exist",
			channelInfo: &ChannelInfo{
				ChannelExists: false,
			},
			expectError: true,
			errorMsg:    "unable to find channel",
		},
		{
			name: "channel is archived",
			channelInfo: &ChannelInfo{
				ChannelExists:     true,
				ChannelIsArchived: true,
			},
			expectError: true,
			errorMsg:    "archived",
		},
		{
			name: "bot not in channel",
			channelInfo: &ChannelInfo{
				ChannelExists:      true,
				ManagerIsInChannel: false,
			},
			expectError: true,
			errorMsg:    "not in channel",
		},
		{
			name: "too many users",
			channelInfo: &ChannelInfo{
				ChannelExists:      true,
				ManagerIsInChannel: true,
				UserCount:          500,
			},
			expectError: true,
			errorMsg:    "exceeds the limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := server.validateChannelInfo("C123", tt.channelInfo)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestServer_SetSlackChannelID(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for empty alerts slice", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		err := server.setSlackChannelID(nil)
		require.NoError(t, err)
	})

	t.Run("uses channel ID from alert body", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServer(t)
		channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")

		alert := &common.Alert{SlackChannelID: "C123"}
		err := server.setSlackChannelID(nil, alert)

		require.NoError(t, err)
		assert.Equal(t, "C123", alert.SlackChannelID)
	})

	t.Run("maps channel name to ID", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServer(t)
		channelInfo.On("MapChannelNameToIDIfNeeded", "general").Return("C999888777")

		alert := &common.Alert{SlackChannelID: "general"}
		err := server.setSlackChannelID(nil, alert)

		require.NoError(t, err)
		assert.Equal(t, "C999888777", alert.SlackChannelID)
	})

	t.Run("uses route key to find channel via routing rules", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServer(t)
		channelInfo.On("MapChannelNameToIDIfNeeded", mock.Anything).Return("")

		// Set up routing rules
		server.apiSettings = &config.APISettings{
			RoutingRules: []*config.RoutingRule{
				{
					Name:    "prod-alerts",
					Equals:  []string{"production"},
					Channel: "C111222333",
				},
			},
		}
		require.NoError(t, server.apiSettings.InitAndValidate(&mockLogger{}))

		alert := &common.Alert{RouteKey: "production"}
		err := server.setSlackChannelID(nil, alert)

		require.NoError(t, err)
		assert.Equal(t, "C111222333", alert.SlackChannelID)
	})

	t.Run("uses fallback match-all rule when route key has no specific match", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServer(t)
		channelInfo.On("MapChannelNameToIDIfNeeded", mock.Anything).Return("")

		// Set up routing rules with a fallback
		server.apiSettings = &config.APISettings{
			RoutingRules: []*config.RoutingRule{
				{
					Name:     "fallback",
					MatchAll: true,
					Channel:  "C444555666",
				},
			},
		}
		require.NoError(t, server.apiSettings.InitAndValidate(&mockLogger{}))

		alert := &common.Alert{RouteKey: "unknown-key"}
		err := server.setSlackChannelID(nil, alert)

		require.NoError(t, err)
		assert.Equal(t, "C444555666", alert.SlackChannelID)
	})

	t.Run("returns error when no mapping exists for route key", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServer(t)
		channelInfo.On("MapChannelNameToIDIfNeeded", mock.Anything).Return("")

		// No routing rules
		server.apiSettings = &config.APISettings{}
		require.NoError(t, server.apiSettings.InitAndValidate(&mockLogger{}))

		alert := &common.Alert{RouteKey: "unmapped-key", Type: "test"}
		err := server.setSlackChannelID(nil, alert)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no mapping exists for route key")
	})

	t.Run("returns error when no route key and no fallback", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServer(t)
		channelInfo.On("MapChannelNameToIDIfNeeded", mock.Anything).Return("")

		// No routing rules
		server.apiSettings = &config.APISettings{}
		require.NoError(t, server.apiSettings.InitAndValidate(&mockLogger{}))

		alert := &common.Alert{} // No SlackChannelID, no RouteKey
		err := server.setSlackChannelID(nil, alert)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no route key")
	})

	t.Run("handles multiple alerts with different channel sources", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServer(t)
		channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
		channelInfo.On("MapChannelNameToIDIfNeeded", "C456").Return("C456")

		alerts := []*common.Alert{
			{SlackChannelID: "C123"},
			{SlackChannelID: "C456"},
		}
		err := server.setSlackChannelID(nil, alerts...)

		require.NoError(t, err)
		assert.Equal(t, "C123", alerts[0].SlackChannelID)
		assert.Equal(t, "C456", alerts[1].SlackChannelID)
	})
}

func TestServer_GetRateLimiter(t *testing.T) {
	t.Parallel()

	t.Run("creates new limiter for channel", func(t *testing.T) {
		t.Parallel()
		server, _, _ := newTestServer(t)
		limiter1 := server.getRateLimiter("C123")
		require.NotNil(t, limiter1)
	})

	t.Run("returns same limiter for same channel", func(t *testing.T) {
		t.Parallel()
		server, _, _ := newTestServer(t)
		limiter1 := server.getRateLimiter("C456")
		limiter2 := server.getRateLimiter("C456")
		assert.Same(t, limiter1, limiter2)
	})

	t.Run("returns different limiters for different channels", func(t *testing.T) {
		t.Parallel()
		server, _, _ := newTestServer(t)
		limiter1 := server.getRateLimiter("C789")
		limiter2 := server.getRateLimiter("C012")
		assert.NotSame(t, limiter1, limiter2)
	})
}

func TestServer_WaitForRateLimit(t *testing.T) {
	t.Parallel()

	t.Run("succeeds when rate limit allows", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		server.cfg.RateLimitPerAlertChannel.AlertsPerSecond = 100
		server.cfg.RateLimitPerAlertChannel.AllowedBurst = 100

		ctx := context.Background()
		err := server.waitForRateLimit(ctx, "C123", 5)

		require.NoError(t, err)
	})

	t.Run("returns ErrRateLimit when timeout exceeded", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		server.cfg.RateLimitPerAlertChannel.AlertsPerSecond = 0.001 // Very slow
		server.cfg.RateLimitPerAlertChannel.AllowedBurst = 1
		server.cfg.RateLimitPerAlertChannel.MaxRequestWaitTime = 100 * time.Millisecond

		// Exhaust the burst
		limiter := server.getRateLimiter("C_TIMEOUT")
		limiter.AllowN(time.Now(), 1)

		ctx := context.Background()
		err := server.waitForRateLimit(ctx, "C_TIMEOUT", 10)

		require.Error(t, err)
		require.ErrorIs(t, err, ErrRateLimit)
	})

	t.Run("returns ErrRateLimit when context is cancelled", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		server.cfg.RateLimitPerAlertChannel.AlertsPerSecond = 0.001 // Very slow
		server.cfg.RateLimitPerAlertChannel.AllowedBurst = 1
		server.cfg.RateLimitPerAlertChannel.MaxRequestWaitTime = 10 * time.Second

		// Exhaust the burst
		limiter := server.getRateLimiter("C_CANCEL")
		limiter.AllowN(time.Now(), 1)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		err := server.waitForRateLimit(ctx, "C_CANCEL", 10)

		require.Error(t, err)
		require.ErrorIs(t, err, ErrRateLimit)
	})
}

// --- Processing Error Tests ---

func TestProcessingError(t *testing.T) {
	t.Parallel()

	t.Run("retryable error", func(t *testing.T) {
		t.Parallel()

		err := newRetryableProcessingError("test error: %s", "details")
		assert.True(t, err.IsRetryable())
		assert.Equal(t, "test error: details", err.Error())
		assert.Error(t, err.Unwrap())
	})

	t.Run("non-retryable error", func(t *testing.T) {
		t.Parallel()

		err := newNonRetryableProcessingError("test error: %d", 42)
		assert.False(t, err.IsRetryable())
		assert.Equal(t, "test error: 42", err.Error())
		assert.Error(t, err.Unwrap())
	})
}

// --- Prometheus Webhook Tests ---

func TestServer_HandlePrometheusWebhook_Validation(t *testing.T) {
	t.Parallel()

	t.Run("returns 400 for empty body", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		router := gin.New()
		router.POST("/prometheus-alert", server.handlePrometheusWebhook)

		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert", nil)
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = 0
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertJSONError(t, w, "POST body")
	})

	t.Run("returns 400 for invalid JSON", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		router := gin.New()
		router.POST("/prometheus-alert", server.handlePrometheusWebhook)

		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert", bytes.NewReader([]byte("invalid json")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertJSONError(t, w, "decode")
	})

	t.Run("returns 400 for empty alerts array", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		router := gin.New()
		router.POST("/prometheus-alert", server.handlePrometheusWebhook)

		body := `{"alerts": []}`
		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert", bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertJSONError(t, w, "empty")
	})
}

func TestServer_HandlePrometheusWebhook_Success(t *testing.T) {
	t.Parallel()

	t.Run("accepts valid prometheus webhook and queues alerts", func(t *testing.T) {
		t.Parallel()

		server, queue, channelInfo := newTestServer(t)

		channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
		channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(&ChannelInfo{
			ChannelExists:      true,
			ManagerIsInChannel: true,
			UserCount:          10,
		}, nil)
		queue.On("Send", mock.Anything, "C123", mock.Anything, mock.Anything).Return(nil)

		router := gin.New()
		router.POST("/prometheus-alert", server.handlePrometheusWebhook)

		webhook := PrometheusWebhook{
			GroupKey: "test-group",
			Alerts: []*PrometheusAlert{
				{
					Status: "firing",
					Labels: map[string]string{
						"alertname": "TestAlert",
						"channel":   "C123",
					},
					Annotations: map[string]string{
						"summary": "Test alert summary",
					},
					StartsAt: time.Now(),
				},
			},
		}
		body, _ := json.Marshal(webhook)
		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusAccepted, w.Code)
		queue.AssertCalled(t, "Send", mock.Anything, "C123", mock.Anything, mock.Anything)
	})

	t.Run("maps resolved status to resolved severity", func(t *testing.T) {
		t.Parallel()

		server, queue, channelInfo := newTestServer(t)

		channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
		channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(&ChannelInfo{
			ChannelExists:      true,
			ManagerIsInChannel: true,
			UserCount:          10,
		}, nil)
		queue.On("Send", mock.Anything, "C123", mock.Anything, mock.MatchedBy(func(body string) bool {
			return strings.Contains(body, `"severity":"resolved"`)
		})).Return(nil)

		router := gin.New()
		router.POST("/prometheus-alert", server.handlePrometheusWebhook)

		webhook := PrometheusWebhook{
			Alerts: []*PrometheusAlert{
				{
					Status: "resolved",
					Labels: map[string]string{"channel": "C123"},
				},
			},
		}
		body, _ := json.Marshal(webhook)
		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusAccepted, w.Code)
	})
}

func TestMapPrometheusAlert(t *testing.T) {
	t.Parallel()

	server, _, _ := newTestServer(t)

	t.Run("maps basic alert fields", func(t *testing.T) {
		t.Parallel()

		webhook := &PrometheusWebhook{
			GroupKey: "test-group",
			Alerts: []*PrometheusAlert{
				{
					Status: "firing",
					Labels: map[string]string{
						"alertname": "TestAlert",
					},
					Annotations: map[string]string{
						"summary":     "Test summary",
						"description": "Test description",
					},
					StartsAt: time.Now(),
				},
			},
		}

		alerts := server.mapPrometheusAlert(webhook)

		require.Len(t, alerts, 1)
		assert.Equal(t, ":status: Test summary", alerts[0].Header)
		assert.Equal(t, "Test description", alerts[0].Text)
	})

	t.Run("uses channel from labels", func(t *testing.T) {
		t.Parallel()

		webhook := &PrometheusWebhook{
			Alerts: []*PrometheusAlert{
				{
					Labels: map[string]string{
						"slack_channel_id": "C999",
					},
				},
			},
		}

		alerts := server.mapPrometheusAlert(webhook)

		require.Len(t, alerts, 1)
		assert.Equal(t, "C999", alerts[0].SlackChannelID)
	})

	t.Run("maps severity correctly", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			severity string
			expected common.AlertSeverity
		}{
			{"critical", common.AlertError},
			{"error", common.AlertError},
			{"warning", common.AlertWarning},
			{"info", common.AlertInfo},
			{"", common.AlertError}, // default
		}

		for _, tt := range tests {
			webhook := &PrometheusWebhook{
				Alerts: []*PrometheusAlert{
					{
						Labels: map[string]string{"severity": tt.severity},
					},
				},
			}

			alerts := server.mapPrometheusAlert(webhook)
			assert.Equal(t, tt.expected, alerts[0].Severity, "severity: %s", tt.severity)
		}
	})

	t.Run("generates correlation ID from labels", func(t *testing.T) {
		t.Parallel()

		webhook := &PrometheusWebhook{
			Alerts: []*PrometheusAlert{
				{
					Labels: map[string]string{
						"namespace": "prod",
						"alertname": "HighCPU",
						"job":       "api-server",
					},
				},
			},
		}

		alerts := server.mapPrometheusAlert(webhook)

		require.Len(t, alerts, 1)
		assert.NotEmpty(t, alerts[0].CorrelationID)
	})

	t.Run("sets ignore if text contains", func(t *testing.T) {
		t.Parallel()

		webhook := &PrometheusWebhook{
			Alerts: []*PrometheusAlert{
				{
					Annotations: map[string]string{
						"ignoreIfTextContains": "test-ignore",
					},
				},
			},
		}

		alerts := server.mapPrometheusAlert(webhook)

		require.Len(t, alerts, 1)
		assert.Contains(t, alerts[0].IgnoreIfTextContains, "test-ignore")
	})
}

func TestCorrelationIDFromLabels(t *testing.T) {
	t.Parallel()

	t.Run("returns empty for nil labels", func(t *testing.T) {
		t.Parallel()
		result := correlationIDFromLabels(nil)
		assert.Empty(t, result)
	})

	t.Run("returns empty for empty labels", func(t *testing.T) {
		t.Parallel()
		result := correlationIDFromLabels(map[string]string{})
		assert.Empty(t, result)
	})

	t.Run("returns empty for labels without known keys", func(t *testing.T) {
		t.Parallel()
		result := correlationIDFromLabels(map[string]string{"unknown": "value"})
		assert.Empty(t, result)
	})

	t.Run("uses namespace and alertname", func(t *testing.T) {
		t.Parallel()
		labels := map[string]string{
			"namespace": "prod",
			"alertname": "HighCPU",
		}
		result := correlationIDFromLabels(labels)
		assert.NotEmpty(t, result)
	})

	t.Run("includes instance for monitoring namespace", func(t *testing.T) {
		t.Parallel()
		labels := map[string]string{
			"namespace": "monitoring",
			"alertname": "HighCPU",
			"instance":  "server-1",
		}
		result := correlationIDFromLabels(labels)
		assert.NotEmpty(t, result)
	})

	t.Run("same labels produce same correlation ID", func(t *testing.T) {
		t.Parallel()
		labels := map[string]string{
			"namespace": "prod",
			"alertname": "HighCPU",
			"job":       "api",
		}
		result1 := correlationIDFromLabels(labels)
		result2 := correlationIDFromLabels(labels)
		assert.Equal(t, result1, result2)
	})
}

func TestHelperFunctions(t *testing.T) {
	t.Parallel()

	t.Run("find returns first match from map1", func(t *testing.T) {
		t.Parallel()
		map1 := map[string]string{"key1": "value1"}
		map2 := map[string]string{"key2": "value2"}

		result := find(map1, map2, "key1", "key2")
		assert.Equal(t, "value1", result)
	})

	t.Run("find falls back to map2", func(t *testing.T) {
		t.Parallel()
		map1 := map[string]string{"other": "value1"}
		map2 := map[string]string{"key2": "value2"}

		result := find(map1, map2, "key2")
		assert.Equal(t, "value2", result)
	})

	t.Run("find returns empty for no match", func(t *testing.T) {
		t.Parallel()
		result := find(nil, nil, "missing")
		assert.Empty(t, result)
	})

	t.Run("find trims whitespace", func(t *testing.T) {
		t.Parallel()
		map1 := map[string]string{"key": "  value  "}

		result := find(map1, nil, "key")
		assert.Equal(t, "value", result)
	})

	t.Run("valueOrDefault returns value if not empty", func(t *testing.T) {
		t.Parallel()
		result := valueOrDefault("value", "default")
		assert.Equal(t, "value", result)
	})

	t.Run("valueOrDefault returns default if empty", func(t *testing.T) {
		t.Parallel()
		result := valueOrDefault("", "default")
		assert.Equal(t, "default", result)
	})

	t.Run("createLowerCaseKeys handles nil map", func(t *testing.T) {
		t.Parallel()
		// Should not panic
		createLowerCaseKeys(nil)
	})

	t.Run("createLowerCaseKeys adds lowercase keys", func(t *testing.T) {
		t.Parallel()
		m := map[string]string{"Key": "value", "UPPER": "data"}
		createLowerCaseKeys(m)

		assert.Equal(t, "value", m["key"])
		assert.Equal(t, "data", m["upper"])
		// Original keys should still exist
		assert.Equal(t, "value", m["Key"])
	})
}

func TestDebugGetAlertFields(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for nil alert", func(t *testing.T) {
		t.Parallel()
		result := debugGetAlertFields(nil)
		assert.Nil(t, result)
	})

	t.Run("returns fields from alert", func(t *testing.T) {
		t.Parallel()
		alert := &common.Alert{
			CorrelationID: "corr-123",
			Header:        "Test Header",
			Text:          "Test Body",
		}

		result := debugGetAlertFields(alert)

		assert.Equal(t, "corr-123", result["CorrelationId"])
		assert.Equal(t, "Test Header", result["Header"])
		assert.Equal(t, "Test Body", result["Body"])
	})

	t.Run("truncates long header", func(t *testing.T) {
		t.Parallel()
		longHeader := make([]byte, 300)
		for i := range longHeader {
			longHeader[i] = 'a'
		}

		alert := &common.Alert{Header: string(longHeader)}
		result := debugGetAlertFields(alert)

		assert.Len(t, result["Header"], 203) // 200 + "..."
		assert.LessOrEqual(t, len(result["Header"]), 203)
	})

	t.Run("truncates long body", func(t *testing.T) {
		t.Parallel()
		longBody := make([]byte, 1500)
		for i := range longBody {
			longBody[i] = 'b'
		}

		alert := &common.Alert{Text: string(longBody)}
		result := debugGetAlertFields(alert)

		assert.Len(t, result["Body"], 1003) // 1000 + "..."
	})
}

// --- Channel Info Syncer Tests ---

func newTestChannelInfoSyncer(slackClient SlackClient) *channelInfoSyncer {
	return newChannelInfoSyncer(slackClient, &mockLogger{})
}

func TestChannelInfoSyncer_Init(t *testing.T) {
	t.Parallel()

	t.Run("initializes with channel list", func(t *testing.T) {
		t.Parallel()

		slackClient := &mockSlackClient{}
		slackClient.On("ListBotChannels", mock.Anything).Return([]*internal.ChannelSummary{
			{ID: "C123", Name: "general"},
			{ID: "C456", Name: "random"},
		}, nil)

		syncer := newTestChannelInfoSyncer(slackClient)
		err := syncer.Init(context.Background())

		require.NoError(t, err)
		channels := syncer.ManagedChannels()
		assert.Len(t, channels, 2)
	})

	t.Run("returns error on slack API failure", func(t *testing.T) {
		t.Parallel()

		slackClient := &mockSlackClient{}
		slackClient.On("ListBotChannels", mock.Anything).Return(nil, errors.New("slack API error"))

		syncer := newTestChannelInfoSyncer(slackClient)
		err := syncer.Init(context.Background())

		require.Error(t, err)
	})
}

func TestChannelInfoSyncer_MapChannelNameToIDIfNeeded(t *testing.T) {
	t.Parallel()

	t.Run("returns empty for empty input", func(t *testing.T) {
		t.Parallel()

		slackClient := &mockSlackClient{}
		slackClient.On("ListBotChannels", mock.Anything).Return([]*internal.ChannelSummary{}, nil)

		syncer := newTestChannelInfoSyncer(slackClient)
		_ = syncer.Init(context.Background())

		result := syncer.MapChannelNameToIDIfNeeded("")
		assert.Empty(t, result)
	})

	t.Run("maps channel name to ID", func(t *testing.T) {
		t.Parallel()

		slackClient := &mockSlackClient{}
		slackClient.On("ListBotChannels", mock.Anything).Return([]*internal.ChannelSummary{
			{ID: "C123", Name: "general"},
		}, nil)

		syncer := newTestChannelInfoSyncer(slackClient)
		_ = syncer.Init(context.Background())

		result := syncer.MapChannelNameToIDIfNeeded("general")
		assert.Equal(t, "C123", result)
	})

	t.Run("maps channel name case-insensitively", func(t *testing.T) {
		t.Parallel()

		slackClient := &mockSlackClient{}
		slackClient.On("ListBotChannels", mock.Anything).Return([]*internal.ChannelSummary{
			{ID: "C123", Name: "General"},
		}, nil)

		syncer := newTestChannelInfoSyncer(slackClient)
		_ = syncer.Init(context.Background())

		result := syncer.MapChannelNameToIDIfNeeded("GENERAL")
		assert.Equal(t, "C123", result)
	})

	t.Run("returns original value if not found", func(t *testing.T) {
		t.Parallel()

		slackClient := &mockSlackClient{}
		slackClient.On("ListBotChannels", mock.Anything).Return([]*internal.ChannelSummary{}, nil)

		syncer := newTestChannelInfoSyncer(slackClient)
		_ = syncer.Init(context.Background())

		result := syncer.MapChannelNameToIDIfNeeded("C999")
		assert.Equal(t, "C999", result)
	})
}

func TestChannelInfoSyncer_GetChannelInfo(t *testing.T) {
	t.Parallel()

	t.Run("fetches channel info from Slack", func(t *testing.T) {
		t.Parallel()

		slackClient := &mockSlackClient{}
		slackClient.On("ListBotChannels", mock.Anything).Return([]*internal.ChannelSummary{}, nil)
		slackClient.On("GetChannelInfo", mock.Anything, "C123").Return(&slack.Channel{
			GroupConversation: slack.GroupConversation{
				Conversation: slack.Conversation{
					ID: "C123",
				},
			},
		}, nil)
		slackClient.On("GetUserIDsInChannel", mock.Anything, "C123").Return(map[string]struct{}{
			"U001": {},
			"U002": {},
		}, nil)
		slackClient.On("BotIsInChannel", mock.Anything, "C123").Return(true, nil)

		syncer := newTestChannelInfoSyncer(slackClient)
		_ = syncer.Init(context.Background())

		info, err := syncer.GetChannelInfo(context.Background(), "C123")

		require.NoError(t, err)
		assert.True(t, info.ChannelExists)
		assert.True(t, info.ManagerIsInChannel)
		assert.Equal(t, 2, info.UserCount)
	})

	t.Run("returns not found for non-existent channel", func(t *testing.T) {
		t.Parallel()

		slackClient := &mockSlackClient{}
		slackClient.On("ListBotChannels", mock.Anything).Return([]*internal.ChannelSummary{}, nil)
		slackClient.On("GetChannelInfo", mock.Anything, "C999").Return(nil, errors.New(internal.SlackChannelNotFoundError))

		syncer := newTestChannelInfoSyncer(slackClient)
		_ = syncer.Init(context.Background())

		info, err := syncer.GetChannelInfo(context.Background(), "C999")

		require.NoError(t, err)
		assert.False(t, info.ChannelExists)
	})

	t.Run("returns error on Slack API failure", func(t *testing.T) {
		t.Parallel()

		slackClient := &mockSlackClient{}
		slackClient.On("ListBotChannels", mock.Anything).Return([]*internal.ChannelSummary{}, nil)
		slackClient.On("GetChannelInfo", mock.Anything, "C123").Return(nil, errors.New("network error"))

		syncer := newTestChannelInfoSyncer(slackClient)
		_ = syncer.Init(context.Background())

		_, err := syncer.GetChannelInfo(context.Background(), "C123")

		require.Error(t, err)
	})

	t.Run("caches channel info", func(t *testing.T) {
		t.Parallel()

		slackClient := &mockSlackClient{}
		slackClient.On("ListBotChannels", mock.Anything).Return([]*internal.ChannelSummary{}, nil)
		slackClient.On("GetChannelInfo", mock.Anything, "C123").Return(&slack.Channel{}, nil).Once()
		slackClient.On("GetUserIDsInChannel", mock.Anything, "C123").Return(map[string]struct{}{}, nil).Once()
		slackClient.On("BotIsInChannel", mock.Anything, "C123").Return(true, nil).Once()

		syncer := newTestChannelInfoSyncer(slackClient)
		_ = syncer.Init(context.Background())

		// First call - fetches from Slack
		info1, err := syncer.GetChannelInfo(context.Background(), "C123")
		require.NoError(t, err)

		// Second call - should use cache
		info2, err := syncer.GetChannelInfo(context.Background(), "C123")
		require.NoError(t, err)

		assert.Equal(t, info1, info2)
		// Verify Slack API was only called once
		slackClient.AssertNumberOfCalls(t, "GetChannelInfo", 1)
	})

	t.Run("detects archived channel", func(t *testing.T) {
		t.Parallel()

		slackClient := &mockSlackClient{}
		slackClient.On("ListBotChannels", mock.Anything).Return([]*internal.ChannelSummary{}, nil)
		slackClient.On("GetChannelInfo", mock.Anything, "C123").Return(&slack.Channel{
			GroupConversation: slack.GroupConversation{
				IsArchived: true,
			},
		}, nil)
		slackClient.On("GetUserIDsInChannel", mock.Anything, "C123").Return(map[string]struct{}{}, nil)
		slackClient.On("BotIsInChannel", mock.Anything, "C123").Return(true, nil)

		syncer := newTestChannelInfoSyncer(slackClient)
		_ = syncer.Init(context.Background())

		info, err := syncer.GetChannelInfo(context.Background(), "C123")

		require.NoError(t, err)
		assert.True(t, info.ChannelIsArchived)
	})
}

func TestChannelInfoSyncer_ManagedChannels(t *testing.T) {
	t.Parallel()

	t.Run("returns nil before init", func(t *testing.T) {
		t.Parallel()

		slackClient := &mockSlackClient{}
		syncer := newTestChannelInfoSyncer(slackClient)

		channels := syncer.ManagedChannels()
		assert.Nil(t, channels)
	})

	t.Run("returns channels after init", func(t *testing.T) {
		t.Parallel()

		slackClient := &mockSlackClient{}
		slackClient.On("ListBotChannels", mock.Anything).Return([]*internal.ChannelSummary{
			{ID: "C123", Name: "general"},
			{ID: "C456", Name: "random"},
		}, nil)

		syncer := newTestChannelInfoSyncer(slackClient)
		_ = syncer.Init(context.Background())

		channels := syncer.ManagedChannels()
		assert.Len(t, channels, 2)
	})
}

// --- Additional Server Tests ---

func TestServer_GetHandlerTimeout(t *testing.T) {
	t.Parallel()

	t.Run("calculates timeout from MaxRequestWaitTime plus buffer", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		server.cfg.RateLimitPerAlertChannel.MaxRequestWaitTime = 60 * time.Second

		timeout := server.getHandlerTimeout()

		// 60 + 5 = 65 seconds
		assert.Equal(t, 65*time.Second, timeout)
	})

	t.Run("uses minimum of 30 seconds", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		server.cfg.RateLimitPerAlertChannel.MaxRequestWaitTime = 10 * time.Second

		timeout := server.getHandlerTimeout()

		// 10 + 5 = 15, but minimum is 30
		assert.Equal(t, 30*time.Second, timeout)
	})
}

func TestServer_CreateClientErrorAlert(t *testing.T) {
	t.Parallel()

	t.Run("creates warning alert for 4xx errors", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		server.cfg.ErrorReportChannelID = "C-errors"

		alert := server.createClientErrorAlert(
			errors.New("bad request"),
			400,
			nil,
			"C123",
		)

		assert.Equal(t, common.AlertWarning, alert.Severity)
		assert.Equal(t, "C-errors", alert.SlackChannelID)
		assert.Contains(t, alert.Header, "400")
	})

	t.Run("creates error alert for 5xx errors", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		server.cfg.ErrorReportChannelID = "C-errors"

		alert := server.createClientErrorAlert(
			errors.New("internal error"),
			500,
			nil,
			"",
		)

		assert.Equal(t, common.AlertError, alert.Severity)
		assert.Contains(t, alert.Text, "N/A") // empty target channel
	})

	t.Run("includes debug fields", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		server.cfg.ErrorReportChannelID = "C-errors"

		debugFields := map[string]string{
			"CorrelationId": "corr-123",
			"Header":        "Test Header",
		}

		alert := server.createClientErrorAlert(
			errors.New("error"),
			400,
			debugFields,
			"C123",
		)

		assert.Contains(t, alert.Text, "corr-123")
		assert.Contains(t, alert.Text, "Test Header")
	})

	t.Run("sanitizes debug field values", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		server.cfg.ErrorReportChannelID = "C-errors"

		debugFields := map[string]string{
			"Header": ":status: *bold* `code` <link>",
		}

		alert := server.createClientErrorAlert(
			errors.New("error"),
			400,
			debugFields,
			"C123",
		)

		// Markdown chars should be removed
		assert.NotContains(t, alert.Text, ":status:")
		assert.NotContains(t, alert.Text, "*bold*")
		assert.NotContains(t, alert.Text, "`code`")
	})

	t.Run("truncates long debug field values", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		server.cfg.ErrorReportChannelID = "C-errors"

		longValue := make([]byte, 200)
		for i := range longValue {
			longValue[i] = 'x'
		}

		debugFields := map[string]string{
			"LongField": string(longValue),
		}

		alert := server.createClientErrorAlert(
			errors.New("error"),
			400,
			debugFields,
			"C123",
		)

		// Value should be truncated to 100 chars + "..."
		assert.Contains(t, alert.Text, "...")
	})
}

func TestServer_QueueAlert(t *testing.T) {
	t.Parallel()

	t.Run("queues alert successfully", func(t *testing.T) {
		t.Parallel()

		server, queue, _ := newTestServer(t)
		queue.On("Send", mock.Anything, "C123", mock.Anything, mock.Anything).Return(nil)

		alert := &common.Alert{
			SlackChannelID: "C123",
			Header:         "Test",
		}

		err := server.queueAlert(context.Background(), alert)

		require.NoError(t, err)
		queue.AssertCalled(t, "Send", mock.Anything, "C123", mock.Anything, mock.Anything)
	})

	t.Run("returns error on queue failure", func(t *testing.T) {
		t.Parallel()

		server, queue, _ := newTestServer(t)
		queue.On("Send", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("queue error"))

		alert := &common.Alert{
			SlackChannelID: "C123",
			Header:         "Test",
		}

		err := server.queueAlert(context.Background(), alert)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "queue")
	})
}

func TestServer_UpdateSettings(t *testing.T) {
	t.Parallel()

	t.Run("updates settings successfully", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)

		newSettings := &config.APISettings{
			RoutingRules: []*config.RoutingRule{
				{Name: "new-rule", Equals: []string{"test-key"}, Channel: "C123ABC456"},
			},
		}

		err := server.UpdateSettings(newSettings)

		require.NoError(t, err)
		assert.Equal(t, newSettings, server.apiSettings)
	})

	t.Run("handles nil settings", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)

		err := server.UpdateSettings(nil)

		require.NoError(t, err)
		assert.NotNil(t, server.apiSettings)
	})
}

func TestIgnoreAlert(t *testing.T) {
	t.Parallel()

	t.Run("returns false for nil alert", func(t *testing.T) {
		t.Parallel()

		ignore, _ := ignoreAlert(nil)
		assert.False(t, ignore)
	})

	t.Run("returns false when no ignore terms", func(t *testing.T) {
		t.Parallel()

		alert := &common.Alert{
			Text: "Some error message",
		}

		ignore, _ := ignoreAlert(alert)
		assert.False(t, ignore)
	})

	t.Run("returns false when text is empty", func(t *testing.T) {
		t.Parallel()

		alert := &common.Alert{
			IgnoreIfTextContains: []string{"error"},
			Text:                 "",
		}

		ignore, _ := ignoreAlert(alert)
		assert.False(t, ignore)
	})

	t.Run("returns true when ignore term found", func(t *testing.T) {
		t.Parallel()

		alert := &common.Alert{
			IgnoreIfTextContains: []string{"ignore-this"},
			Text:                 "This message contains ignore-this text",
		}

		ignore, reason := ignoreAlert(alert)
		assert.True(t, ignore)
		assert.NotEmpty(t, reason)
	})

	t.Run("case insensitive matching", func(t *testing.T) {
		t.Parallel()

		alert := &common.Alert{
			IgnoreIfTextContains: []string{"ERROR"},
			Text:                 "This has an error in it",
		}

		ignore, _ := ignoreAlert(alert)
		assert.True(t, ignore)
	})

	t.Run("skips empty ignore terms", func(t *testing.T) {
		t.Parallel()

		alert := &common.Alert{
			IgnoreIfTextContains: []string{"", "  ", "specific-term"},
			Text:                 "Text without that term",
		}

		ignore, _ := ignoreAlert(alert)
		assert.False(t, ignore)
	})
}

// --- Raw Alert Consumer Tests ---

type mockFifoQueueConsumer struct {
	mock.Mock

	items chan *common.FifoQueueItem
}

func newMockFifoQueueConsumer() *mockFifoQueueConsumer {
	return &mockFifoQueueConsumer{
		items: make(chan *common.FifoQueueItem, 10),
	}
}

func (m *mockFifoQueueConsumer) Receive(ctx context.Context, sinkCh chan<- *common.FifoQueueItem) error {
	args := m.Called(ctx, sinkCh)

	// Forward items from internal channel to sink channel
	for {
		select {
		case <-ctx.Done():
			close(sinkCh)
			return ctx.Err()
		case item, ok := <-m.items:
			if !ok {
				close(sinkCh)
				return args.Error(0)
			}
			sinkCh <- item
		}
	}
}

func (m *mockFifoQueueConsumer) SendItem(item *common.FifoQueueItem) {
	m.items <- item
}

func (m *mockFifoQueueConsumer) Close() {
	close(m.items)
}

func newTestServerWithInitializedSettings(t *testing.T) (*Server, *mockFifoQueueProducer, *mockChannelInfoProvider) {
	t.Helper()

	server, queue, channelInfo := newTestServer(t)
	// Initialize settings to set up the internal matchCache
	_ = server.apiSettings.InitAndValidate(&mockLogger{})
	return server, queue, channelInfo
}

func TestServer_RunRawAlertConsumer(t *testing.T) {
	t.Parallel()

	t.Run("acks message on successful processing", func(t *testing.T) {
		t.Parallel()

		server, queue, channelInfo := newTestServerWithInitializedSettings(t)

		channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
		channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(&ChannelInfo{
			ChannelExists:      true,
			ManagerIsInChannel: true,
			UserCount:          10,
		}, nil)
		queue.On("Send", mock.Anything, "C123", mock.Anything, mock.Anything).Return(nil)

		consumer := newMockFifoQueueConsumer()
		consumer.On("Receive", mock.Anything, mock.Anything).Return(nil)

		ackCalled := false
		nackCalled := false

		alert := common.Alert{
			SlackChannelID: "C123",
			Header:         "Test Alert",
		}
		alertJSON, _ := json.Marshal(alert)

		item := &common.FifoQueueItem{
			MessageID:      "msg-1",
			SlackChannelID: "C123",
			Body:           string(alertJSON),
			Ack:            func() { ackCalled = true },
			Nack:           func() { nackCalled = true },
		}

		ctx, cancel := context.WithCancel(context.Background())

		go func() {
			consumer.SendItem(item)
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		_ = server.runRawAlertConsumer(ctx, consumer)

		assert.True(t, ackCalled, "ack should be called on successful processing")
		assert.False(t, nackCalled, "nack should not be called on successful processing")
	})

	t.Run("acks message on invalid JSON", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServerWithInitializedSettings(t)

		consumer := newMockFifoQueueConsumer()
		consumer.On("Receive", mock.Anything, mock.Anything).Return(nil)

		ackCalled := false
		nackCalled := false

		item := &common.FifoQueueItem{
			MessageID:      "msg-1",
			SlackChannelID: "C123",
			Body:           "invalid json",
			Ack:            func() { ackCalled = true },
			Nack:           func() { nackCalled = true },
		}

		ctx, cancel := context.WithCancel(context.Background())

		go func() {
			consumer.SendItem(item)
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		_ = server.runRawAlertConsumer(ctx, consumer)

		assert.True(t, ackCalled, "ack should be called on invalid JSON to avoid reprocessing")
		assert.False(t, nackCalled, "nack should not be called on invalid JSON")
	})

	t.Run("nacks message on retryable processing error", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServerWithInitializedSettings(t)

		channelInfo.On("MapChannelNameToIDIfNeeded", "C123").Return("C123")
		// Return an error from GetChannelInfo - this causes a retryable error
		channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(nil, errors.New("transient error"))

		consumer := newMockFifoQueueConsumer()
		consumer.On("Receive", mock.Anything, mock.Anything).Return(nil)

		ackCalled := false
		nackCalled := false

		alert := common.Alert{
			SlackChannelID: "C123",
			Header:         "Test Alert",
		}
		alertJSON, _ := json.Marshal(alert)

		item := &common.FifoQueueItem{
			MessageID:      "msg-1",
			SlackChannelID: "C123",
			Body:           string(alertJSON),
			Ack:            func() { ackCalled = true },
			Nack:           func() { nackCalled = true },
		}

		ctx, cancel := context.WithCancel(context.Background())

		go func() {
			consumer.SendItem(item)
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		_ = server.runRawAlertConsumer(ctx, consumer)

		assert.False(t, ackCalled, "ack should not be called on retryable error")
		assert.True(t, nackCalled, "nack should be called on retryable error")
	})

	t.Run("acks message on non-retryable processing error", func(t *testing.T) {
		t.Parallel()

		server, _, channelInfo := newTestServerWithInitializedSettings(t)

		// No channel ID and no route key match - this causes a non-retryable error
		channelInfo.On("MapChannelNameToIDIfNeeded", "").Return("")

		consumer := newMockFifoQueueConsumer()
		consumer.On("Receive", mock.Anything, mock.Anything).Return(nil)

		ackCalled := false
		nackCalled := false

		// Alert without SlackChannelID - will fail validation (non-retryable)
		alert := common.Alert{
			Header: "Test Alert",
		}
		alertJSON, _ := json.Marshal(alert)

		item := &common.FifoQueueItem{
			MessageID:      "msg-1",
			SlackChannelID: "",
			Body:           string(alertJSON),
			Ack:            func() { ackCalled = true },
			Nack:           func() { nackCalled = true },
		}

		ctx, cancel := context.WithCancel(context.Background())

		go func() {
			consumer.SendItem(item)
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		_ = server.runRawAlertConsumer(ctx, consumer)

		assert.True(t, ackCalled, "ack should be called on non-retryable error to avoid reprocessing")
		assert.False(t, nackCalled, "nack should not be called on non-retryable error")
	})

	t.Run("handles nil ack and nack functions", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServerWithInitializedSettings(t)

		consumer := newMockFifoQueueConsumer()
		consumer.On("Receive", mock.Anything, mock.Anything).Return(nil)

		item := &common.FifoQueueItem{
			MessageID:      "msg-1",
			SlackChannelID: "C123",
			Body:           "invalid json",
			Ack:            nil,
			Nack:           nil,
		}

		ctx, cancel := context.WithCancel(context.Background())

		go func() {
			consumer.SendItem(item)
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		// Should not panic when Ack/Nack are nil
		err := server.runRawAlertConsumer(ctx, consumer)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("processes multiple messages in order", func(t *testing.T) {
		t.Parallel()

		server, queue, channelInfo := newTestServerWithInitializedSettings(t)

		channelInfo.On("MapChannelNameToIDIfNeeded", mock.Anything).Return("C123")
		channelInfo.On("GetChannelInfo", mock.Anything, "C123").Return(&ChannelInfo{
			ChannelExists:      true,
			ManagerIsInChannel: true,
			UserCount:          10,
		}, nil)
		queue.On("Send", mock.Anything, "C123", mock.Anything, mock.Anything).Return(nil)

		consumer := newMockFifoQueueConsumer()
		consumer.On("Receive", mock.Anything, mock.Anything).Return(nil)

		ackCount := 0

		ctx, cancel := context.WithCancel(context.Background())

		go func() {
			for i := range 3 {
				alert := common.Alert{
					SlackChannelID: "C123",
					Header:         "Test Alert",
				}
				alertJSON, _ := json.Marshal(alert)

				item := &common.FifoQueueItem{
					MessageID:      "msg-" + string(rune('1'+i)),
					SlackChannelID: "C123",
					Body:           string(alertJSON),
					Ack:            func() { ackCount++ },
					Nack:           nil,
				}
				consumer.SendItem(item)
			}
			time.Sleep(100 * time.Millisecond)
			cancel()
		}()

		_ = server.runRawAlertConsumer(ctx, consumer)

		assert.Equal(t, 3, ackCount, "all messages should be acked")
	})

	t.Run("returns error from consumer", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServerWithInitializedSettings(t)

		consumer := newMockFifoQueueConsumer()
		expectedErr := errors.New("consumer error")
		consumer.On("Receive", mock.Anything, mock.Anything).Return(expectedErr)

		go func() {
			time.Sleep(10 * time.Millisecond)
			consumer.Close()
		}()

		err := server.runRawAlertConsumer(context.Background(), consumer)

		assert.ErrorIs(t, err, expectedErr)
	})
}
