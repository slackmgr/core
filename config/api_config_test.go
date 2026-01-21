package config_test

import (
	"testing"
	"time"

	"github.com/peteraglen/slack-manager/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDefaultAPIConfig(t *testing.T) {
	t.Parallel()

	cfg := config.NewDefaultAPIConfig()

	assert.True(t, cfg.LogJSON)
	assert.False(t, cfg.Verbose)
	assert.Equal(t, "8080", cfg.RestPort)
	assert.Empty(t, cfg.EncryptionKey)
	assert.Equal(t, "slack-manager:", cfg.CacheKeyPrefix)
	assert.Empty(t, cfg.ErrorReportChannelID)
	assert.Equal(t, 100, cfg.MaxUsersInAlertChannel)

	require.NotNil(t, cfg.RateLimitPerAlertChannel)
	assert.InDelta(t, 1.0, cfg.RateLimitPerAlertChannel.AlertsPerSecond, 0.001)
	assert.Equal(t, 30, cfg.RateLimitPerAlertChannel.AllowedBurst)
	assert.Equal(t, 15*time.Second, cfg.RateLimitPerAlertChannel.MaxRequestWaitTime)

	require.NotNil(t, cfg.SlackClient)
}

func TestAPIConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		modify      func(*config.APIConfig)
		expectError string
	}{
		{
			name:        "valid config",
			modify:      func(c *config.APIConfig) {},
			expectError: "",
		},
		// RestPort validation
		{
			name: "empty rest port",
			modify: func(c *config.APIConfig) {
				c.RestPort = ""
			},
			expectError: "rest port is required",
		},
		{
			name: "rest port is not a number",
			modify: func(c *config.APIConfig) {
				c.RestPort = "abc"
			},
			expectError: "rest port must be a valid number",
		},
		{
			name: "rest port is zero",
			modify: func(c *config.APIConfig) {
				c.RestPort = "0"
			},
			expectError: "rest port must be between 1 and 65535",
		},
		{
			name: "rest port exceeds maximum",
			modify: func(c *config.APIConfig) {
				c.RestPort = "65536"
			},
			expectError: "rest port must be between 1 and 65535",
		},
		// EncryptionKey validation
		{
			name: "empty encryption key",
			modify: func(c *config.APIConfig) {
				c.EncryptionKey = ""
			},
			expectError: "encryption key must be a 32 character alphanumeric string",
		},
		{
			name: "encryption key too short",
			modify: func(c *config.APIConfig) {
				c.EncryptionKey = "abc123"
			},
			expectError: "encryption key must be a 32 character alphanumeric string",
		},
		{
			name: "encryption key too long",
			modify: func(c *config.APIConfig) {
				c.EncryptionKey = "abcdefghijklmnopqrstuvwxyz1234567890"
			},
			expectError: "encryption key must be a 32 character alphanumeric string",
		},
		{
			name: "encryption key with special characters",
			modify: func(c *config.APIConfig) {
				c.EncryptionKey = "abcdefghijklmnopqrstuvwxyz12345!"
			},
			expectError: "encryption key must be a 32 character alphanumeric string",
		},
		// CacheKeyPrefix validation
		{
			name: "empty cache key prefix",
			modify: func(c *config.APIConfig) {
				c.CacheKeyPrefix = ""
			},
			expectError: "cache key prefix is required",
		},
		// MaxUsersInAlertChannel validation
		{
			name: "max users in alert channel is zero",
			modify: func(c *config.APIConfig) {
				c.MaxUsersInAlertChannel = 0
			},
			expectError: "max users in alert channel must be between 1 and 10000",
		},
		{
			name: "max users in alert channel is negative",
			modify: func(c *config.APIConfig) {
				c.MaxUsersInAlertChannel = -1
			},
			expectError: "max users in alert channel must be between 1 and 10000",
		},
		{
			name: "max users in alert channel exceeds limit",
			modify: func(c *config.APIConfig) {
				c.MaxUsersInAlertChannel = 10001
			},
			expectError: "max users in alert channel must be between 1 and 10000",
		},
		// RateLimitPerAlertChannel validation
		{
			name: "nil rate limit config",
			modify: func(c *config.APIConfig) {
				c.RateLimitPerAlertChannel = nil
			},
			expectError: "rate limit config is required",
		},
		{
			name: "alerts per second is zero",
			modify: func(c *config.APIConfig) {
				c.RateLimitPerAlertChannel.AlertsPerSecond = 0
			},
			expectError: "rate limit config is invalid: alerts per second must be between 0.001 and 1000",
		},
		{
			name: "alerts per second is negative",
			modify: func(c *config.APIConfig) {
				c.RateLimitPerAlertChannel.AlertsPerSecond = -1
			},
			expectError: "rate limit config is invalid: alerts per second must be between 0.001 and 1000",
		},
		{
			name: "alerts per second exceeds maximum",
			modify: func(c *config.APIConfig) {
				c.RateLimitPerAlertChannel.AlertsPerSecond = 1001
			},
			expectError: "rate limit config is invalid: alerts per second must be between 0.001 and 1000",
		},
		{
			name: "allowed burst is zero",
			modify: func(c *config.APIConfig) {
				c.RateLimitPerAlertChannel.AllowedBurst = 0
			},
			expectError: "rate limit config is invalid: allowed burst must be between 1 and 10000",
		},
		{
			name: "allowed burst is negative",
			modify: func(c *config.APIConfig) {
				c.RateLimitPerAlertChannel.AllowedBurst = -1
			},
			expectError: "rate limit config is invalid: allowed burst must be between 1 and 10000",
		},
		{
			name: "allowed burst exceeds maximum",
			modify: func(c *config.APIConfig) {
				c.RateLimitPerAlertChannel.AllowedBurst = 10001
			},
			expectError: "rate limit config is invalid: allowed burst must be between 1 and 10000",
		},
		{
			name: "max request wait time is zero",
			modify: func(c *config.APIConfig) {
				c.RateLimitPerAlertChannel.MaxRequestWaitTime = 0
			},
			expectError: "rate limit config is invalid: max request wait time must be between 1s and 5m0s",
		},
		{
			name: "max request wait time is too short",
			modify: func(c *config.APIConfig) {
				c.RateLimitPerAlertChannel.MaxRequestWaitTime = 500 * time.Millisecond
			},
			expectError: "rate limit config is invalid: max request wait time must be between 1s and 5m0s",
		},
		{
			name: "max request wait time exceeds maximum",
			modify: func(c *config.APIConfig) {
				c.RateLimitPerAlertChannel.MaxRequestWaitTime = 6 * time.Minute
			},
			expectError: "rate limit config is invalid: max request wait time must be between 1s and 5m0s",
		},
		// SlackClient validation
		{
			name: "nil slack client config",
			modify: func(c *config.APIConfig) {
				c.SlackClient = nil
			},
			expectError: "slack client config is required",
		},
		{
			name: "invalid slack client config - missing app token",
			modify: func(c *config.APIConfig) {
				c.SlackClient.AppToken = ""
			},
			expectError: "slack client config is invalid: app token is empty",
		},
		{
			name: "invalid slack client config - missing bot token",
			modify: func(c *config.APIConfig) {
				c.SlackClient.BotToken = ""
				c.SlackClient.AppToken = "xapp-test-token"
			},
			expectError: "slack client config is invalid: bot token is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := validAPIConfig()
			tt.modify(cfg)

			err := cfg.Validate()

			if tt.expectError == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Equal(t, tt.expectError, err.Error())
			}
		})
	}
}

func TestAPIConfig_Validate_BoundaryValues(t *testing.T) {
	t.Parallel()

	// RestPort boundaries
	t.Run("rest port at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.RestPort = "1"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("rest port at upper bound", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.RestPort = "65535"
		assert.NoError(t, cfg.Validate())
	})

	// MaxUsersInAlertChannel boundaries
	t.Run("max users at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.MaxUsersInAlertChannel = config.MinMaxUsersInAlertChannel
		assert.NoError(t, cfg.Validate())
	})

	t.Run("max users at upper bound", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.MaxUsersInAlertChannel = config.MaxMaxUsersInAlertChannel
		assert.NoError(t, cfg.Validate())
	})

	// EncryptionKey boundaries
	t.Run("encryption key exactly 32 alphanumeric chars", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.EncryptionKey = "abcdefghijklmnopqrstuvwxyz123456"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("encryption key with uppercase", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.EncryptionKey = "ABCDEFGHIJKLMNOPQRSTUVWXYZ123456"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("encryption key mixed case", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.EncryptionKey = "AbCdEfGhIjKlMnOpQrStUvWxYz123456"
		assert.NoError(t, cfg.Validate())
	})

	// AlertsPerSecond boundaries
	t.Run("alerts per second at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.RateLimitPerAlertChannel.AlertsPerSecond = config.MinAlertsPerSecond
		assert.NoError(t, cfg.Validate())
	})

	t.Run("alerts per second at upper bound", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.RateLimitPerAlertChannel.AlertsPerSecond = config.MaxAlertsPerSecond
		assert.NoError(t, cfg.Validate())
	})

	// AllowedBurst boundaries
	t.Run("allowed burst at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.RateLimitPerAlertChannel.AllowedBurst = config.MinAllowedBurst
		assert.NoError(t, cfg.Validate())
	})

	t.Run("allowed burst at upper bound", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.RateLimitPerAlertChannel.AllowedBurst = config.MaxAllowedBurst
		assert.NoError(t, cfg.Validate())
	})

	// MaxRequestWaitTime boundaries
	t.Run("max request wait time at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.RateLimitPerAlertChannel.MaxRequestWaitTime = config.MinMaxRequestWaitTime
		assert.NoError(t, cfg.Validate())
	})

	t.Run("max request wait time at upper bound", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.RateLimitPerAlertChannel.MaxRequestWaitTime = config.MaxMaxRequestWaitTime
		assert.NoError(t, cfg.Validate())
	})
}

// validAPIConfig returns an APIConfig with all required fields populated for testing.
func validAPIConfig() *config.APIConfig {
	cfg := config.NewDefaultAPIConfig()
	cfg.EncryptionKey = "abcdefghijklmnopqrstuvwxyz123456"
	cfg.SlackClient.AppToken = "xapp-test-token"
	cfg.SlackClient.BotToken = "xoxb-test-token"
	return cfg
}
