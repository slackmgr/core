package config

import (
	"fmt"
	"time"
)

type RateLimitConfig struct {
	AlertsPerSecond   float64       `json:"alertsPerSecond" yaml:"alertsPerSecond"`
	AllowedBurst      int           `json:"allowedBurst" yaml:"allowedBurst"`
	MaxWaitPerAttempt time.Duration `json:"maxWaitPerAttempt" yaml:"maxWaitPerAttempt"`
	MaxAttempts       int           `json:"maxAttempts" yaml:"maxAttempts"`
}

type APIConfig struct {
	RestPort               string             `json:"restPort" yaml:"restPort"`
	EncryptionKey          string             `json:"encryptionKey" yaml:"encryptionKey"`
	CachePrefix            string             `json:"cachePrefix" yaml:"cachePrefix"`
	ErrorReportChannelID   string             `json:"errorReportChannelID" yaml:"errorReportChannelID"`
	MaxUsersInAlertChannel int                `json:"maxUsersInAlertChannel" yaml:"maxUsersInAlertChannel"`
	RateLimit              *RateLimitConfig   `json:"rateLimit" yaml:"rateLimit"`
	SlackClient            *SlackClientConfig `json:"slackClient" yaml:"slackClient"`
}

func NewDefaultAPIConfig() *APIConfig {
	return &APIConfig{
		RestPort:               "8080",
		EncryptionKey:          "",
		CachePrefix:            "slack-manager",
		ErrorReportChannelID:   "",
		MaxUsersInAlertChannel: 100,
		RateLimit: &RateLimitConfig{
			AlertsPerSecond:   0.5,
			AllowedBurst:      10,
			MaxWaitPerAttempt: 10 * time.Second,
			MaxAttempts:       3,
		},
		SlackClient: NewDefaultSlackClientConfig(),
	}
}

func (c *APIConfig) Validate() error {
	if c.RestPort == "" {
		return fmt.Errorf("rest port is required")
	}

	if c.SlackClient == nil {
		return fmt.Errorf("slack client config is required")
	}

	if err := c.SlackClient.Validate(); err != nil {
		return fmt.Errorf("slack client config is invalid: %w", err)
	}

	return nil
}
