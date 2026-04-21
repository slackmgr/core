package config_test

import (
	"strings"
	"testing"

	"github.com/slackmgr/core/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubDefaultsSetter struct {
	m map[string]any
}

func newStubDefaultsSetter() *stubDefaultsSetter {
	return &stubDefaultsSetter{m: make(map[string]any)}
}

func (s *stubDefaultsSetter) SetDefault(key string, value any) {
	s.m[key] = value
}

func TestSetManagerConfigDefaults(t *testing.T) {
	t.Parallel()

	stub := newStubDefaultsSetter()
	config.SetManagerConfigDefaults(stub)

	assert.Equal(t, config.DefaultKeyPrefix, stub.m["cacheKeyPrefix"])
	assert.Equal(t, config.DefaultMetricsPrefix, stub.m["metricsPrefix"])
	assert.Equal(t, config.DefaultLocation, stub.m["location"])
	assert.Equal(t, config.DefaultCoordinatorDrainTimeoutMs, stub.m["coordinatorDrainTimeoutMs"])
	assert.Equal(t, config.DefaultChannelManagerDrainTimeoutMs, stub.m["channelManagerDrainTimeoutMs"])
	assert.Equal(t, config.DefaultSocketModeMaxWorkers, stub.m["socketModeMaxWorkers"])
	assert.Equal(t, config.DefaultSocketModeDrainTimeoutMs, stub.m["socketModeDrainTimeoutMs"])

	// SlackClient defaults nested under "slackClient"
	assert.Equal(t, config.DefaultConcurrency, stub.m["slackClient.concurrency"])
	assert.Equal(t, config.DefaultMaxAttemptsForRateLimitError, stub.m["slackClient.maxAttemptsForRateLimitError"])
	assert.Equal(t, config.DefaultHTTPTimeoutSeconds, stub.m["slackClient.httpTimeoutSeconds"])

	// Zero-value / must-be-explicit fields must NOT be registered
	_, hasEncryptionKey := stub.m["encryptionKey"]
	assert.False(t, hasEncryptionKey, "encryptionKey must not have a default")
	_, hasAppToken := stub.m["slackClient.appToken"]
	assert.False(t, hasAppToken, "slackClient.appToken must not have a default")
	_, hasBotToken := stub.m["slackClient.botToken"]
	assert.False(t, hasBotToken, "slackClient.botToken must not have a default")
}

func TestSetAPIConfigDefaults(t *testing.T) {
	t.Parallel()

	stub := newStubDefaultsSetter()
	config.SetAPIConfigDefaults(stub)

	assert.Equal(t, true, stub.m["logJson"])
	assert.Equal(t, "8080", stub.m["restPort"])
	assert.Equal(t, config.DefaultKeyPrefix, stub.m["cacheKeyPrefix"])
	assert.Equal(t, config.DefaultMetricsPrefix, stub.m["metricsPrefix"])
	assert.Equal(t, 100, stub.m["maxUsersInAlertChannel"])
	assert.InEpsilon(t, 1.0, stub.m["rateLimitPerAlertChannel.alertsPerSecond"], 1e-9)
	assert.Equal(t, 30, stub.m["rateLimitPerAlertChannel.allowedBurst"])

	// SlackClient defaults nested under "slackClient"
	assert.Equal(t, config.DefaultConcurrency, stub.m["slackClient.concurrency"])
	assert.Equal(t, config.DefaultHTTPTimeoutSeconds, stub.m["slackClient.httpTimeoutSeconds"])

	// Zero-value / must-be-explicit fields must NOT be registered
	_, hasEncryptionKey := stub.m["encryptionKey"]
	assert.False(t, hasEncryptionKey, "encryptionKey must not have a default")
	_, hasErrorChannel := stub.m["errorReportChannelId"]
	assert.False(t, hasErrorChannel, "errorReportChannelId must not have a default")
	_, hasVerbose := stub.m["verbose"]
	assert.False(t, hasVerbose, "verbose must not have a default")
}

func TestSetSlackClientConfigDefaults_Prefix(t *testing.T) {
	t.Parallel()

	managerStub := newStubDefaultsSetter()
	config.SetManagerConfigDefaults(managerStub)

	apiStub := newStubDefaultsSetter()
	config.SetAPIConfigDefaults(apiStub)

	slackClientKeys := []string{
		"slackClient.concurrency",
		"slackClient.maxAttemptsForRateLimitError",
		"slackClient.maxAttemptsForTransientError",
		"slackClient.maxRateLimitErrorWaitTimeSeconds",
		"slackClient.maxTransientErrorWaitTimeSeconds",
		"slackClient.httpTimeoutSeconds",
	}

	for _, key := range slackClientKeys {
		assert.Contains(t, managerStub.m, key, "SetManagerConfigDefaults missing %s", key)
		assert.Contains(t, apiStub.m, key, "SetAPIConfigDefaults missing %s", key)
	}

	// Verify no non-slackClient key accidentally starts with the prefix
	for key := range managerStub.m {
		if strings.HasPrefix(key, "slackClient.") {
			assert.Contains(t, slackClientKeys, key, "unexpected slackClient key: %s", key)
		}
	}
}

// TestSetManagerConfigDefaults_MatchesConstructor verifies that every default registered
// by SetManagerConfigDefaults matches the value set by NewDefaultManagerConfig.
// This prevents the two from drifting apart silently when new fields are added.
func TestSetManagerConfigDefaults_MatchesConstructor(t *testing.T) {
	t.Parallel()

	stub := newStubDefaultsSetter()
	config.SetManagerConfigDefaults(stub)

	cfg := config.NewDefaultManagerConfig()
	require.NotNil(t, cfg.SlackClient)

	assert.Equal(t, cfg.CacheKeyPrefix, stub.m["cacheKeyPrefix"])
	assert.Equal(t, cfg.MetricsPrefix, stub.m["metricsPrefix"])
	assert.Equal(t, cfg.Location, stub.m["location"])
	assert.Equal(t, cfg.CoordinatorDrainTimeoutMs, stub.m["coordinatorDrainTimeoutMs"])
	assert.Equal(t, cfg.ChannelManagerDrainTimeoutMs, stub.m["channelManagerDrainTimeoutMs"])
	assert.Equal(t, cfg.SocketModeMaxWorkers, stub.m["socketModeMaxWorkers"])
	assert.Equal(t, cfg.SocketModeDrainTimeoutMs, stub.m["socketModeDrainTimeoutMs"])
	assert.Equal(t, cfg.SlackClient.Concurrency, stub.m["slackClient.concurrency"])
	assert.Equal(t, cfg.SlackClient.MaxAttemptsForRateLimitError, stub.m["slackClient.maxAttemptsForRateLimitError"])
	assert.Equal(t, cfg.SlackClient.MaxAttemptsForTransientError, stub.m["slackClient.maxAttemptsForTransientError"])
	assert.Equal(t, cfg.SlackClient.MaxRateLimitErrorWaitTimeSeconds, stub.m["slackClient.maxRateLimitErrorWaitTimeSeconds"])
	assert.Equal(t, cfg.SlackClient.MaxTransientErrorWaitTimeSeconds, stub.m["slackClient.maxTransientErrorWaitTimeSeconds"])
	assert.Equal(t, cfg.SlackClient.HTTPTimeoutSeconds, stub.m["slackClient.httpTimeoutSeconds"])
}

// TestSetAPIConfigDefaults_MatchesConstructor verifies that every default registered
// by SetAPIConfigDefaults matches the value set by NewDefaultAPIConfig.
func TestSetAPIConfigDefaults_MatchesConstructor(t *testing.T) {
	t.Parallel()

	stub := newStubDefaultsSetter()
	config.SetAPIConfigDefaults(stub)

	cfg := config.NewDefaultAPIConfig()
	require.NotNil(t, cfg.RateLimitPerAlertChannel)
	require.NotNil(t, cfg.SlackClient)

	assert.Equal(t, cfg.LogJSON, stub.m["logJson"])
	assert.Equal(t, cfg.RestPort, stub.m["restPort"])
	assert.Equal(t, cfg.CacheKeyPrefix, stub.m["cacheKeyPrefix"])
	assert.Equal(t, cfg.MetricsPrefix, stub.m["metricsPrefix"])
	assert.Equal(t, cfg.MaxUsersInAlertChannel, stub.m["maxUsersInAlertChannel"])
	assert.InEpsilon(t, cfg.RateLimitPerAlertChannel.AlertsPerSecond, stub.m["rateLimitPerAlertChannel.alertsPerSecond"], 1e-9)
	assert.Equal(t, cfg.RateLimitPerAlertChannel.AllowedBurst, stub.m["rateLimitPerAlertChannel.allowedBurst"])
	assert.Equal(t, cfg.SlackClient.Concurrency, stub.m["slackClient.concurrency"])
	assert.Equal(t, cfg.SlackClient.MaxAttemptsForRateLimitError, stub.m["slackClient.maxAttemptsForRateLimitError"])
	assert.Equal(t, cfg.SlackClient.MaxAttemptsForTransientError, stub.m["slackClient.maxAttemptsForTransientError"])
	assert.Equal(t, cfg.SlackClient.MaxRateLimitErrorWaitTimeSeconds, stub.m["slackClient.maxRateLimitErrorWaitTimeSeconds"])
	assert.Equal(t, cfg.SlackClient.MaxTransientErrorWaitTimeSeconds, stub.m["slackClient.maxTransientErrorWaitTimeSeconds"])
	assert.Equal(t, cfg.SlackClient.HTTPTimeoutSeconds, stub.m["slackClient.httpTimeoutSeconds"])
}
