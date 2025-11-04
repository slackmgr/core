package config

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

var encryptionKeyRegex = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)

// ManagerConfig holds configuration for the Slack Manager main application (not the API).
// The configuration here are for basic app settings. They cannot be changed after startup.
type ManagerConfig struct {
	// WebhookTimeoutSeconds is the timeout in seconds for outgoing HTTP webhooks.
	WebhookTimeoutSeconds int `json:"webhookTimeoutSeconds" yaml:"webhookTimeoutSeconds"`

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
}

// NewDefaultManagerConfig returns a ManagerConfig with default values.
func NewDefaultManagerConfig() *ManagerConfig {
	return &ManagerConfig{
		WebhookTimeoutSeconds: 2,
		CacheKeyPrefix:        "slack-manager:",
		Location:              time.UTC,
		SlackClient:           NewDefaultSlackClientConfig(),
	}
}

// Validate validates the ManagerConfig and returns an error if any required fields are missing or invalid.
func (c *ManagerConfig) Validate() error {
	if c.WebhookTimeoutSeconds < 1 || time.Duration(c.WebhookTimeoutSeconds) > 30 {
		return errors.New("webhook timeout must be between 1 and 30 seconds")
	}

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

	return nil
}
