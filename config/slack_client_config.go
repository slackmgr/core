package config

import (
	"errors"
	"fmt"
)

// Default values for SlackClientConfig fields.
// These provide sensible defaults for typical production deployments.
const (
	// DefaultConcurrency is the default number of concurrent Slack API requests.
	// A value of 3 balances throughput with Slack API rate limits. Higher values
	// may trigger more rate limit responses; lower values reduce throughput.
	DefaultConcurrency = 3

	// DefaultMaxAttemptsForRateLimitError is the default number of retry attempts
	// for rate limit (429) errors. Slack rate limits are usually short-lived,
	// so 10 attempts with exponential backoff typically succeeds.
	DefaultMaxAttemptsForRateLimitError = 10

	// DefaultMaxAttemptsForTransientError is the default number of retry attempts
	// for transient errors (network timeouts, 5xx responses). These errors often
	// resolve quickly, so 5 attempts is usually sufficient.
	DefaultMaxAttemptsForTransientError = 5

	// DefaultMaxAttemptsForFatalError is the default number of retry attempts for
	// errors that are typically permanent (4xx except 429). While these usually
	// indicate a problem that won't resolve with retries, a few attempts help
	// handle edge cases like brief authentication issues.
	DefaultMaxAttemptsForFatalError = 5

	// DefaultMaxRateLimitErrorWaitTimeSeconds is the maximum time (in seconds) to
	// wait between retries for rate limit errors. Slack typically indicates a
	// Retry-After header, but this caps the wait to prevent excessive delays.
	DefaultMaxRateLimitErrorWaitTimeSeconds = 120

	// DefaultMaxTransientErrorWaitTimeSeconds is the maximum time (in seconds) to
	// wait between retries for transient errors. Shorter than rate limit waits
	// since transient issues often resolve quickly.
	DefaultMaxTransientErrorWaitTimeSeconds = 30

	// DefaultMaxFatalErrorWaitTimeSeconds is the maximum time (in seconds) to wait
	// between retries for fatal errors. Kept short since fatal errors rarely
	// resolve on their own.
	DefaultMaxFatalErrorWaitTimeSeconds = 30

	// DefaultHTTPTimeoutSeconds is the default HTTP client timeout in seconds.
	// 30 seconds accommodates slow Slack API responses while not hanging indefinitely.
	DefaultHTTPTimeoutSeconds = 30
)

// Validation bounds for SlackClientConfig numeric fields.
// These ensure configuration values are within reasonable operational ranges.
const (
	// MinConcurrency is the minimum allowed concurrency value.
	// At least 1 concurrent request is required to make any API calls.
	MinConcurrency = 1

	// MaxConcurrency is the maximum allowed concurrency value.
	// Higher values risk overwhelming Slack's API and triggering aggressive rate limiting.
	// 50 concurrent requests is more than sufficient for any realistic workload.
	MaxConcurrency = 50

	// MinMaxAttempts is the minimum allowed value for retry attempt fields.
	// At least 1 attempt is required to make any request.
	MinMaxAttempts = 1

	// MaxMaxAttempts is the maximum allowed value for retry attempt fields.
	// More than 100 attempts indicates a configuration error or misunderstanding
	// of retry behavior. Extended retries should use longer wait times, not more attempts.
	MaxMaxAttempts = 100

	// MinWaitTimeSeconds is the minimum allowed wait time between retries.
	// At least 1 second prevents tight retry loops that could overwhelm the API.
	MinWaitTimeSeconds = 1

	// MaxWaitTimeSeconds is the maximum allowed wait time between retries.
	// Waiting longer than 10 minutes between retries is impractical for most use cases.
	MaxWaitTimeSeconds = 600

	// MinHTTPTimeoutSeconds is the minimum HTTP client timeout.
	// At least 1 second is needed for any network operation.
	MinHTTPTimeoutSeconds = 1

	// MaxHTTPTimeoutSeconds is the maximum HTTP client timeout.
	// Timeouts longer than 5 minutes typically indicate a configuration error.
	// Most Slack API calls should complete within seconds.
	MaxHTTPTimeoutSeconds = 300
)

// SlackClientConfig holds configuration for the Slack API client used by both
// the API server and Manager service.
//
// # Authentication
//
// Two tokens are required for full functionality:
//   - AppToken (xapp-...): Used for Socket Mode connections to receive real-time events
//   - BotToken (xoxb-...): Used for API calls like posting messages and reading channels
//
// Both tokens are obtained from the Slack App configuration at https://api.slack.com/apps.
//
// # Retry Behavior
//
// The client implements automatic retry with exponential backoff for different error types:
//
//   - Rate Limit Errors (429): Retried with Slack's Retry-After header, up to
//     MaxAttemptsForRateLimitError attempts with max wait of MaxRateLimitErrorWaitTimeSeconds.
//
//   - Transient Errors (5xx, timeouts): Retried with exponential backoff, up to
//     MaxAttemptsForTransientError attempts with max wait of MaxTransientErrorWaitTimeSeconds.
//
//   - Fatal Errors (4xx except 429): Retried sparingly (MaxAttemptsForFatalError attempts)
//     since these usually indicate permanent problems like invalid parameters.
//
// # Concurrency
//
// The Concurrency setting controls how many parallel Slack API requests can be made.
// Higher values increase throughput but risk triggering rate limits. The default of 3
// is conservative; increase only if you need higher throughput and are prepared to
// handle more rate limit responses.
//
// # Debug Mode
//
// When DebugLogging is true, all Slack API requests and responses are logged at debug
// level. This is useful for troubleshooting but should be disabled in production as
// it may log sensitive information.
//
// # Dry Run Mode
//
// When DryRun is true, the client logs what it would do without actually calling the
// Slack API. Useful for testing configuration and message formatting without sending
// real messages.
type SlackClientConfig struct {
	// AppToken is the Slack App-Level Token (xapp-...) used for Socket Mode connections.
	// This token enables real-time event handling including reactions, mentions, and
	// interactive components. Obtain from: Slack App Settings > Basic Information >
	// App-Level Tokens.
	AppToken string `json:"appToken" yaml:"appToken"`

	// BotToken is the Slack Bot User OAuth Token (xoxb-...) used for API operations.
	// This token is used for posting messages, reading channels, managing reactions,
	// and other bot actions. Obtain from: Slack App Settings > OAuth & Permissions.
	BotToken string `json:"botToken" yaml:"botToken"`

	// DebugLogging enables verbose logging of all Slack API requests and responses.
	// Useful for debugging but should be disabled in production as it may expose
	// sensitive data in logs.
	DebugLogging bool `json:"debugLogging" yaml:"debugLogging"`

	// DryRun prevents actual Slack API calls when true. The client logs intended
	// actions without executing them. Useful for testing configuration and validating
	// message formatting without affecting real Slack channels.
	DryRun bool `json:"dryRun" yaml:"dryRun"`

	// Concurrency sets the maximum number of concurrent Slack API requests.
	// Higher values increase throughput but may trigger more rate limit responses.
	// Default: 3. Must be between 1 and 50.
	Concurrency int `json:"concurrency" yaml:"concurrency"`

	// MaxAttemptsForRateLimitError sets how many times to retry after receiving
	// a rate limit (HTTP 429) response. Slack rate limits are usually short-lived
	// (seconds to minutes). Default: 10. Must be between 1 and 100.
	MaxAttemptsForRateLimitError int `json:"maxAttemptsForRateLimitError" yaml:"maxAttemptsForRateLimitError"`

	// MaxAttemptsForTransientError sets how many times to retry after transient
	// errors like network timeouts or 5xx server errors. Default: 5. Must be
	// between 1 and 100.
	MaxAttemptsForTransientError int `json:"maxAttemptsForTransientError" yaml:"maxAttemptsForTransientError"`

	// MaxAttemptsForFatalError sets how many times to retry after fatal errors
	// (4xx responses except 429). These rarely succeed on retry but a few attempts
	// help with edge cases. Default: 5. Must be between 1 and 100.
	MaxAttemptsForFatalError int `json:"maxAttemptsForFatalError" yaml:"maxAttemptsForFatalError"`

	// MaxRateLimitErrorWaitTimeSeconds caps the wait time between rate limit retries.
	// Even if Slack's Retry-After header suggests longer, we won't wait more than
	// this many seconds. Default: 120. Must be between 1 and 600.
	MaxRateLimitErrorWaitTimeSeconds int `json:"maxRateLimitErrorWaitTimeSeconds" yaml:"maxRateLimitErrorWaitTimeSeconds"`

	// MaxTransientErrorWaitTimeSeconds caps the wait time between transient error
	// retries using exponential backoff. Default: 30. Must be between 1 and 600.
	MaxTransientErrorWaitTimeSeconds int `json:"maxTransientErrorWaitTimeSeconds" yaml:"maxTransientErrorWaitTimeSeconds"`

	// MaxFatalErrorWaitTimeSeconds caps the wait time between fatal error retries.
	// Kept short since fatal errors rarely resolve on their own. Default: 30.
	// Must be between 1 and 600.
	MaxFatalErrorWaitTimeSeconds int `json:"maxFatalErrorWaitTimeSeconds" yaml:"maxFatalErrorWaitTimeSeconds"`

	// HTTPTimeoutSeconds sets the HTTP client timeout for Slack API requests.
	// This should be long enough to accommodate slow responses but short enough
	// to fail fast on unresponsive endpoints. Default: 30. Must be between 1 and 300.
	HTTPTimeoutSeconds int `json:"httpTimeoutSeconds" yaml:"httpTimeoutSeconds"`
}

// NewDefaultSlackClientConfig returns a SlackClientConfig populated with sensible defaults.
//
// The defaults are configured for typical production use:
//   - Concurrency: 3 (balanced throughput vs rate limits)
//   - Rate limit retries: 10 attempts, max 120s wait
//   - Transient error retries: 5 attempts, max 30s wait
//   - Fatal error retries: 5 attempts, max 30s wait
//   - HTTP timeout: 30 seconds
//
// The AppToken and BotToken are intentionally left empty and must be set before use.
func NewDefaultSlackClientConfig() *SlackClientConfig {
	c := &SlackClientConfig{}
	c.SetDefaults()
	return c
}

// SetDefaults sets default values for any numeric fields that have zero or negative values.
// This is useful when the config is loaded from a file or environment variables where
// some fields may not be specified.
//
// This method does not set defaults for required fields like AppToken and BotToken,
// as those must be explicitly configured.
func (c *SlackClientConfig) SetDefaults() {
	if c.Concurrency <= 0 {
		c.Concurrency = DefaultConcurrency
	}

	if c.MaxAttemptsForRateLimitError <= 0 {
		c.MaxAttemptsForRateLimitError = DefaultMaxAttemptsForRateLimitError
	}

	if c.MaxAttemptsForTransientError <= 0 {
		c.MaxAttemptsForTransientError = DefaultMaxAttemptsForTransientError
	}

	if c.MaxAttemptsForFatalError <= 0 {
		c.MaxAttemptsForFatalError = DefaultMaxAttemptsForFatalError
	}

	if c.MaxRateLimitErrorWaitTimeSeconds <= 0 {
		c.MaxRateLimitErrorWaitTimeSeconds = DefaultMaxRateLimitErrorWaitTimeSeconds
	}

	if c.MaxTransientErrorWaitTimeSeconds <= 0 {
		c.MaxTransientErrorWaitTimeSeconds = DefaultMaxTransientErrorWaitTimeSeconds
	}

	if c.MaxFatalErrorWaitTimeSeconds <= 0 {
		c.MaxFatalErrorWaitTimeSeconds = DefaultMaxFatalErrorWaitTimeSeconds
	}

	if c.HTTPTimeoutSeconds <= 0 {
		c.HTTPTimeoutSeconds = DefaultHTTPTimeoutSeconds
	}
}

// Validate checks that all required fields are present and all values are within
// acceptable ranges. It returns a descriptive error for the first validation failure
// encountered, or nil if the configuration is valid.
//
// Validation includes:
//   - AppToken: must not be empty
//   - BotToken: must not be empty
//   - Concurrency: must be between 1 and 50
//   - All MaxAttempts fields: must be between 1 and 100
//   - All MaxWaitTime fields: must be between 1 and 600 seconds
//   - HTTPTimeoutSeconds: must be between 1 and 300 seconds
func (c *SlackClientConfig) Validate() error {
	if c.AppToken == "" {
		return errors.New("app token is empty")
	}

	if c.BotToken == "" {
		return errors.New("bot token is empty")
	}

	if c.Concurrency < MinConcurrency || c.Concurrency > MaxConcurrency {
		return fmt.Errorf("concurrency must be between %d and %d", MinConcurrency, MaxConcurrency)
	}

	if c.MaxAttemptsForRateLimitError < MinMaxAttempts || c.MaxAttemptsForRateLimitError > MaxMaxAttempts {
		return fmt.Errorf("max attempts for rate limit error must be between %d and %d", MinMaxAttempts, MaxMaxAttempts)
	}

	if c.MaxAttemptsForTransientError < MinMaxAttempts || c.MaxAttemptsForTransientError > MaxMaxAttempts {
		return fmt.Errorf("max attempts for transient error must be between %d and %d", MinMaxAttempts, MaxMaxAttempts)
	}

	if c.MaxAttemptsForFatalError < MinMaxAttempts || c.MaxAttemptsForFatalError > MaxMaxAttempts {
		return fmt.Errorf("max attempts for fatal error must be between %d and %d", MinMaxAttempts, MaxMaxAttempts)
	}

	if c.MaxRateLimitErrorWaitTimeSeconds < MinWaitTimeSeconds || c.MaxRateLimitErrorWaitTimeSeconds > MaxWaitTimeSeconds {
		return fmt.Errorf("max rate limit error wait time must be between %d and %d seconds", MinWaitTimeSeconds, MaxWaitTimeSeconds)
	}

	if c.MaxTransientErrorWaitTimeSeconds < MinWaitTimeSeconds || c.MaxTransientErrorWaitTimeSeconds > MaxWaitTimeSeconds {
		return fmt.Errorf("max transient error wait time must be between %d and %d seconds", MinWaitTimeSeconds, MaxWaitTimeSeconds)
	}

	if c.MaxFatalErrorWaitTimeSeconds < MinWaitTimeSeconds || c.MaxFatalErrorWaitTimeSeconds > MaxWaitTimeSeconds {
		return fmt.Errorf("max fatal error wait time must be between %d and %d seconds", MinWaitTimeSeconds, MaxWaitTimeSeconds)
	}

	if c.HTTPTimeoutSeconds < MinHTTPTimeoutSeconds || c.HTTPTimeoutSeconds > MaxHTTPTimeoutSeconds {
		return fmt.Errorf("http timeout must be between %d and %d seconds", MinHTTPTimeoutSeconds, MaxHTTPTimeoutSeconds)
	}

	return nil
}
