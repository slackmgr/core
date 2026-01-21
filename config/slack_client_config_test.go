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
	assert.Equal(t, 10, cfg.MaxAttemptsForRateLimitError)
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
		assert.Equal(t, 10, cfg.MaxAttemptsForRateLimitError)
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
			MaxAttemptsForRateLimitError:     -5,
			MaxAttemptsForTransientError:     -10,
			MaxAttemptsForFatalError:         -3,
			MaxRateLimitErrorWaitTimeSeconds: -100,
			MaxTransientErrorWaitTimeSeconds: -50,
			MaxFatalErrorWaitTimeSeconds:     -25,
			HTTPTimeoutSeconds:               -15,
		}
		cfg.SetDefaults()

		assert.Equal(t, 3, cfg.Concurrency)
		assert.Equal(t, 10, cfg.MaxAttemptsForRateLimitError)
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
			MaxAttemptsForRateLimitError:     20,
			MaxAttemptsForTransientError:     15,
			MaxAttemptsForFatalError:         10,
			MaxRateLimitErrorWaitTimeSeconds: 200,
			MaxTransientErrorWaitTimeSeconds: 60,
			MaxFatalErrorWaitTimeSeconds:     45,
			HTTPTimeoutSeconds:               60,
		}
		cfg.SetDefaults()

		assert.Equal(t, 5, cfg.Concurrency)
		assert.Equal(t, 20, cfg.MaxAttemptsForRateLimitError)
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
			expectError: "http timeout must be between 1 and 300 seconds",
		},
		{
			name: "negative http timeout",
			modify: func(c *config.SlackClientConfig) {
				c.HTTPTimeoutSeconds = -1
			},
			expectError: "http timeout must be between 1 and 300 seconds",
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

func TestSlackClientConfig_Validate_BoundaryValues(t *testing.T) {
	t.Parallel()

	// Concurrency boundaries
	t.Run("concurrency at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.Concurrency = config.MinConcurrency
		assert.NoError(t, cfg.Validate())
	})

	t.Run("concurrency at upper bound", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.Concurrency = config.MaxConcurrency
		assert.NoError(t, cfg.Validate())
	})

	t.Run("concurrency below minimum", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.Concurrency = config.MinConcurrency - 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Equal(t, "concurrency must be between 1 and 50", err.Error())
	})

	t.Run("concurrency above maximum", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.Concurrency = config.MaxConcurrency + 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Equal(t, "concurrency must be between 1 and 50", err.Error())
	})

	// MaxAttemptsForRateLimitError boundaries
	t.Run("max attempts for rate limit at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.MaxAttemptsForRateLimitError = config.MinMaxAttempts
		assert.NoError(t, cfg.Validate())
	})

	t.Run("max attempts for rate limit at upper bound", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.MaxAttemptsForRateLimitError = config.MaxMaxAttempts
		assert.NoError(t, cfg.Validate())
	})

	t.Run("max attempts for rate limit below minimum", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.MaxAttemptsForRateLimitError = config.MinMaxAttempts - 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Equal(t, "max attempts for rate limit error must be between 1 and 100", err.Error())
	})

	t.Run("max attempts for rate limit above maximum", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.MaxAttemptsForRateLimitError = config.MaxMaxAttempts + 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Equal(t, "max attempts for rate limit error must be between 1 and 100", err.Error())
	})

	// MaxAttemptsForTransientError boundaries
	t.Run("max attempts for transient at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.MaxAttemptsForTransientError = config.MinMaxAttempts
		assert.NoError(t, cfg.Validate())
	})

	t.Run("max attempts for transient above maximum", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.MaxAttemptsForTransientError = config.MaxMaxAttempts + 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Equal(t, "max attempts for transient error must be between 1 and 100", err.Error())
	})

	// MaxAttemptsForFatalError boundaries
	t.Run("max attempts for fatal at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.MaxAttemptsForFatalError = config.MinMaxAttempts
		assert.NoError(t, cfg.Validate())
	})

	t.Run("max attempts for fatal above maximum", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.MaxAttemptsForFatalError = config.MaxMaxAttempts + 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Equal(t, "max attempts for fatal error must be between 1 and 100", err.Error())
	})

	// MaxRateLimitErrorWaitTimeSeconds boundaries
	t.Run("max rate limit wait time at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.MaxRateLimitErrorWaitTimeSeconds = config.MinWaitTimeSeconds
		assert.NoError(t, cfg.Validate())
	})

	t.Run("max rate limit wait time at upper bound", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.MaxRateLimitErrorWaitTimeSeconds = config.MaxWaitTimeSeconds
		assert.NoError(t, cfg.Validate())
	})

	t.Run("max rate limit wait time below minimum", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.MaxRateLimitErrorWaitTimeSeconds = config.MinWaitTimeSeconds - 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Equal(t, "max rate limit error wait time must be between 1 and 600 seconds", err.Error())
	})

	t.Run("max rate limit wait time above maximum", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.MaxRateLimitErrorWaitTimeSeconds = config.MaxWaitTimeSeconds + 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Equal(t, "max rate limit error wait time must be between 1 and 600 seconds", err.Error())
	})

	// MaxTransientErrorWaitTimeSeconds boundaries
	t.Run("max transient wait time at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.MaxTransientErrorWaitTimeSeconds = config.MinWaitTimeSeconds
		assert.NoError(t, cfg.Validate())
	})

	t.Run("max transient wait time above maximum", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.MaxTransientErrorWaitTimeSeconds = config.MaxWaitTimeSeconds + 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Equal(t, "max transient error wait time must be between 1 and 600 seconds", err.Error())
	})

	// MaxFatalErrorWaitTimeSeconds boundaries
	t.Run("max fatal wait time at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.MaxFatalErrorWaitTimeSeconds = config.MinWaitTimeSeconds
		assert.NoError(t, cfg.Validate())
	})

	t.Run("max fatal wait time above maximum", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.MaxFatalErrorWaitTimeSeconds = config.MaxWaitTimeSeconds + 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Equal(t, "max fatal error wait time must be between 1 and 600 seconds", err.Error())
	})

	// HTTPTimeoutSeconds boundaries
	t.Run("http timeout at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.HTTPTimeoutSeconds = config.MinHTTPTimeoutSeconds
		assert.NoError(t, cfg.Validate())
	})

	t.Run("http timeout at upper bound", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.HTTPTimeoutSeconds = config.MaxHTTPTimeoutSeconds
		assert.NoError(t, cfg.Validate())
	})

	t.Run("http timeout above maximum", func(t *testing.T) {
		t.Parallel()
		cfg := validSlackClientConfig()
		cfg.HTTPTimeoutSeconds = config.MaxHTTPTimeoutSeconds + 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Equal(t, "http timeout must be between 1 and 300 seconds", err.Error())
	})
}

func validSlackClientConfig() *config.SlackClientConfig {
	cfg := config.NewDefaultSlackClientConfig()
	cfg.AppToken = "xapp-test-token"
	cfg.BotToken = "xoxb-test-token"
	return cfg
}
