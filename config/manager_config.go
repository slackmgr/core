package config

import (
	"fmt"
	"time"
)

type ThrottleConfig struct {
	MinIssueCountForThrottle int           `json:"minIssueCountForThrottle" yaml:"minIssueCountForThrottle"`
	UpperLimit               time.Duration `json:"upperLimit"               yaml:"upperLimit"`
}

type ManagerConfig struct {
	ProcessInterval       time.Duration      `json:"processInterval"       yaml:"processInterval"`
	DefaultArchivingDelay time.Duration      `json:"defaultArchivingDelay" yaml:"defaultArchivingDelay"`
	WebhookTimeout        time.Duration      `json:"webhookTimeout"        yaml:"webhookTimeout"`
	ReorderIssueLimit     int                `json:"reorderIssueLimit"     yaml:"reorderIssueLimit"`
	EncryptionKey         string             `json:"encryptionKey"         yaml:"encryptionKey"`
	CachePrefix           string             `json:"cachePrefix"           yaml:"cachePrefix"`
	IgnoreCacheReadErrors bool               `json:"ignoreCacheReadErrors" yaml:"ignoreCacheReadErrors"`
	IgnoreSaveAlertErrors bool               `json:"ignoreSaveAlertErrors" yaml:"ignoreSaveAlertErrors"`
	Location              *time.Location     `json:"location"              yaml:"location"`
	SlackClient           *SlackClientConfig `json:"slackClient"           yaml:"slackClient"`
	Throttle              *ThrottleConfig    `json:"throttle"              yaml:"throttle"`
	DocsURL               string             `json:"docsURL"               yaml:"docsURL"`
}

func NewDefaultManagerConfig() *ManagerConfig {
	return &ManagerConfig{
		ProcessInterval:       10 * time.Second,
		DefaultArchivingDelay: 12 * time.Hour,
		WebhookTimeout:        2 * time.Second,
		ReorderIssueLimit:     30,
		CachePrefix:           "slack-manager",
		IgnoreCacheReadErrors: true,
		IgnoreSaveAlertErrors: true,
		Location:              time.UTC,
		SlackClient:           NewDefaultSlackClientConfig(),
		Throttle: &ThrottleConfig{
			MinIssueCountForThrottle: 5,
			UpperLimit:               90 * time.Second,
		},
	}
}

func (c *ManagerConfig) Validate() error {
	if c.ProcessInterval < 2*time.Second || c.ProcessInterval > time.Minute {
		return fmt.Errorf("process interval must be between 2 seconds and 1 minute")
	}

	if c.DefaultArchivingDelay < 1*time.Minute || c.DefaultArchivingDelay > 30*24*time.Hour {
		return fmt.Errorf("default archiving delay must be between 1 minute and 30 days")
	}

	if c.WebhookTimeout < 1*time.Second || c.WebhookTimeout > 30*time.Second {
		return fmt.Errorf("webhook timeout must be between 1 and 30 seconds")
	}

	if c.ReorderIssueLimit < 1 || c.ReorderIssueLimit > 100 {
		return fmt.Errorf("reorder issue limit must be between 1 and 100")
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

	if c.Throttle == nil {
		return fmt.Errorf("throttle config is required")
	}

	if c.Throttle.MinIssueCountForThrottle < 1 {
		return fmt.Errorf("min issue count for throttle must be at least 1")
	}

	if c.Throttle.UpperLimit < 10*time.Second || c.Throttle.UpperLimit > 5*time.Minute {
		return fmt.Errorf("upper limit must be between 10 seconds and 5 minutes")
	}

	return nil
}
