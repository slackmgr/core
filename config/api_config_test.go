package config_test

import (
	"strings"
	"testing"

	"github.com/slackmgr/core/config"
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
	assert.Equal(t, config.DefaultMetricsPrefix, cfg.MetricsPrefix)
	assert.Empty(t, cfg.ErrorReportChannelID)
	assert.Equal(t, 100, cfg.MaxUsersInAlertChannel)

	require.NotNil(t, cfg.RateLimitPerAlertChannel)
	assert.InDelta(t, 1.0, cfg.RateLimitPerAlertChannel.AlertsPerSecond, 0.001)
	assert.Equal(t, 30, cfg.RateLimitPerAlertChannel.AllowedBurst)

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
			expectError: "",
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
		{
			name: "cache key prefix too long",
			modify: func(c *config.APIConfig) {
				c.CacheKeyPrefix = strings.Repeat("a", config.MaxCacheKeyPrefixLen+1)
			},
			expectError: "cache key prefix must not exceed 100 characters",
		},
		{
			name: "cache key prefix with space",
			modify: func(c *config.APIConfig) {
				c.CacheKeyPrefix = "my prefix:"
			},
			expectError: "cache key prefix may only contain letters, digits, '.', '_', ':', or '-'",
		},
		{
			name: "cache key prefix with at sign",
			modify: func(c *config.APIConfig) {
				c.CacheKeyPrefix = "my@prefix:"
			},
			expectError: "cache key prefix may only contain letters, digits, '.', '_', ':', or '-'",
		},
		// MetricsPrefix validation
		{
			name: "empty metrics prefix is valid",
			modify: func(c *config.APIConfig) {
				c.MetricsPrefix = ""
			},
			expectError: "",
		},
		{
			name: "metrics prefix too long",
			modify: func(c *config.APIConfig) {
				c.MetricsPrefix = strings.Repeat("a", config.MaxMetricsPrefixLen+1)
			},
			expectError: "metrics prefix must not exceed 64 characters",
		},
		{
			name: "metrics prefix starts with digit",
			modify: func(c *config.APIConfig) {
				c.MetricsPrefix = "1invalid_"
			},
			expectError: "metrics prefix must start with a letter or underscore and contain only letters, digits, or underscores",
		},
		{
			name: "metrics prefix contains hyphen",
			modify: func(c *config.APIConfig) {
				c.MetricsPrefix = "slackmgr-"
			},
			expectError: "metrics prefix must start with a letter or underscore and contain only letters, digits, or underscores",
		},
		{
			name: "metrics prefix contains colon",
			modify: func(c *config.APIConfig) {
				c.MetricsPrefix = "slackmgr:"
			},
			expectError: "metrics prefix must start with a letter or underscore and contain only letters, digits, or underscores",
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

func TestAPIConfig_Validate_PrefixVariations(t *testing.T) {
	t.Parallel()

	// CacheKeyPrefix valid variants
	t.Run("cache key prefix with colon separator", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.CacheKeyPrefix = "slack-manager:"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("cache key prefix with underscore and dot", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.CacheKeyPrefix = "my_app.v2:"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("cache key prefix at max length", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.CacheKeyPrefix = strings.Repeat("a", config.MaxCacheKeyPrefixLen)
		assert.NoError(t, cfg.Validate())
	})

	// MetricsPrefix valid variants
	t.Run("metrics prefix with trailing underscore", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.MetricsPrefix = "slackmgr_"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("metrics prefix starting with underscore", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.MetricsPrefix = "_internal_"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("metrics prefix at max length", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.MetricsPrefix = strings.Repeat("a", config.MaxMetricsPrefixLen)
		assert.NoError(t, cfg.Validate())
	})
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
}

// validAPIConfig returns an APIConfig with all required fields populated for testing.
func validAPIConfig() *config.APIConfig {
	cfg := config.NewDefaultAPIConfig()
	cfg.EncryptionKey = "abcdefghijklmnopqrstuvwxyz123456"
	cfg.SlackClient.AppToken = "xapp-test-token"
	cfg.SlackClient.BotToken = "xoxb-test-token"
	return cfg
}
