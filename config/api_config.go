package config

import (
	"errors"
	"fmt"
	"time"
)

// APIConfig holds configuration for the Slack Manager REST API.
// The configuration here are for basic app settings. They cannot be changed after startup.
type APIConfig struct {
	// LogJSON indicates whether to log in JSON format.
	LogJSON bool `json:"logJson" yaml:"logJson"`

	// Verbose indicates whether to enable verbose logging.
	Verbose bool `json:"verbose" yaml:"verbose"`

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

	// RateLimitPerAlertChannel holds rate limiting configuration per alert channel.
	RateLimitPerAlertChannel *RateLimitConfig `json:"rateLimitPerAlertChannel" yaml:"rateLimitPerAlertChannel"`

	// SlackClient holds configuration for the Slack client.
	SlackClient *SlackClientConfig `json:"slackClient" yaml:"slackClient"`
}

// RateLimitConfig holds rate limiting configuration.
type RateLimitConfig struct {
	// AlertsPerSecond is the number of alerts allowed per second.
	AlertsPerSecond float64 `json:"alertsPerSecond" yaml:"alertsPerSecond"`

	// AllowedBurst is the maximum burst size.
	AllowedBurst int `json:"allowedBurst" yaml:"allowedBurst"`

	// MaxRequestWaitTime is the maximum time a request will wait for available rate limit tokens.
	MaxRequestWaitTime time.Duration `json:"maxRequestWaitTime" yaml:"maxRequestWaitTime"`
}

// NewDefaultAPIConfig returns an APIConfig with default values.
func NewDefaultAPIConfig() *APIConfig {
	return &APIConfig{
		LogJSON:                true,
		Verbose:                false,
		RestPort:               "8080",
		EncryptionKey:          "",
		CacheKeyPrefix:         "slack-manager:",
		ErrorReportChannelID:   "",
		MaxUsersInAlertChannel: 100,
		RateLimitPerAlertChannel: &RateLimitConfig{
			AlertsPerSecond:    1,
			AllowedBurst:       30,
			MaxRequestWaitTime: 15 * time.Second,
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
