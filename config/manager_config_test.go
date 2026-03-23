package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/slackmgr/core/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDefaultManagerConfig(t *testing.T) {
	t.Parallel()

	cfg := config.NewDefaultManagerConfig()

	assert.Empty(t, cfg.EncryptionKey)
	assert.Equal(t, "slack-manager:", cfg.CacheKeyPrefix)
	assert.Equal(t, config.DefaultMetricsPrefix, cfg.MetricsPrefix)
	assert.False(t, cfg.SkipDatabaseCache)
	assert.Equal(t, config.DefaultLocation, cfg.Location)
	require.NotNil(t, cfg.SlackClient)
	assert.Equal(t, config.DefaultCoordinatorDrainTimeout, cfg.CoordinatorDrainTimeout)
	assert.Equal(t, config.DefaultChannelManagerDrainTimeout, cfg.ChannelManagerDrainTimeout)
	assert.Equal(t, config.DefaultSocketModeMaxWorkers, cfg.SocketModeMaxWorkers)
	assert.Equal(t, config.DefaultSocketModeDrainTimeout, cfg.SocketModeDrainTimeout)
}

func TestManagerConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		modify      func(*config.ManagerConfig)
		expectError string
	}{
		{
			name:        "valid config",
			modify:      func(c *config.ManagerConfig) {},
			expectError: "",
		},
		{
			name: "empty encryption key",
			modify: func(c *config.ManagerConfig) {
				c.EncryptionKey = ""
			},
			expectError: "",
		},
		{
			name: "encryption key too short",
			modify: func(c *config.ManagerConfig) {
				c.EncryptionKey = "abc123"
			},
			expectError: "encryption key must be a 32 character alphanumeric string",
		},
		{
			name: "encryption key too long",
			modify: func(c *config.ManagerConfig) {
				c.EncryptionKey = "abcdefghijklmnopqrstuvwxyz1234567890"
			},
			expectError: "encryption key must be a 32 character alphanumeric string",
		},
		{
			name: "encryption key with special characters",
			modify: func(c *config.ManagerConfig) {
				c.EncryptionKey = "abcdefghijklmnopqrstuvwxyz12345!"
			},
			expectError: "encryption key must be a 32 character alphanumeric string",
		},
		{
			name: "encryption key with spaces",
			modify: func(c *config.ManagerConfig) {
				c.EncryptionKey = "abcdefghijklmnopqrstuvwxyz12345 "
			},
			expectError: "encryption key must be a 32 character alphanumeric string",
		},
		// CacheKeyPrefix validation
		{
			name: "empty cache key prefix",
			modify: func(c *config.ManagerConfig) {
				c.CacheKeyPrefix = ""
			},
			expectError: "cache key prefix is required",
		},
		{
			name: "cache key prefix too long",
			modify: func(c *config.ManagerConfig) {
				c.CacheKeyPrefix = strings.Repeat("a", config.MaxCacheKeyPrefixLen+1)
			},
			expectError: "cache key prefix must not exceed 100 characters",
		},
		{
			name: "cache key prefix with space",
			modify: func(c *config.ManagerConfig) {
				c.CacheKeyPrefix = "my prefix:"
			},
			expectError: "cache key prefix may only contain letters, digits, '.', '_', ':', or '-'",
		},
		{
			name: "cache key prefix with at sign",
			modify: func(c *config.ManagerConfig) {
				c.CacheKeyPrefix = "my@prefix:"
			},
			expectError: "cache key prefix may only contain letters, digits, '.', '_', ':', or '-'",
		},
		// MetricsPrefix validation
		{
			name: "empty metrics prefix is valid",
			modify: func(c *config.ManagerConfig) {
				c.MetricsPrefix = ""
			},
			expectError: "",
		},
		{
			name: "metrics prefix too long",
			modify: func(c *config.ManagerConfig) {
				c.MetricsPrefix = strings.Repeat("a", config.MaxMetricsPrefixLen+1)
			},
			expectError: "metrics prefix must not exceed 64 characters",
		},
		{
			name: "metrics prefix starts with digit",
			modify: func(c *config.ManagerConfig) {
				c.MetricsPrefix = "1invalid_"
			},
			expectError: "metrics prefix must start with a letter or underscore and contain only letters, digits, or underscores",
		},
		{
			name: "metrics prefix contains hyphen",
			modify: func(c *config.ManagerConfig) {
				c.MetricsPrefix = "slackmgr-"
			},
			expectError: "metrics prefix must start with a letter or underscore and contain only letters, digits, or underscores",
		},
		{
			name: "metrics prefix contains colon",
			modify: func(c *config.ManagerConfig) {
				c.MetricsPrefix = "slackmgr:"
			},
			expectError: "metrics prefix must start with a letter or underscore and contain only letters, digits, or underscores",
		},
		// Location validation
		{
			name: "empty location",
			modify: func(c *config.ManagerConfig) {
				c.Location = ""
			},
			expectError: "location is required",
		},
		{
			name: "invalid location",
			modify: func(c *config.ManagerConfig) {
				c.Location = "Not/A/Timezone"
			},
			expectError: `location "Not/A/Timezone" is not a valid IANA timezone: unknown time zone Not/A/Timezone`,
		},
		{
			name: "nil slack client config",
			modify: func(c *config.ManagerConfig) {
				c.SlackClient = nil
			},
			expectError: "slack client config is required",
		},
		{
			name: "invalid slack client config - missing app token",
			modify: func(c *config.ManagerConfig) {
				c.SlackClient.AppToken = ""
			},
			expectError: "slack client config is invalid: app token is empty",
		},
		{
			name: "invalid slack client config - missing bot token",
			modify: func(c *config.ManagerConfig) {
				c.SlackClient.AppToken = "xapp-test-token"
				c.SlackClient.BotToken = ""
			},
			expectError: "slack client config is invalid: bot token is empty",
		},
		{
			name: "coordinator drain timeout too short",
			modify: func(c *config.ManagerConfig) {
				c.CoordinatorDrainTimeout = 1 * time.Second
			},
			expectError: "coordinator drain timeout must be between 2s and 5m0s",
		},
		{
			name: "channel manager drain timeout too short",
			modify: func(c *config.ManagerConfig) {
				c.ChannelManagerDrainTimeout = 1 * time.Second
			},
			expectError: "channel manager drain timeout must be between 2s and 5m0s",
		},
		{
			name: "drain timeouts at minimum are valid",
			modify: func(c *config.ManagerConfig) {
				c.CoordinatorDrainTimeout = 2 * time.Second
				c.ChannelManagerDrainTimeout = 2 * time.Second
			},
			expectError: "",
		},
		// SocketModeMaxWorkers validation
		{
			name: "socket mode max workers below minimum",
			modify: func(c *config.ManagerConfig) {
				c.SocketModeMaxWorkers = config.MinSocketModeMaxWorkers - 1
			},
			expectError: "socket mode max workers must be between 10 and 1000",
		},
		{
			name: "socket mode max workers above maximum",
			modify: func(c *config.ManagerConfig) {
				c.SocketModeMaxWorkers = config.MaxSocketModeMaxWorkers + 1
			},
			expectError: "socket mode max workers must be between 10 and 1000",
		},
		{
			name: "socket mode max workers at minimum is valid",
			modify: func(c *config.ManagerConfig) {
				c.SocketModeMaxWorkers = config.MinSocketModeMaxWorkers
			},
			expectError: "",
		},
		{
			name: "socket mode max workers at maximum is valid",
			modify: func(c *config.ManagerConfig) {
				c.SocketModeMaxWorkers = config.MaxSocketModeMaxWorkers
			},
			expectError: "",
		},
		// SocketModeDrainTimeout validation
		{
			name: "socket mode drain timeout too short",
			modify: func(c *config.ManagerConfig) {
				c.SocketModeDrainTimeout = 1 * time.Second
			},
			expectError: "socket mode drain timeout must be between 2s and 5m0s",
		},
		{
			name: "socket mode drain timeout too long",
			modify: func(c *config.ManagerConfig) {
				c.SocketModeDrainTimeout = config.MaxDrainTimeout + 1
			},
			expectError: "socket mode drain timeout must be between 2s and 5m0s",
		},
		{
			name: "socket mode drain timeout at minimum is valid",
			modify: func(c *config.ManagerConfig) {
				c.SocketModeDrainTimeout = config.MinDrainTimeout
			},
			expectError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := validManagerConfig()
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

func TestManagerConfig_Validate_EncryptionKeyVariations(t *testing.T) {
	t.Parallel()

	t.Run("lowercase alphanumeric", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.EncryptionKey = "abcdefghijklmnopqrstuvwxyz123456"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("uppercase alphanumeric", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.EncryptionKey = "ABCDEFGHIJKLMNOPQRSTUVWXYZ123456"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("mixed case alphanumeric", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.EncryptionKey = "AbCdEfGhIjKlMnOpQrStUvWxYz123456"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("all digits", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.EncryptionKey = "12345678901234567890123456789012"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("all letters", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.EncryptionKey = "abcdefghijklmnopqrstuvwxyzABCDEF"
		assert.NoError(t, cfg.Validate())
	})
}

func TestManagerConfig_Validate_LocationVariations(t *testing.T) {
	t.Parallel()

	t.Run("UTC location", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.Location = "UTC"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("America/New_York", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.Location = "America/New_York"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("Europe/London", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.Location = "Europe/London"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("Local timezone", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.Location = "Local"
		assert.NoError(t, cfg.Validate())
	})
}

func TestManagerConfig_Validate_Order(t *testing.T) {
	t.Parallel()

	t.Run("encryption key checked before location", func(t *testing.T) {
		t.Parallel()

		cfg := validManagerConfig()
		cfg.EncryptionKey = "invalid"
		cfg.Location = ""

		err := cfg.Validate()

		require.Error(t, err)
		assert.Equal(t, "encryption key must be a 32 character alphanumeric string", err.Error())
	})

	t.Run("location checked before slack client", func(t *testing.T) {
		t.Parallel()

		cfg := validManagerConfig()
		cfg.Location = ""
		cfg.SlackClient = nil

		err := cfg.Validate()

		require.Error(t, err)
		assert.Equal(t, "location is required", err.Error())
	})

	t.Run("slack client nil checked before validation", func(t *testing.T) {
		t.Parallel()

		cfg := validManagerConfig()
		cfg.SlackClient = nil

		err := cfg.Validate()

		require.Error(t, err)
		assert.Equal(t, "slack client config is required", err.Error())
	})
}

func TestManagerConfig_Validate_PrefixVariations(t *testing.T) {
	t.Parallel()

	// CacheKeyPrefix valid variants
	t.Run("cache key prefix with colon separator", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.CacheKeyPrefix = "slack-manager:"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("cache key prefix with underscore and dot", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.CacheKeyPrefix = "my_app.v2:"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("cache key prefix at max length", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.CacheKeyPrefix = strings.Repeat("a", config.MaxCacheKeyPrefixLen)
		assert.NoError(t, cfg.Validate())
	})

	// MetricsPrefix valid variants
	t.Run("metrics prefix with trailing underscore", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.MetricsPrefix = "slackmgr_"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("metrics prefix starting with underscore", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.MetricsPrefix = "_internal_"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("metrics prefix with digits in body", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.MetricsPrefix = "myapp2_"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("metrics prefix at max length", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.MetricsPrefix = strings.Repeat("a", config.MaxMetricsPrefixLen)
		assert.NoError(t, cfg.Validate())
	})
}

func TestManagerConfig_OptionalFields(t *testing.T) {
	t.Parallel()

	t.Run("skip database cache true is valid", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.SkipDatabaseCache = true
		assert.NoError(t, cfg.Validate())
	})

	t.Run("skip database cache false is valid", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.SkipDatabaseCache = false
		assert.NoError(t, cfg.Validate())
	})
}

func TestManagerConfig_Validate_BoundaryValues(t *testing.T) {
	t.Parallel()

	// CoordinatorDrainTimeout boundaries
	t.Run("coordinator drain timeout at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.CoordinatorDrainTimeout = config.MinDrainTimeout
		assert.NoError(t, cfg.Validate())
	})

	t.Run("coordinator drain timeout at upper bound", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.CoordinatorDrainTimeout = config.MaxDrainTimeout
		assert.NoError(t, cfg.Validate())
	})

	t.Run("coordinator drain timeout exceeds maximum", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.CoordinatorDrainTimeout = config.MaxDrainTimeout + 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Equal(t, "coordinator drain timeout must be between 2s and 5m0s", err.Error())
	})

	// ChannelManagerDrainTimeout boundaries
	t.Run("channel manager drain timeout at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.ChannelManagerDrainTimeout = config.MinDrainTimeout
		assert.NoError(t, cfg.Validate())
	})

	t.Run("channel manager drain timeout at upper bound", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.ChannelManagerDrainTimeout = config.MaxDrainTimeout
		assert.NoError(t, cfg.Validate())
	})

	t.Run("channel manager drain timeout exceeds maximum", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.ChannelManagerDrainTimeout = config.MaxDrainTimeout + 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Equal(t, "channel manager drain timeout must be between 2s and 5m0s", err.Error())
	})

	// SocketModeMaxWorkers boundaries
	t.Run("socket mode max workers at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.SocketModeMaxWorkers = config.MinSocketModeMaxWorkers
		assert.NoError(t, cfg.Validate())
	})

	t.Run("socket mode max workers at upper bound", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.SocketModeMaxWorkers = config.MaxSocketModeMaxWorkers
		assert.NoError(t, cfg.Validate())
	})

	t.Run("socket mode max workers below minimum", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.SocketModeMaxWorkers = config.MinSocketModeMaxWorkers - 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Equal(t, "socket mode max workers must be between 10 and 1000", err.Error())
	})

	t.Run("socket mode max workers exceeds maximum", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.SocketModeMaxWorkers = config.MaxSocketModeMaxWorkers + 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Equal(t, "socket mode max workers must be between 10 and 1000", err.Error())
	})

	// SocketModeDrainTimeout boundaries
	t.Run("socket mode drain timeout at lower bound", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.SocketModeDrainTimeout = config.MinDrainTimeout
		assert.NoError(t, cfg.Validate())
	})

	t.Run("socket mode drain timeout at upper bound", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.SocketModeDrainTimeout = config.MaxDrainTimeout
		assert.NoError(t, cfg.Validate())
	})

	t.Run("socket mode drain timeout exceeds maximum", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.SocketModeDrainTimeout = config.MaxDrainTimeout + 1
		err := cfg.Validate()
		require.Error(t, err)
		assert.Equal(t, "socket mode drain timeout must be between 2s and 5m0s", err.Error())
	})
}

func validManagerConfig() *config.ManagerConfig {
	cfg := config.NewDefaultManagerConfig()
	cfg.EncryptionKey = "abcdefghijklmnopqrstuvwxyz123456"
	cfg.SlackClient.AppToken = "xapp-test-token"
	cfg.SlackClient.BotToken = "xoxb-test-token"
	return cfg
}
