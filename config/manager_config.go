package config

import (
	"fmt"
	"time"
)

type ManagerConfig struct {
	WebhookTimeoutSeconds int                `json:"webhookTimeoutSeconds" yaml:"webhookTimeoutSeconds"`
	EncryptionKey         string             `json:"encryptionKey"         yaml:"encryptionKey"`
	CacheKeyPrefix        string             `json:"cacheKeyPrefix"        yaml:"cacheKeyPrefix"`
	IgnoreCacheReadErrors bool               `json:"ignoreCacheReadErrors" yaml:"ignoreCacheReadErrors"`
	Location              *time.Location     `json:"location"              yaml:"location"`
	SlackClient           *SlackClientConfig `json:"slackClient"           yaml:"slackClient"`
}

func NewDefaultManagerConfig() *ManagerConfig {
	return &ManagerConfig{
		WebhookTimeoutSeconds: 2,
		CacheKeyPrefix:        "slack-manager::",
		IgnoreCacheReadErrors: true,
		Location:              time.UTC,
		SlackClient:           NewDefaultSlackClientConfig(),
	}
}

func (c *ManagerConfig) Validate() error {
	if c.WebhookTimeoutSeconds < 1 || time.Duration(c.WebhookTimeoutSeconds) > 30 {
		return fmt.Errorf("webhook timeout must be between 1 and 30 seconds")
	}

	if c.SlackClient == nil {
		return fmt.Errorf("slack client config is required")
	}

	if c.Location == nil {
		return fmt.Errorf("location is required")
	}

	if c.SlackClient == nil {
		return fmt.Errorf("slack client config is required")
	}

	if err := c.SlackClient.Validate(); err != nil {
		return fmt.Errorf("slack client config is invalid: %w", err)
	}

	return nil
}
