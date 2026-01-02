package config_test

import (
	"testing"

	"github.com/peteraglen/slack-manager/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDefaultSlackClientConfig(t *testing.T) {
	t.Parallel()

	cfg := config.NewDefaultSlackClientConfig()

	assert.Empty(t, cfg.AppToken)
	assert.Empty(t, cfg.BotToken)
	assert.False(t, cfg.DebugLogging)
	assert.False(t, cfg.DryRun)
	assert.Equal(t, 3, cfg.Concurrency)
	assert.Equal(t, 10, cfg.MaxAttemtsForRateLimitError)
	assert.Equal(t, 5, cfg.MaxAttemptsForTransientError)
	assert.Equal(t, 5, cfg.MaxAttemptsForFatalError)
	assert.Equal(t, 120, cfg.MaxRateLimitErrorWaitTimeSeconds)
	assert.Equal(t, 30, cfg.MaxTransientErrorWaitTimeSeconds)
	assert.Equal(t, 30, cfg.MaxFatalErrorWaitTimeSeconds)
	assert.Equal(t, 30, cfg.HTTPTimeoutSeconds)
}

func TestSlackClientConfig_SetDefaults(t *testing.T) {
	t.Parallel()

	t.Run("sets defaults for zero values", func(t *testing.T) {
		t.Parallel()

		cfg := &config.SlackClientConfig{}
		cfg.SetDefaults()

		assert.Equal(t, 3, cfg.Concurrency)
		assert.Equal(t, 10, cfg.MaxAttemtsForRateLimitError)
		assert.Equal(t, 5, cfg.MaxAttemptsForTransientError)
		assert.Equal(t, 5, cfg.MaxAttemptsForFatalError)
		assert.Equal(t, 120, cfg.MaxRateLimitErrorWaitTimeSeconds)
		assert.Equal(t, 30, cfg.MaxTransientErrorWaitTimeSeconds)
		assert.Equal(t, 30, cfg.MaxFatalErrorWaitTimeSeconds)
		assert.Equal(t, 30, cfg.HTTPTimeoutSeconds)
	})

	t.Run("sets defaults for negative values", func(t *testing.T) {
		t.Parallel()

		cfg := &config.SlackClientConfig{
			Concurrency:                      -1,
			MaxAttemtsForRateLimitError:      -5,
			MaxAttemptsForTransientError:     -10,
			MaxAttemptsForFatalError:         -3,
			MaxRateLimitErrorWaitTimeSeconds: -100,
			MaxTransientErrorWaitTimeSeconds: -50,
			MaxFatalErrorWaitTimeSeconds:     -25,
			HTTPTimeoutSeconds:               -15,
		}
		cfg.SetDefaults()

		assert.Equal(t, 3, cfg.Concurrency)
		assert.Equal(t, 10, cfg.MaxAttemtsForRateLimitError)
		assert.Equal(t, 5, cfg.MaxAttemptsForTransientError)
		assert.Equal(t, 5, cfg.MaxAttemptsForFatalError)
		assert.Equal(t, 120, cfg.MaxRateLimitErrorWaitTimeSeconds)
		assert.Equal(t, 30, cfg.MaxTransientErrorWaitTimeSeconds)
		assert.Equal(t, 30, cfg.MaxFatalErrorWaitTimeSeconds)
		assert.Equal(t, 30, cfg.HTTPTimeoutSeconds)
	})

	t.Run("preserves positive custom values", func(t *testing.T) {
		t.Parallel()

		cfg := &config.SlackClientConfig{
			Concurrency:                      5,
			MaxAttemtsForRateLimitError:      20,
			MaxAttemptsForTransientError:     15,
			MaxAttemptsForFatalError:         10,
			MaxRateLimitErrorWaitTimeSeconds: 200,
			MaxTransientErrorWaitTimeSeconds: 60,
			MaxFatalErrorWaitTimeSeconds:     45,
			HTTPTimeoutSeconds:               60,
		}
		cfg.SetDefaults()

		assert.Equal(t, 5, cfg.Concurrency)
		assert.Equal(t, 20, cfg.MaxAttemtsForRateLimitError)
		assert.Equal(t, 15, cfg.MaxAttemptsForTransientError)
		assert.Equal(t, 10, cfg.MaxAttemptsForFatalError)
		assert.Equal(t, 200, cfg.MaxRateLimitErrorWaitTimeSeconds)
		assert.Equal(t, 60, cfg.MaxTransientErrorWaitTimeSeconds)
		assert.Equal(t, 45, cfg.MaxFatalErrorWaitTimeSeconds)
		assert.Equal(t, 60, cfg.HTTPTimeoutSeconds)
	})

	t.Run("does not modify non-numeric fields", func(t *testing.T) {
		t.Parallel()

		cfg := &config.SlackClientConfig{
			AppToken:     "xapp-test",
			BotToken:     "xoxb-test",
			DebugLogging: true,
			DryRun:       true,
		}
		cfg.SetDefaults()

		assert.Equal(t, "xapp-test", cfg.AppToken)
		assert.Equal(t, "xoxb-test", cfg.BotToken)
		assert.True(t, cfg.DebugLogging)
		assert.True(t, cfg.DryRun)
	})
}

func TestSlackClientConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		modify      func(*config.SlackClientConfig)
		expectError string
	}{
		{
			name:        "valid config",
			modify:      func(c *config.SlackClientConfig) {},
			expectError: "",
		},
		{
			name: "empty app token",
			modify: func(c *config.SlackClientConfig) {
				c.AppToken = ""
			},
			expectError: "app token is empty",
		},
		{
			name: "empty bot token",
			modify: func(c *config.SlackClientConfig) {
				c.BotToken = ""
			},
			expectError: "bot token is empty",
		},
		{
			name: "zero http timeout",
			modify: func(c *config.SlackClientConfig) {
				c.HTTPTimeoutSeconds = 0
			},
			expectError: "http timeout must be greater than 0",
		},
		{
			name: "negative http timeout",
			modify: func(c *config.SlackClientConfig) {
				c.HTTPTimeoutSeconds = -1
			},
			expectError: "http timeout must be greater than 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := validSlackClientConfig()
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

func TestSlackClientConfig_Validate_Order(t *testing.T) {
	t.Parallel()

	t.Run("app token checked before bot token", func(t *testing.T) {
		t.Parallel()

		cfg := &config.SlackClientConfig{
			AppToken:           "",
			BotToken:           "",
			HTTPTimeoutSeconds: 30,
		}

		err := cfg.Validate()

		require.Error(t, err)
		assert.Equal(t, "app token is empty", err.Error())
	})

	t.Run("bot token checked before http timeout", func(t *testing.T) {
		t.Parallel()

		cfg := &config.SlackClientConfig{
			AppToken:           "xapp-test",
			BotToken:           "",
			HTTPTimeoutSeconds: 0,
		}

		err := cfg.Validate()

		require.Error(t, err)
		assert.Equal(t, "bot token is empty", err.Error())
	})
}

func validSlackClientConfig() *config.SlackClientConfig {
	cfg := config.NewDefaultSlackClientConfig()
	cfg.AppToken = "xapp-test-token"
	cfg.BotToken = "xoxb-test-token"
	return cfg
}
