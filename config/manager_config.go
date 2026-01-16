package config

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

var encryptionKeyRegex = regexp.MustCompile(`^[a-zA-Z0-9]{32}$`)

// minDrainTimeout is the minimum allowed drain timeout.
// This must be at least as long as the internal ack/nack/lock release operation timeouts (2 seconds).
const minDrainTimeout = 2 * time.Second

// ManagerConfig holds configuration for the Slack Manager main application (not the API).
// The configuration here are for basic app settings. They cannot be changed after startup.
type ManagerConfig struct {
	// EncryptionKey is the key used to encrypt sensitive data. It must be the same in the API and Manager configs.
	EncryptionKey string `json:"encryptionKey" yaml:"encryptionKey"`

	// CacheKeyPrefix is the prefix used for all cache keys. Use the same as in the API config to share the cache (recommended).
	CacheKeyPrefix string `json:"cacheKeyPrefix" yaml:"cacheKeyPrefix"`

	// SkipDatabaseCache indicates whether to skip the database cache layer.
	// The default is false, meaning the database cache is used.
	SkipDatabaseCache bool `json:"skipDatabaseCache" yaml:"skipDatabaseCache"`

	// Location is the time.Location used for timestamp parsing and formatting.
	// The default value is time.UTC.
	Location *time.Location `json:"location" yaml:"location"`

	// SlackClient holds configuration for the Slack client.
	SlackClient *SlackClientConfig `json:"slackClient" yaml:"slackClient"`

	// CoordinatorDrainTimeout is the maximum time the coordinator will spend draining
	// messages from its channels during shutdown. Messages that cannot be drained within
	// this timeout will remain in the queue for reprocessing by another instance.
	// Default: 5 seconds.
	CoordinatorDrainTimeout time.Duration `json:"coordinatorDrainTimeout" yaml:"coordinatorDrainTimeout"`

	// ChannelManagerDrainTimeout is the maximum time each channel manager will spend
	// draining messages from its internal buffers during shutdown.
	// Default: 3 seconds.
	ChannelManagerDrainTimeout time.Duration `json:"channelManagerDrainTimeout" yaml:"channelManagerDrainTimeout"`
}

// NewDefaultManagerConfig returns a ManagerConfig with default values.
func NewDefaultManagerConfig() *ManagerConfig {
	return &ManagerConfig{
		CacheKeyPrefix:             "slack-manager:",
		Location:                   time.UTC,
		SlackClient:                NewDefaultSlackClientConfig(),
		CoordinatorDrainTimeout:    5 * time.Second,
		ChannelManagerDrainTimeout: 3 * time.Second,
	}
}

// SetDefaults sets default values for any fields that are not set.
// This is useful when the config is not created using NewDefaultManagerConfig.
func (c *ManagerConfig) SetDefaults() {
	if c.CoordinatorDrainTimeout == 0 {
		c.CoordinatorDrainTimeout = 5 * time.Second
	}

	if c.ChannelManagerDrainTimeout == 0 {
		c.ChannelManagerDrainTimeout = 3 * time.Second
	}
}

// Validate validates the ManagerConfig and returns an error if any required fields are missing or invalid.
func (c *ManagerConfig) Validate() error {
	if !encryptionKeyRegex.MatchString(c.EncryptionKey) {
		return errors.New("the encryption key must be a 32 character alphanumeric string")
	}

	if c.Location == nil {
		return errors.New("location is required")
	}

	if c.SlackClient == nil {
		return errors.New("slack client config is required")
	}

	if err := c.SlackClient.Validate(); err != nil {
		return fmt.Errorf("slack client config is invalid: %w", err)
	}

	if c.CoordinatorDrainTimeout < minDrainTimeout {
		return fmt.Errorf("coordinator drain timeout must be at least %v", minDrainTimeout)
	}

	if c.ChannelManagerDrainTimeout < minDrainTimeout {
		return fmt.Errorf("channel manager drain timeout must be at least %v", minDrainTimeout)
	}

	return nil
}
