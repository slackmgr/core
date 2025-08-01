package config

import (
	"errors"
	"fmt"
)

type RateLimitConfig struct {
	AlertsPerSecond          float64 `json:"alertsPerSecond"          yaml:"alertsPerSecond"`
	AllowedBurst             int     `json:"allowedBurst"             yaml:"allowedBurst"`
	MaxWaitPerAttemptSeconds int     `json:"maxWaitPerAttemptSeconds" yaml:"maxWaitPerAttemptSeconds"`
	MaxAttempts              int     `json:"maxAttempts"              yaml:"maxAttempts"`
}

type APIConfig struct {
	RestPort               string             `json:"restPort"               yaml:"restPort"`
	EncryptionKey          string             `json:"encryptionKey"          yaml:"encryptionKey"`
	CacheKeyPrefix         string             `json:"cacheKeyPrefix"         yaml:"cacheKeyPrefix"`
	ErrorReportChannelID   string             `json:"errorReportChannelId"   yaml:"errorReportChannelId"`
	MaxUsersInAlertChannel int                `json:"maxUsersInAlertChannel" yaml:"maxUsersInAlertChannel"`
	RateLimit              *RateLimitConfig   `json:"rateLimit"              yaml:"rateLimit"`
	SlackClient            *SlackClientConfig `json:"slackClient"            yaml:"slackClient"`
}

func NewDefaultAPIConfig() *APIConfig {
	return &APIConfig{
		RestPort:               "8080",
		EncryptionKey:          "",
		CacheKeyPrefix:         "slack-manager:",
		ErrorReportChannelID:   "",
		MaxUsersInAlertChannel: 100,
		RateLimit: &RateLimitConfig{
			AlertsPerSecond:          0.5,
			AllowedBurst:             10,
			MaxWaitPerAttemptSeconds: 10,
			MaxAttempts:              3,
		},
		SlackClient: NewDefaultSlackClientConfig(),
	}
}

func (c *APIConfig) Validate() error {
	if c.RestPort == "" {
		return errors.New("rest port is required")
	}

	if c.SlackClient == nil {
		return errors.New("slack client config is required")
	}

	if err := c.SlackClient.Validate(); err != nil {
		return fmt.Errorf("slack client config is invalid: %w", err)
	}

	return nil
}
