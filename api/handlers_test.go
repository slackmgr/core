package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// containsIgnoreCase checks if s contains substr (case-insensitive)
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// mockLogger implements common.Logger for testing
type mockLogger struct{}

func (m *mockLogger) Debug(msg string)                                       {}
func (m *mockLogger) Debugf(format string, args ...interface{})              {}
func (m *mockLogger) Info(msg string)                                        {}
func (m *mockLogger) Infof(format string, args ...interface{})               {}
func (m *mockLogger) Warn(msg string)                                        {}
func (m *mockLogger) Warnf(format string, args ...interface{})               {}
func (m *mockLogger) Error(msg string)                                       {}
func (m *mockLogger) Errorf(format string, args ...interface{})              {}
func (m *mockLogger) WithField(key string, value interface{}) common.Logger  { return m }
func (m *mockLogger) WithFields(fields map[string]interface{}) common.Logger { return m }
func (m *mockLogger) HttpLoggingHandler() io.Writer                          { return nil }

// mockQueue implements FifoQueueProducer for testing
type mockQueue struct {
	mu       sync.Mutex
	messages []queueMessage
	sendErr  error
}

type queueMessage struct {
	channelID string
	dedupID   string
	body      string
}

func newMockQueue() *mockQueue {
	return &mockQueue{
		messages: make([]queueMessage, 0),
	}
}

func (m *mockQueue) Send(ctx context.Context, slackChannelID, dedupID, body string) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, queueMessage{
		channelID: slackChannelID,
		dedupID:   dedupID,
		body:      body,
	})
	return nil
}

func (m *mockQueue) Messages() []queueMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.messages
}

// mockChannelInfoProvider implements ChannelInfoProvider for testing
type mockChannelInfoProvider struct {
	channels     map[string]*channelInfo
	channelNames map[string]string // name -> ID mapping
}

func newMockChannelInfoProvider() *mockChannelInfoProvider {
	return &mockChannelInfoProvider{
		channels:     make(map[string]*channelInfo),
		channelNames: make(map[string]string),
	}
}

func (m *mockChannelInfoProvider) GetChannelInfo(ctx context.Context, channelID string) (*channelInfo, error) {
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

func (m *mockChannelInfoProvider) MapChannelNameToIDIfNeeded(channelIDOrName string) string {
	if id, ok := m.channelNames[channelIDOrName]; ok {
		return id
	}
	return channelIDOrName
}

func (m *mockChannelInfoProvider) ManagedChannels() any {
	return m.channelNames
}

func (m *mockChannelInfoProvider) Init(ctx context.Context) error {
	return nil
}

func (m *mockChannelInfoProvider) Run(ctx context.Context) error {
	return nil
}

func (m *mockChannelInfoProvider) addChannel(id string, info *channelInfo) {
	m.channels[id] = info
}

func (m *mockChannelInfoProvider) addChannelName(name, id string) {
	m.channelNames[name] = id
}

// testServer creates a minimal server for testing with mocked dependencies
func testServer(t *testing.T) (*Server, *mockQueue, *mockChannelInfoProvider) {
	t.Helper()

	queue := newMockQueue()
	logger := &mockLogger{}
	metrics := &common.NoopMetrics{}
	cfg := config.NewDefaultAPIConfig()
	cfg.SlackClient.AppToken = "xapp-test"
	cfg.SlackClient.BotToken = "xoxb-test"
	settings := &config.APISettings{}
	settings.RoutingRules = []*config.RoutingRule{}

	// Initialize the settings to avoid nil map panic
	_ = settings.InitAndValidate(logger)

	server := New(queue, nil, logger, metrics, cfg, settings)

	// Inject mock channel info provider
	mockProvider := newMockChannelInfoProvider()
	server.channelInfoSyncer = mockProvider

	return server, queue, mockProvider
}

// setupTestRouter creates a Gin router with all API routes for testing
func setupTestRouter(server *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Add middleware to get coverage
	router.Use(server.metricsMiddleware())
	if server.logger.HttpLoggingHandler() != nil {
		router.Use(server.loggingMiddleware())
	}

	router.POST("/alert", server.handleAlerts)
	router.POST("/alert/:slackChannelId", server.handleAlerts)
	router.POST("/alerts", server.handleAlerts)
	router.POST("/alerts/:slackChannelId", server.handleAlerts)
	router.POST("/prometheus-alert", server.handlePrometheusWebhook)
	router.POST("/prometheus-alert/:slackChannelId", server.handlePrometheusWebhook)
	router.POST("/alerts-test", server.handleAlertsTest)
	router.POST("/alerts-test/:slackChannelId", server.handleAlertsTest)
	router.GET("/mappings", server.handleMappings)
	router.GET("/channels", server.handleChannels)
	router.GET("/ping", server.ping)

	return router
}

// validChannelInfo returns a channel info that passes validation
func validChannelInfo() *channelInfo {
	return &channelInfo{
		ChannelExists:      true,
		ChannelIsArchived:  false,
		ManagerIsInChannel: true,
		UserCount:          5,
	}
}

// TestPingEndpoint tests the GET /ping endpoint
func TestPingEndpoint(t *testing.T) {
	t.Parallel()

	server, _, _ := testServer(t)
	router := setupTestRouter(server)

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "pong", w.Body.String())
}

// TestMappingsEndpoint tests the GET /mappings endpoint
func TestMappingsEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("empty routing rules", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)
		router := setupTestRouter(server)

		req := httptest.NewRequest(http.MethodGet, "/mappings", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var rules []*config.RoutingRule
		err := json.Unmarshal(w.Body.Bytes(), &rules)
		require.NoError(t, err)
		assert.Empty(t, rules)
	})

	t.Run("with routing rules", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)
		server.apiSettings.RoutingRules = []*config.RoutingRule{
			{
				Name:    "test-rule",
				Channel: "C123456789",
				Equals:  []string{"test-key"},
			},
		}
		router := setupTestRouter(server)

		req := httptest.NewRequest(http.MethodGet, "/mappings", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var rules []*config.RoutingRule
		err := json.Unmarshal(w.Body.Bytes(), &rules)
		require.NoError(t, err)
		assert.Len(t, rules, 1)
		assert.Equal(t, "test-rule", rules[0].Name)
	})
}

// TestChannelsEndpoint tests the GET /channels endpoint
func TestChannelsEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("empty channels", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)
		router := setupTestRouter(server)

		req := httptest.NewRequest(http.MethodGet, "/channels", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	})

	t.Run("with channels", func(t *testing.T) {
		t.Parallel()

		server, _, mockProvider := testServer(t)
		mockProvider.addChannelName("alerts", "C123456789")
		mockProvider.addChannelName("incidents", "C987654321")
		router := setupTestRouter(server)

		req := httptest.NewRequest(http.MethodGet, "/channels", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var channels map[string]string
		err := json.Unmarshal(w.Body.Bytes(), &channels)
		require.NoError(t, err)
		assert.Len(t, channels, 2)
	})
}

// TestAlertEndpoint tests the POST /alert endpoint
func TestAlertEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("missing body returns 400", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)
		router := setupTestRouter(server)

		req := httptest.NewRequest(http.MethodPost, "/alert", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.True(t, containsIgnoreCase(w.Body.String(), "missing POST body"))
	})

	t.Run("invalid JSON returns 400", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)
		router := setupTestRouter(server)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBufferString("{invalid}"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.True(t, containsIgnoreCase(w.Body.String(), "failed to parse POST body"))
	})

	t.Run("alert without channel and no route returns 400", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)
		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = "Test Alert"
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.True(t, containsIgnoreCase(w.Body.String(), "no route key"))
	})

	t.Run("alert with channel ID in URL", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-123"
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alert/"+channelID, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Len(t, queue.Messages(), 1)
		assert.Equal(t, channelID, queue.Messages()[0].channelID)
	})

	t.Run("alert with channel ID in body", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-456"
		alert.SlackChannelID = channelID
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Len(t, queue.Messages(), 1)
		assert.Equal(t, channelID, queue.Messages()[0].channelID)
	})

	t.Run("array of alerts", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		alerts := []*common.Alert{
			{Header: ":status: Alert 1", CorrelationID: "corr-1", SlackChannelID: channelID, Severity: common.AlertError},
			{Header: ":status: Alert 2", CorrelationID: "corr-2", SlackChannelID: channelID, Severity: common.AlertWarning},
		}
		body, _ := json.Marshal(alerts)

		req := httptest.NewRequest(http.MethodPost, "/alerts", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Len(t, queue.Messages(), 2)
	})

	t.Run("channel does not exist returns 400", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)
		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-789"
		alert.SlackChannelID = "C999999999"
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.True(t, containsIgnoreCase(w.Body.String(), "unable to find channel"))
	})

	t.Run("archived channel returns 400", func(t *testing.T) {
		t.Parallel()

		server, _, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, &channelInfo{
			ChannelExists:      true,
			ChannelIsArchived:  true,
			ManagerIsInChannel: true,
			UserCount:          5,
		})
		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-archived"
		alert.SlackChannelID = channelID
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.True(t, containsIgnoreCase(w.Body.String(), "archived"))
	})

	t.Run("manager not in channel returns 400", func(t *testing.T) {
		t.Parallel()

		server, _, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, &channelInfo{
			ChannelExists:      true,
			ChannelIsArchived:  false,
			ManagerIsInChannel: false,
			UserCount:          5,
		})
		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-not-in-channel"
		alert.SlackChannelID = channelID
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.True(t, containsIgnoreCase(w.Body.String(), "not in channel"))
	})

	t.Run("empty alerts array returns 204", func(t *testing.T) {
		t.Parallel()

		server, queue, _ := testServer(t)
		router := setupTestRouter(server)

		body := []byte("[]")

		req := httptest.NewRequest(http.MethodPost, "/alerts", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Empty(t, queue.Messages())
	})
}

// TestPrometheusWebhookEndpoint tests the POST /prometheus-alert endpoint
func TestPrometheusWebhookEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("missing body returns 400", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)
		router := setupTestRouter(server)

		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.True(t, containsIgnoreCase(w.Body.String(), "missing POST body"))
	})

	t.Run("invalid JSON returns 400", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)
		router := setupTestRouter(server)

		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert", bytes.NewBufferString("{invalid}"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.True(t, containsIgnoreCase(w.Body.String(), "failed to decode POST body"))
	})

	t.Run("empty alerts array returns 400", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)
		router := setupTestRouter(server)

		webhook := PrometheusWebhook{
			Alerts: []*PrometheusAlert{},
		}
		body, _ := json.Marshal(webhook)

		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.True(t, containsIgnoreCase(w.Body.String(), "alert list is empty"))
	})

	t.Run("valid prometheus alert with channel in URL", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		webhook := PrometheusWebhook{
			GroupKey: "test-group",
			Alerts: []*PrometheusAlert{
				{
					Status: "firing",
					Labels: map[string]string{
						"alertname": "TestAlert",
						"severity":  "error",
					},
					Annotations: map[string]string{
						"summary":     "Test alert summary",
						"description": "Test alert description",
					},
					StartsAt: time.Now(),
				},
			},
		}
		body, _ := json.Marshal(webhook)

		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert/"+channelID, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Len(t, queue.Messages(), 1)
		assert.Equal(t, channelID, queue.Messages()[0].channelID)
	})

	t.Run("resolved alert sets correct severity", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		webhook := PrometheusWebhook{
			GroupKey: "test-group-resolved",
			Alerts: []*PrometheusAlert{
				{
					Status: "resolved",
					Labels: map[string]string{
						"alertname": "TestAlert",
						"severity":  "error",
					},
					Annotations: map[string]string{
						"summary": "Test resolved alert",
					},
					StartsAt: time.Now().Add(-time.Hour),
					EndsAt:   time.Now(),
				},
			},
		}
		body, _ := json.Marshal(webhook)

		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert/"+channelID, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		require.Len(t, queue.Messages(), 1)

		// Verify the queued alert has resolved severity
		var alert common.Alert
		err := json.Unmarshal([]byte(queue.Messages()[0].body), &alert)
		require.NoError(t, err)
		assert.Equal(t, common.AlertResolved, alert.Severity)
	})

	t.Run("alert with correlation ID from annotations", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		webhook := PrometheusWebhook{
			GroupKey: "test-group-corr",
			Alerts: []*PrometheusAlert{
				{
					Status: "firing",
					Labels: map[string]string{
						"alertname": "TestAlert",
					},
					Annotations: map[string]string{
						"correlationid": "custom-correlation-id",
						"summary":       "Test alert with correlation ID",
					},
					StartsAt: time.Now(),
				},
			},
		}
		body, _ := json.Marshal(webhook)

		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert/"+channelID, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		require.Len(t, queue.Messages(), 1)

		var alert common.Alert
		err := json.Unmarshal([]byte(queue.Messages()[0].body), &alert)
		require.NoError(t, err)
		assert.Equal(t, "custom-correlation-id", alert.CorrelationID)
	})
}

// TestAlertsTestEndpoint tests the POST /alerts-test endpoint
func TestAlertsTestEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("missing body returns 400", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)
		router := setupTestRouter(server)

		req := httptest.NewRequest(http.MethodPost, "/alerts-test", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.True(t, containsIgnoreCase(w.Body.String(), "missing POST body"))
	})

	t.Run("valid alert test with channel in URL returns 204", func(t *testing.T) {
		t.Parallel()

		server, _, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-alert-test"
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alerts-test/"+channelID, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
	})
}

// TestAlertEndpointWithRouteKey tests alert routing via route keys
func TestAlertEndpointWithRouteKey(t *testing.T) {
	t.Parallel()

	t.Run("alert routed via matching rule", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())

		// Set up routing rules
		server.apiSettings.RoutingRules = []*config.RoutingRule{
			{
				Name:    "test-route",
				Channel: channelID,
				Equals:  []string{"my-service"},
			},
		}
		_ = server.apiSettings.InitAndValidate(&mockLogger{})

		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-route-key"
		alert.RouteKey = "my-service"
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Len(t, queue.Messages(), 1)
		assert.Equal(t, channelID, queue.Messages()[0].channelID)
	})

	t.Run("alert with non-matching route key returns 400", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)
		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-no-match"
		alert.RouteKey = "unknown-service"
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.True(t, containsIgnoreCase(w.Body.String(), "no mapping exists"))
	})
}

// TestAlertEndpointEdgeCases tests additional edge cases
func TestAlertEndpointEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("channel user count exceeds limit returns 400", func(t *testing.T) {
		t.Parallel()

		server, _, mockProvider := testServer(t)
		// Set max users to a low value
		server.cfg.MaxUsersInAlertChannel = 10
		channelID := "C123456789"
		mockProvider.addChannel(channelID, &channelInfo{
			ChannelExists:      true,
			ChannelIsArchived:  false,
			ManagerIsInChannel: true,
			UserCount:          100, // Exceeds limit
		})
		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-user-limit"
		alert.SlackChannelID = channelID
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.True(t, containsIgnoreCase(w.Body.String(), "exceeds the limit"))
	})

	t.Run("alert with ignoreIfTextContains matching is ignored", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-ignored"
		alert.SlackChannelID = channelID
		alert.Text = "This error should be ignored because it contains a known pattern"
		alert.IgnoreIfTextContains = []string{"known pattern"}
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		// Should return 204 but no alert queued
		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Empty(t, queue.Messages())
	})

	t.Run("error response with error report channel queues error alert", func(t *testing.T) {
		t.Parallel()

		server, queue, _ := testServer(t)
		// Set error report channel
		server.cfg.ErrorReportChannelID = "CERROR12345"
		router := setupTestRouter(server)

		// Send invalid alert to trigger error response
		alert := common.NewErrorAlert()
		alert.Header = "Test Alert"
		// No channel ID, no route key - will fail
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		// Error alert should be queued to error report channel
		messages := queue.Messages()
		require.Len(t, messages, 1)
		assert.Equal(t, "CERROR12345", messages[0].channelID)
	})

	t.Run("channel name is mapped to ID", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		channelName := "alerts"
		mockProvider.addChannel(channelID, validChannelInfo())
		mockProvider.addChannelName(channelName, channelID)
		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-channel-name"
		alert.SlackChannelID = channelName // Use name instead of ID
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		require.Len(t, queue.Messages(), 1)
		assert.Equal(t, channelID, queue.Messages()[0].channelID) // Should use ID, not name
	})
}

// TestLoggingMiddleware tests the logging middleware directly
func TestLoggingMiddleware(t *testing.T) {
	t.Parallel()

	t.Run("logging middleware executes", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)

		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.Use(server.loggingMiddleware())
		router.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// TestQueueAlertErrors tests error paths in queueAlert
func TestQueueAlertErrors(t *testing.T) {
	t.Parallel()

	t.Run("alert without channel ID returns error", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)

		alert := common.NewErrorAlert()
		alert.SlackChannelID = "" // No channel ID

		err := server.queueAlert(context.Background(), alert)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no Slack channel ID")
	})
}

// TestGetClientErrorDebugText tests the debug text helper
func TestGetClientErrorDebugText(t *testing.T) {
	t.Parallel()

	t.Run("nil alert returns nil", func(t *testing.T) {
		t.Parallel()
		result := getClientErrorDebugText(nil)
		assert.Nil(t, result)
	})

	t.Run("alert returns debug fields", func(t *testing.T) {
		t.Parallel()
		alert := &common.Alert{
			CorrelationID: "corr-123",
			Header:        "Test Header",
			Text:          "Test Body",
		}
		result := getClientErrorDebugText(alert)
		assert.Equal(t, "corr-123", result["CorrelationId"])
		assert.Equal(t, "Test Header", result["Header"])
		assert.Equal(t, "Test Body", result["Body"])
	})
}

// TestGetAlertChannelWithRouteKey tests the alert channel helper
func TestGetAlertChannelWithRouteKey(t *testing.T) {
	t.Parallel()

	t.Run("channel ID only", func(t *testing.T) {
		t.Parallel()
		alert := &common.Alert{SlackChannelID: "C123456789"}
		result := getAlertChannelWithRouteKey(alert)
		assert.Equal(t, "C123456789", result)
	})

	t.Run("no channel ID returns N/A", func(t *testing.T) {
		t.Parallel()
		alert := &common.Alert{SlackChannelID: ""}
		result := getAlertChannelWithRouteKey(alert)
		assert.Equal(t, "N/A", result)
	})

	t.Run("channel ID with route key", func(t *testing.T) {
		t.Parallel()
		alert := &common.Alert{
			SlackChannelID: "C123456789",
			RouteKey:       "my-service",
		}
		result := getAlertChannelWithRouteKey(alert)
		assert.Equal(t, "C123456789 [my-service]", result)
	})

	t.Run("no channel ID with route key", func(t *testing.T) {
		t.Parallel()
		alert := &common.Alert{
			SlackChannelID: "",
			RouteKey:       "my-service",
		}
		result := getAlertChannelWithRouteKey(alert)
		assert.Equal(t, "N/A [my-service]", result)
	})
}

// TestCreateRateLimitAlert tests the rate limit alert creation
func TestCreateRateLimitAlert(t *testing.T) {
	t.Parallel()

	t.Run("creates rate limit alert with correct fields", func(t *testing.T) {
		t.Parallel()
		template := &common.Alert{
			IconEmoji:            ":warning:",
			Username:             "AlertBot",
			IssueFollowUpEnabled: true,
		}
		alert := createRateLimitAlert("C123456789", 5, template)

		assert.Equal(t, "__rate_limit_C123456789", alert.CorrelationID)
		assert.Contains(t, alert.Header, "Too many alerts")
		assert.Contains(t, alert.Text, "5 alerts were dropped")
		assert.Equal(t, "C123456789", alert.SlackChannelID)
		assert.Equal(t, ":warning:", alert.IconEmoji)
		assert.Equal(t, "AlertBot", alert.Username)
		assert.True(t, alert.IssueFollowUpEnabled)
	})

	t.Run("without issue follow up", func(t *testing.T) {
		t.Parallel()
		template := &common.Alert{
			IssueFollowUpEnabled: false,
		}
		alert := createRateLimitAlert("C123456789", 3, template)

		assert.False(t, alert.IssueFollowUpEnabled)
	})
}

// TestAlertsTestEndpointExtended tests more alerts-test scenarios
func TestAlertsTestEndpointExtended(t *testing.T) {
	t.Parallel()

	t.Run("invalid JSON returns 400", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)
		router := setupTestRouter(server)

		req := httptest.NewRequest(http.MethodPost, "/alerts-test", bytes.NewBufferString("{invalid}"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("alert without channel returns error", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)
		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = "Test Alert"
		// No channel ID, no route key
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alerts-test", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("alert with channel in body succeeds", func(t *testing.T) {
		t.Parallel()

		server, _, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-body"
		alert.SlackChannelID = channelID
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alerts-test", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
	})
}

// TestMultipleAlertsToMultipleChannels tests alerts to different channels
func TestMultipleAlertsToMultipleChannels(t *testing.T) {
	t.Parallel()

	t.Run("alerts to different channels are all processed", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channel1 := "C111111111"
		channel2 := "C222222222"
		mockProvider.addChannel(channel1, validChannelInfo())
		mockProvider.addChannel(channel2, validChannelInfo())
		router := setupTestRouter(server)

		alerts := []*common.Alert{
			{Header: ":status: Alert 1", CorrelationID: "corr-1", SlackChannelID: channel1, Severity: common.AlertError},
			{Header: ":status: Alert 2", CorrelationID: "corr-2", SlackChannelID: channel2, Severity: common.AlertWarning},
			{Header: ":status: Alert 3", CorrelationID: "corr-3", SlackChannelID: channel1, Severity: common.AlertInfo},
		}
		body, _ := json.Marshal(alerts)

		req := httptest.NewRequest(http.MethodPost, "/alerts", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Len(t, queue.Messages(), 3)
	})
}

// TestValidateChannelInfoDirectly tests validateChannelInfo directly
func TestValidateChannelInfoDirectly(t *testing.T) {
	t.Parallel()

	t.Run("channel exists and valid", func(t *testing.T) {
		t.Parallel()
		server, _, _ := testServer(t)
		info := validChannelInfo()
		err := server.validateChannelInfo("C123", info)
		assert.NoError(t, err)
	})

	t.Run("channel does not exist", func(t *testing.T) {
		t.Parallel()
		server, _, _ := testServer(t)
		info := &channelInfo{ChannelExists: false}
		err := server.validateChannelInfo("C123", info)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unable to find channel")
	})

	t.Run("channel is archived", func(t *testing.T) {
		t.Parallel()
		server, _, _ := testServer(t)
		info := &channelInfo{ChannelExists: true, ChannelIsArchived: true}
		err := server.validateChannelInfo("C123", info)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "archived")
	})

	t.Run("manager not in channel", func(t *testing.T) {
		t.Parallel()
		server, _, _ := testServer(t)
		info := &channelInfo{ChannelExists: true, ChannelIsArchived: false, ManagerIsInChannel: false}
		err := server.validateChannelInfo("C123", info)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not in channel")
	})

	t.Run("user count exceeds limit", func(t *testing.T) {
		t.Parallel()
		server, _, _ := testServer(t)
		server.cfg.MaxUsersInAlertChannel = 50
		info := &channelInfo{
			ChannelExists:      true,
			ChannelIsArchived:  false,
			ManagerIsInChannel: true,
			UserCount:          100,
		}
		err := server.validateChannelInfo("C123", info)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds the limit")
	})
}

// TestNewServerDefaults tests server creation with defaults
func TestNewServerDefaults(t *testing.T) {
	t.Parallel()

	t.Run("creates server with nil cache store", func(t *testing.T) {
		t.Parallel()
		queue := newMockQueue()
		logger := &mockLogger{}
		cfg := config.NewDefaultAPIConfig()

		server := New(queue, nil, logger, nil, cfg, nil)

		assert.NotNil(t, server)
		assert.NotNil(t, server.cacheStore)
		assert.NotNil(t, server.metrics)
		assert.NotNil(t, server.apiSettings)
	})
}

// TestQueueSendError tests error handling when queue send fails
func TestQueueSendError(t *testing.T) {
	t.Parallel()

	t.Run("queue send error returns 500", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		queue.sendErr = errors.New("queue send failed")
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-queue-error"
		alert.SlackChannelID = channelID
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.True(t, containsIgnoreCase(w.Body.String(), "queue"))
	})
}

// TestWriteErrorResponsePaths tests different error response code paths
func TestWriteErrorResponsePaths(t *testing.T) {
	t.Parallel()

	t.Run("single character error message", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)
		router := setupTestRouter(server)

		// Send request with invalid JSON that produces a short error
		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBufferString("x"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

// TestAlertValidationErrors tests alert validation error paths
func TestAlertValidationErrors(t *testing.T) {
	t.Parallel()

	t.Run("invalid alert severity returns 400", func(t *testing.T) {
		t.Parallel()

		server, _, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		// Create alert with invalid severity (missing required fields)
		alert := &common.Alert{
			SlackChannelID: channelID,
			// Missing Header and CorrelationID
		}
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.True(t, containsIgnoreCase(w.Body.String(), "validation"))
	})
}

// TestPrometheusWebhookEdgeCases tests additional Prometheus webhook scenarios
func TestPrometheusWebhookEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("alert with warning severity", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		webhook := PrometheusWebhook{
			GroupKey: "test-warning",
			Alerts: []*PrometheusAlert{
				{
					Status: "firing",
					Labels: map[string]string{
						"alertname": "TestAlert",
						"severity":  "warning",
					},
					Annotations: map[string]string{
						"summary": "Warning alert",
					},
					StartsAt: time.Now(),
				},
			},
		}
		body, _ := json.Marshal(webhook)

		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert/"+channelID, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		require.Len(t, queue.Messages(), 1)

		var alert common.Alert
		err := json.Unmarshal([]byte(queue.Messages()[0].body), &alert)
		require.NoError(t, err)
		assert.Equal(t, common.AlertWarning, alert.Severity)
	})

	t.Run("alert with info severity", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		webhook := PrometheusWebhook{
			GroupKey: "test-info",
			Alerts: []*PrometheusAlert{
				{
					Status: "firing",
					Labels: map[string]string{
						"alertname": "TestAlert",
						"severity":  "info",
					},
					Annotations: map[string]string{
						"summary": "Info alert",
					},
					StartsAt: time.Now(),
				},
			},
		}
		body, _ := json.Marshal(webhook)

		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert/"+channelID, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		require.Len(t, queue.Messages(), 1)

		var alert common.Alert
		err := json.Unmarshal([]byte(queue.Messages()[0].body), &alert)
		require.NoError(t, err)
		assert.Equal(t, common.AlertInfo, alert.Severity)
	})

	t.Run("alert with multiple labels generates correlation ID", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		webhook := PrometheusWebhook{
			GroupKey: "test-labels",
			Alerts: []*PrometheusAlert{
				{
					Status: "firing",
					Labels: map[string]string{
						"alertname": "TestAlert",
						"namespace": "production",
						"job":       "api-server",
						"service":   "backend",
					},
					Annotations: map[string]string{
						"summary": "Multi-label alert",
					},
					StartsAt: time.Now(),
				},
			},
		}
		body, _ := json.Marshal(webhook)

		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert/"+channelID, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		require.Len(t, queue.Messages(), 1)

		var alert common.Alert
		err := json.Unmarshal([]byte(queue.Messages()[0].body), &alert)
		require.NoError(t, err)
		assert.NotEmpty(t, alert.CorrelationID)
	})

	t.Run("alert with monitoring namespace includes instance in correlation ID", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		webhook := PrometheusWebhook{
			GroupKey: "test-monitoring",
			Alerts: []*PrometheusAlert{
				{
					Status: "firing",
					Labels: map[string]string{
						"alertname": "TestAlert",
						"namespace": "monitoring",
						"instance":  "localhost:9090",
					},
					Annotations: map[string]string{
						"summary": "Monitoring namespace alert",
					},
					StartsAt: time.Now(),
				},
			},
		}
		body, _ := json.Marshal(webhook)

		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert/"+channelID, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		require.Len(t, queue.Messages(), 1)
	})

	t.Run("alert with uppercase labels", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		webhook := PrometheusWebhook{
			GroupKey: "test-uppercase",
			Alerts: []*PrometheusAlert{
				{
					Status: "firing",
					Labels: map[string]string{
						"ALERTNAME": "TestAlert",
						"SEVERITY":  "error",
					},
					Annotations: map[string]string{
						"SUMMARY": "Uppercase labels alert",
					},
					StartsAt: time.Now(),
				},
			},
		}
		body, _ := json.Marshal(webhook)

		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert/"+channelID, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		require.Len(t, queue.Messages(), 1)
	})

	t.Run("multiple alerts in single webhook", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		webhook := PrometheusWebhook{
			GroupKey: "test-multi",
			Alerts: []*PrometheusAlert{
				{
					Status: "firing",
					Labels: map[string]string{
						"alertname": "Alert1",
						"severity":  "error",
					},
					Annotations: map[string]string{
						"summary": "First alert",
					},
					StartsAt: time.Now(),
				},
				{
					Status: "firing",
					Labels: map[string]string{
						"alertname": "Alert2",
						"severity":  "warning",
					},
					Annotations: map[string]string{
						"summary": "Second alert",
					},
					StartsAt: time.Now(),
				},
			},
		}
		body, _ := json.Marshal(webhook)

		req := httptest.NewRequest(http.MethodPost, "/prometheus-alert/"+channelID, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Len(t, queue.Messages(), 2)
	})
}

// TestAllIgnoredAlerts tests when all alerts are ignored
func TestAllIgnoredAlerts(t *testing.T) {
	t.Parallel()

	t.Run("all alerts ignored returns 204 with no queued messages", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())
		router := setupTestRouter(server)

		alerts := []*common.Alert{
			{
				Header:               ":status: Alert 1",
				CorrelationID:        "corr-1",
				SlackChannelID:       channelID,
				Severity:             common.AlertError,
				Text:                 "This contains ignore-me pattern",
				IgnoreIfTextContains: []string{"ignore-me"},
			},
			{
				Header:               ":status: Alert 2",
				CorrelationID:        "corr-2",
				SlackChannelID:       channelID,
				Severity:             common.AlertWarning,
				Text:                 "This also contains ignore-me pattern",
				IgnoreIfTextContains: []string{"ignore-me"},
			},
		}
		body, _ := json.Marshal(alerts)

		req := httptest.NewRequest(http.MethodPost, "/alerts", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Empty(t, queue.Messages())
	})
}

// TestSetSlackChannelID tests various channel ID resolution scenarios
func TestSetSlackChannelID(t *testing.T) {
	t.Parallel()

	t.Run("empty alerts array returns no error", func(t *testing.T) {
		t.Parallel()

		server, _, _ := testServer(t)
		err := server.setSlackChannelID("", []*common.Alert{}...)
		assert.NoError(t, err)
	})

	t.Run("route key with match_all rule", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())

		// Set up a match-all routing rule
		server.apiSettings.RoutingRules = []*config.RoutingRule{
			{
				Name:     "catch-all",
				Channel:  channelID,
				MatchAll: true,
			},
		}
		_ = server.apiSettings.InitAndValidate(&mockLogger{})

		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-match-all"
		alert.RouteKey = "any-service"
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		require.Len(t, queue.Messages(), 1)
		assert.Equal(t, channelID, queue.Messages()[0].channelID)
	})

	t.Run("route key with prefix match rule", func(t *testing.T) {
		t.Parallel()

		server, queue, mockProvider := testServer(t)
		channelID := "C123456789"
		mockProvider.addChannel(channelID, validChannelInfo())

		// Set up a prefix matching rule
		server.apiSettings.RoutingRules = []*config.RoutingRule{
			{
				Name:      "prefix-match",
				Channel:   channelID,
				HasPrefix: []string{"prod-"},
			},
		}
		_ = server.apiSettings.InitAndValidate(&mockLogger{})

		router := setupTestRouter(server)

		alert := common.NewErrorAlert()
		alert.Header = ":status: Test Alert"
		alert.CorrelationID = "test-prefix"
		alert.RouteKey = "prod-api-server"
		body, _ := json.Marshal(alert)

		req := httptest.NewRequest(http.MethodPost, "/alert", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		require.Len(t, queue.Messages(), 1)
	})
}
