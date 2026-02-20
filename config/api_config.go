package config

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Validation constants for APIConfig fields.
const (
	// EncryptionKeyLength is the required length for the encryption key.
	// The key must be exactly 32 alphanumeric characters to work with AES-256 encryption.
	EncryptionKeyLength = 32

	// MinRestPort is the minimum valid TCP port number. Port 0 is reserved and cannot be used.
	MinRestPort = 1

	// MaxRestPort is the maximum valid TCP port number. Ports above 65535 do not exist
	// in the TCP/IP protocol.
	MaxRestPort = 65535

	// MinMaxUsersInAlertChannel is the minimum allowed value for MaxUsersInAlertChannel.
	// At least one user must be allowed in a channel for alerts to be useful.
	MinMaxUsersInAlertChannel = 1

	// MaxMaxUsersInAlertChannel is the maximum allowed value for MaxUsersInAlertChannel.
	// This limit prevents accidental alerts to large public channels which could cause
	// spam and excessive Slack API usage. The value of 10,000 accommodates most
	// legitimate use cases while still providing protection.
	MaxMaxUsersInAlertChannel = 10000
)

// Validation constants for RateLimitConfig fields.
// These limits ensure the rate limiter operates within reasonable bounds.
const (
	// MinAlertsPerSecond is the minimum token refill rate.
	// A value of 0.001 means 1 token per 1000 seconds (~16.7 minutes), which is
	// useful for very strict rate limiting during incidents or testing.
	MinAlertsPerSecond = 0.001

	// MaxAlertsPerSecond is the maximum token refill rate.
	// A value of 1000 allows up to 1000 alerts per second per channel, which is
	// more than sufficient for any realistic alerting workload.
	MaxAlertsPerSecond = 1000

	// MinAllowedBurst is the minimum bucket capacity.
	// At least 1 token must be allowed, otherwise no alerts could ever be sent.
	MinAllowedBurst = 1

	// MaxAllowedBurst is the maximum bucket capacity.
	// A burst of 10,000 alerts allows handling large batch imports or incident
	// recovery scenarios while still providing meaningful rate protection.
	MaxAllowedBurst = 10000

	// MinMaxRequestWaitTime is the minimum time a request will wait for tokens.
	// At least 1 second is required to allow the rate limiter to function.
	MinMaxRequestWaitTime = 1 * time.Second

	// MaxMaxRequestWaitTime is the maximum time a request will wait for tokens.
	// Waiting longer than 5 minutes is impractical as HTTP clients and load
	// balancers typically have shorter timeouts.
	MaxMaxRequestWaitTime = 5 * time.Minute
)

// APIConfig holds configuration for the Slack Manager REST API server.
//
// These settings are read at startup and cannot be changed without restarting the server.
// For runtime-configurable settings, see APISettings which can be reloaded dynamically.
type APIConfig struct {
	// LogJSON controls the log output format. When true, logs are written as structured
	// JSON objects suitable for log aggregation systems like Elasticsearch or Splunk.
	// When false, logs are written in a human-readable text format for local development.
	LogJSON bool `json:"logJson" yaml:"logJson"`

	// Verbose enables debug-level logging when true. This includes detailed information
	// about request processing, rate limiting decisions, and internal state changes.
	// Should be disabled in production to reduce log volume and avoid exposing
	// sensitive information.
	Verbose bool `json:"verbose" yaml:"verbose"`

	// RestPort specifies the TCP port the HTTP server listens on. The value is stored
	// as a string to allow easy configuration from environment variables. Common values
	// are "8080" for development and "80" or "443" for production (though TLS termination
	// is typically handled by a reverse proxy).
	RestPort string `json:"restPort" yaml:"restPort"`

	// EncryptionKey is a 32-character alphanumeric key used for AES-256 encryption of
	// sensitive data such as webhook payloads. This key must be identical in both the
	// API and Manager configurations to ensure encrypted data can be decrypted correctly.
	// Generate a secure random key for production use; never use predictable values.
	EncryptionKey string `json:"encryptionKey" yaml:"encryptionKey"`

	// CacheKeyPrefix is prepended to all Redis cache keys to namespace them. Using the
	// same prefix in both the API and Manager allows them to share cached data (such as
	// channel information), reducing Slack API calls. Use different prefixes if running
	// multiple independent Slack Manager instances against the same Redis cluster.
	CacheKeyPrefix string `json:"cacheKeyPrefix" yaml:"cacheKeyPrefix"`

	// ErrorReportChannelID is the Slack channel where the API posts error reports for
	// failed requests (4xx and 5xx responses). This is useful for monitoring API health
	// and debugging integration issues. Leave empty to disable error reporting.
	// The channel must exist and the bot must be a member.
	ErrorReportChannelID string `json:"errorReportChannelId" yaml:"errorReportChannelId"`

	// MaxUsersInAlertChannel sets the maximum number of members allowed in a channel
	// that receives alerts. The API returns HTTP 400 if an alert targets a channel
	// exceeding this limit. This prevents accidental alerts to large public channels
	// (like #general) which could spam many users and trigger Slack API rate limits.
	MaxUsersInAlertChannel int `json:"maxUsersInAlertChannel" yaml:"maxUsersInAlertChannel"`

	// RateLimitPerAlertChannel configures the token bucket rate limiter that controls
	// how many alerts can be sent to each Slack channel. Each channel has its own
	// independent rate limiter. See RateLimitConfig for detailed documentation.
	RateLimitPerAlertChannel *RateLimitConfig `json:"rateLimitPerAlertChannel" yaml:"rateLimitPerAlertChannel"`

	// SlackClient contains configuration for connecting to the Slack API, including
	// authentication tokens and API behavior settings.
	SlackClient *SlackClientConfig `json:"slackClient" yaml:"slackClient"`
}

// RateLimitConfig configures the token bucket rate limiter for alert processing.
//
// # Token Bucket Algorithm
//
// The rate limiter uses a token bucket algorithm, which can be visualized as a bucket
// that holds tokens. Each alert consumes one token from the bucket. The bucket has
// two key properties:
//
//   - Capacity (AllowedBurst): The maximum number of tokens the bucket can hold.
//   - Refill Rate (AlertsPerSecond): How fast tokens are added back to the bucket.
//
// # How It Works
//
// 1. The bucket starts full with AllowedBurst tokens.
// 2. Each alert request removes one token from the bucket.
// 3. Tokens are continuously added at the AlertsPerSecond rate, up to the maximum capacity.
// 4. If the bucket is empty, requests must wait for tokens to be added.
//
// # Example Configuration
//
// With AlertsPerSecond=1 and AllowedBurst=30:
//   - A burst of up to 30 alerts can be sent immediately (draining the bucket).
//   - After the burst, alerts are limited to 1 per second (the refill rate).
//   - If no alerts are sent for 30 seconds, the bucket refills completely.
//
// # Choosing Values
//
//   - AllowedBurst: Set this to handle expected traffic spikes. For example, if your
//     monitoring system sends 20 alerts during an incident, set AllowedBurst >= 20.
//   - AlertsPerSecond: Set this to the sustained rate you want to allow. A value of 1
//     means one alert per second during continuous traffic.
//
// The combination allows short bursts while preventing sustained flooding.
type RateLimitConfig struct {
	// AlertsPerSecond controls the token refill rate - how many tokens are added to
	// the bucket per second. This determines the sustained alert rate after a burst
	// has depleted the bucket. For example:
	//   - 1.0 = one alert per second (one token added every second)
	//   - 0.5 = one alert every 2 seconds (one token added every 2 seconds)
	//   - 10.0 = ten alerts per second (ten tokens added every second)
	//
	// This value can be fractional to allow rates slower than 1 per second.
	AlertsPerSecond float64 `json:"alertsPerSecond" yaml:"alertsPerSecond"`

	// AllowedBurst sets the token bucket capacity - the maximum number of tokens that
	// can accumulate. This determines how many alerts can be sent in a rapid burst
	// before rate limiting kicks in. The bucket starts full and refills over time
	// when not in use.
	//
	// This value also serves as the maximum number of alerts allowed in a single API
	// request. Requests exceeding this limit are rejected with HTTP 400 immediately,
	// without waiting for rate limit tokens.
	AllowedBurst int `json:"allowedBurst" yaml:"allowedBurst"`

	// MaxRequestWaitTime is the maximum duration an API request will wait for rate
	// limit tokens to become available. If tokens are not available within this time,
	// the request fails with HTTP 429 (Too Many Requests).
	//
	// For example, with AlertsPerSecond=1 and an empty bucket, a request for 5 alerts
	// would need to wait ~5 seconds for enough tokens. If MaxRequestWaitTime is 3s,
	// the request would fail after 3 seconds rather than waiting the full 5 seconds.
	//
	// Set this based on your HTTP client timeout and user experience requirements.
	// Longer waits reduce rejected requests but increase response latency.
	MaxRequestWaitTime time.Duration `json:"maxRequestWaitTime" yaml:"maxRequestWaitTime"`
}

// NewDefaultAPIConfig returns an APIConfig populated with sensible default values.
//
// The defaults are configured for a typical production deployment:
//   - JSON logging enabled for log aggregation compatibility
//   - Verbose logging disabled to reduce noise
//   - Port 8080 (standard non-privileged HTTP port)
//   - Rate limiting: 1 alert/second sustained, 30 alert burst capacity, 15s max wait
//   - Max 100 users per alert channel (prevents accidental spam to large channels)
//
// The EncryptionKey is intentionally left empty and must be set before use.
// The ErrorReportChannelID is also empty; set it to enable error reporting.
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

// Validate checks that all required fields are present and all values are within
// acceptable ranges. It returns a descriptive error for the first validation failure
// encountered, or nil if the configuration is valid.
//
// Validation includes:
//   - RestPort: must be a valid port number (1-65535)
//   - EncryptionKey: if non-empty, must be exactly 32 alphanumeric characters
//   - CacheKeyPrefix: must not be empty
//   - MaxUsersInAlertChannel: must be between 1 and 10,000
//   - RateLimitPerAlertChannel: must not be nil, and all fields must be valid
//   - SlackClient: must not be nil, and must pass its own validation
func (c *APIConfig) Validate() error {
	if err := c.validateRestPort(); err != nil {
		return err
	}

	if c.EncryptionKey != "" && !encryptionKeyRegex.MatchString(c.EncryptionKey) {
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

	if err := c.RateLimitPerAlertChannel.validate(); err != nil {
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

// validateRestPort checks that RestPort is a non-empty string containing a valid
// TCP port number (1-65535). Port 0 is reserved and not allowed.
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

// validate checks that all rate limit configuration values are within acceptable ranges.
// Returns a descriptive error for the first validation failure, or nil if valid.
//
// Validation ensures:
//   - AlertsPerSecond is between 0.001 and 1000 (allowing very slow to very fast rates)
//   - AllowedBurst is between 1 and 10,000 (at least one alert must be allowed)
//   - MaxRequestWaitTime is between 1 second and 5 minutes (practical timeout range)
func (c *RateLimitConfig) validate() error {
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
