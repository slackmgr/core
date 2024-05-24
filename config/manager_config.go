package config

import (
	"fmt"
	"time"
)

type ManagerConfig struct {
	WebhookTimeout        time.Duration      `json:"webhookTimeout"        yaml:"webhookTimeout"`
	EncryptionKey         string             `json:"encryptionKey"         yaml:"encryptionKey"`
	CachePrefix           string             `json:"cachePrefix"           yaml:"cachePrefix"`
	IgnoreCacheReadErrors bool               `json:"ignoreCacheReadErrors" yaml:"ignoreCacheReadErrors"`
	Location              *time.Location     `json:"location"              yaml:"location"`
	SlackClient           *SlackClientConfig `json:"slackClient"           yaml:"slackClient"`
}

func NewDefaultManagerConfig() *ManagerConfig {
	return &ManagerConfig{
		WebhookTimeout:        2 * time.Second,
		CachePrefix:           "slack-manager",
		IgnoreCacheReadErrors: true,
		Location:              time.UTC,
		SlackClient:           NewDefaultSlackClientConfig(),
	}
}

func (c *ManagerConfig) Validate() error {
	if c.WebhookTimeout < 1*time.Second || c.WebhookTimeout > 30*time.Second {
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
