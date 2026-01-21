package config

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Validation constants for APIConfig.
const (
	// EncryptionKeyLength is the required length for the encryption key.
	EncryptionKeyLength = 32

	// MinRestPort is the minimum valid port number.
	MinRestPort = 1
	// MaxRestPort is the maximum valid port number.
	MaxRestPort = 65535

	// MinMaxUsersInAlertChannel is the minimum allowed value for MaxUsersInAlertChannel.
	MinMaxUsersInAlertChannel = 1
	// MaxMaxUsersInAlertChannel is the maximum allowed value for MaxUsersInAlertChannel.
	MaxMaxUsersInAlertChannel = 10000
)

// Validation constants for RateLimitConfig.
const (
	// MinAlertsPerSecond is the minimum allowed alerts per second rate.
	MinAlertsPerSecond = 0.001
	// MaxAlertsPerSecond is the maximum allowed alerts per second rate.
	MaxAlertsPerSecond = 1000

	// MinAllowedBurst is the minimum allowed burst size.
	MinAllowedBurst = 1
	// MaxAllowedBurst is the maximum allowed burst size.
	MaxAllowedBurst = 10000

	// MinMaxRequestWaitTime is the minimum allowed request wait time.
	MinMaxRequestWaitTime = 1 * time.Second
	// MaxMaxRequestWaitTime is the maximum allowed request wait time.
	MaxMaxRequestWaitTime = 5 * time.Minute
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

// Validate validates the RateLimitConfig and returns an error if any fields are invalid.
func (c *RateLimitConfig) Validate() error {
	if c.AlertsPerSecond < MinAlertsPerSecond || c.AlertsPerSecond > MaxAlertsPerSecond {
		return fmt.Errorf("alerts per second must be between %v and %v", MinAlertsPerSecond, MaxAlertsPerSecond)
	}

	if c.AllowedBurst < MinAllowedBurst || c.AllowedBurst > MaxAllowedBurst {
		return fmt.Errorf("allowed burst must be between %d and %d", MinAllowedBurst, MaxAllowedBurst)
	}

	if c.MaxRequestWaitTime < MinMaxRequestWaitTime || c.MaxRequestWaitTime > MaxMaxRequestWaitTime {
		return fmt.Errorf("max request wait time must be between %v and %v", MinMaxRequestWaitTime, MaxMaxRequestWaitTime)
	}

	return nil
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
	if err := c.validateRestPort(); err != nil {
		return err
	}

	if !encryptionKeyRegex.MatchString(c.EncryptionKey) {
		return fmt.Errorf("encryption key must be a %d character alphanumeric string", EncryptionKeyLength)
	}

	if c.CacheKeyPrefix == "" {
		return errors.New("cache key prefix is required")
	}

	if c.MaxUsersInAlertChannel < MinMaxUsersInAlertChannel || c.MaxUsersInAlertChannel > MaxMaxUsersInAlertChannel {
		return fmt.Errorf("max users in alert channel must be between %d and %d", MinMaxUsersInAlertChannel, MaxMaxUsersInAlertChannel)
	}

	if c.RateLimitPerAlertChannel == nil {
		return errors.New("rate limit config is required")
	}

	if err := c.RateLimitPerAlertChannel.Validate(); err != nil {
		return fmt.Errorf("rate limit config is invalid: %w", err)
	}

	if c.SlackClient == nil {
		return errors.New("slack client config is required")
	}

	if err := c.SlackClient.Validate(); err != nil {
		return fmt.Errorf("slack client config is invalid: %w", err)
	}

	return nil
}

// validateRestPort validates that the REST port is a valid port number.
func (c *APIConfig) validateRestPort() error {
	if c.RestPort == "" {
		return errors.New("rest port is required")
	}

	port, err := strconv.Atoi(c.RestPort)
	if err != nil {
		return errors.New("rest port must be a valid number")
	}

	if port < MinRestPort || port > MaxRestPort {
		return fmt.Errorf("rest port must be between %d and %d", MinRestPort, MaxRestPort)
	}

	return nil
}
