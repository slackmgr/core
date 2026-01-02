package config_test

import (
	"testing"

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

	require.NotNil(t, cfg.RateLimit)
	assert.InDelta(t, 0.5, cfg.RateLimit.AlertsPerSecond, 0.001)
	assert.Equal(t, 10, cfg.RateLimit.AllowedBurst)
	assert.Equal(t, 10, cfg.RateLimit.MaxWaitPerAttemptSeconds)
	assert.Equal(t, 3, cfg.RateLimit.MaxAttempts)

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
		{
			name: "empty rest port",
			modify: func(c *config.APIConfig) {
				c.RestPort = ""
			},
			expectError: "rest port is required",
		},
		{
			name: "empty encryption key",
			modify: func(c *config.APIConfig) {
				c.EncryptionKey = ""
			},
			expectError: "the encryption key must be a 32 character alphanumeric string",
		},
		{
			name: "encryption key too short",
			modify: func(c *config.APIConfig) {
				c.EncryptionKey = "abc123"
			},
			expectError: "the encryption key must be a 32 character alphanumeric string",
		},
		{
			name: "encryption key too long",
			modify: func(c *config.APIConfig) {
				c.EncryptionKey = "abcdefghijklmnopqrstuvwxyz1234567890"
			},
			expectError: "the encryption key must be a 32 character alphanumeric string",
		},
		{
			name: "encryption key with special characters",
			modify: func(c *config.APIConfig) {
				c.EncryptionKey = "abcdefghijklmnopqrstuvwxyz12345!"
			},
			expectError: "the encryption key must be a 32 character alphanumeric string",
		},
		{
			name: "nil slack client config",
			modify: func(c *config.APIConfig) {
				c.SlackClient = nil
			},
			expectError: "slack client config is required",
		},
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

	t.Run("max users at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.MaxUsersInAlertChannel = 1
		assert.NoError(t, cfg.Validate())
	})

	t.Run("max users at upper bound", func(t *testing.T) {
		t.Parallel()
		cfg := validAPIConfig()
		cfg.MaxUsersInAlertChannel = 10000
		assert.NoError(t, cfg.Validate())
	})

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
}

// validAPIConfig returns an APIConfig with all required fields populated for testing.
func validAPIConfig() *config.APIConfig {
	cfg := config.NewDefaultAPIConfig()
	cfg.EncryptionKey = "abcdefghijklmnopqrstuvwxyz123456"
	cfg.SlackClient.AppToken = "xapp-test-token"
	cfg.SlackClient.BotToken = "xoxb-test-token"
	return cfg
}
