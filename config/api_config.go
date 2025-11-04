package config

import (
	"errors"
	"fmt"
)

// APIConfig holds configuration for the Slack Manager REST API.
// The configuration here are for basic app settings. They cannot be changed after startup.
type APIConfig struct {
	// RestPort is the port the REST API server listens on.
	RestPort string `json:"restPort" yaml:"restPort"`

	// EncryptionKey is the key used to encrypt sensitive data. It must be the same in the API and Manager configs.
	EncryptionKey string `json:"encryptionKey" yaml:"encryptionKey"`

	// CacheKeyPrefix is the prefix used for all cache keys. Use the same as in the Manager config to share the cache (recommended).
	CacheKeyPrefix string `json:"cacheKeyPrefix" yaml:"cacheKeyPrefix"`

	// ErrorReportChannelID is the Slack channel ID where error reports are sent. If empty, error reports are not sent.
	ErrorReportChannelID string `json:"errorReportChannelId" yaml:"errorReportChannelId"`

	// MaxUsersInAlertChannel is the maximum number of users allowed in an alert channel. If the number of users exceeds this value, the API will return 400 when posting alerts to the channel.
	MaxUsersInAlertChannel int `json:"maxUsersInAlertChannel" yaml:"maxUsersInAlertChannel"`

	// RateLimit holds configuration for API rate limiting.
	RateLimit *RateLimitConfig `json:"rateLimit" yaml:"rateLimit"`

	// SlackClient holds configuration for the Slack client.
	SlackClient *SlackClientConfig `json:"slackClient" yaml:"slackClient"`
}

type RateLimitConfig struct {
	AlertsPerSecond          float64 `json:"alertsPerSecond"          yaml:"alertsPerSecond"`
	AllowedBurst             int     `json:"allowedBurst"             yaml:"allowedBurst"`
	MaxWaitPerAttemptSeconds int     `json:"maxWaitPerAttemptSeconds" yaml:"maxWaitPerAttemptSeconds"`
	MaxAttempts              int     `json:"maxAttempts"              yaml:"maxAttempts"`
}

// NewDefaultAPIConfig returns an APIConfig with default values.
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

// Validate validates the APIConfig and returns an error if any required fields are missing or invalid.
func (c *APIConfig) Validate() error {
	if c.RestPort == "" {
		return errors.New("rest port is required")
	}

	if !encryptionKeyRegex.MatchString(c.EncryptionKey) {
		return errors.New("the encryption key must be a 32 character alphanumeric string")
	}

	if c.SlackClient == nil {
		return errors.New("slack client config is required")
	}

	if c.MaxUsersInAlertChannel < 1 || c.MaxUsersInAlertChannel > 10000 {
		return errors.New("max users in alert channel must be between 1 and 10000")
	}

	if err := c.SlackClient.Validate(); err != nil {
		return fmt.Errorf("slack client config is invalid: %w", err)
	}

	return nil
}
