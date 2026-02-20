package config_test

import (
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
	assert.False(t, cfg.SkipDatabaseCache)
	assert.Equal(t, time.UTC, cfg.Location)
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
		// Location validation
		{
			name: "nil location",
			modify: func(c *config.ManagerConfig) {
				c.Location = nil
			},
			expectError: "location is required",
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
		cfg.Location = time.UTC
		assert.NoError(t, cfg.Validate())
	})

	t.Run("custom fixed zone", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.Location = time.FixedZone("PST", -8*60*60)
		assert.NoError(t, cfg.Validate())
	})

	t.Run("fixed zone location", func(t *testing.T) {
		t.Parallel()
		cfg := validManagerConfig()
		cfg.Location = time.FixedZone("EST", -5*60*60)
		assert.NoError(t, cfg.Validate())
	})

	t.Run("loaded timezone", func(t *testing.T) {
		t.Parallel()
		loc, err := time.LoadLocation("America/New_York")
		require.NoError(t, err)

		cfg := validManagerConfig()
		cfg.Location = loc
		assert.NoError(t, cfg.Validate())
	})
}

func TestManagerConfig_Validate_Order(t *testing.T) {
	t.Parallel()

	t.Run("encryption key checked before location", func(t *testing.T) {
		t.Parallel()

		cfg := validManagerConfig()
		cfg.EncryptionKey = "invalid"
		cfg.Location = nil

		err := cfg.Validate()

		require.Error(t, err)
		assert.Equal(t, "encryption key must be a 32 character alphanumeric string", err.Error())
	})

	t.Run("location checked before slack client", func(t *testing.T) {
		t.Parallel()

		cfg := validManagerConfig()
		cfg.Location = nil
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
