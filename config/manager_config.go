package config

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

var encryptionKeyRegex = regexp.MustCompile(`^[a-zA-Z0-9]{32}$`)

// cacheKeyPrefixRegex allows letters, digits, dots, underscores, colons, and hyphens.
// These are the characters that are safe and conventional in Redis key namespacing,
// while avoiding spaces, control characters, and other characters that cause issues
// with redis-cli, monitoring tooling, and key-slot hashing in Redis Cluster.
var cacheKeyPrefixRegex = regexp.MustCompile(`^[a-zA-Z0-9._:-]+$`)

// metricsPrefixRegex enforces the Prometheus metric name character set for the prefix.
// Prometheus metric names must match [a-zA-Z_:][a-zA-Z0-9_:]*, but colons are reserved
// for recording rules, so client library metrics should use [a-zA-Z_][a-zA-Z0-9_]*.
// An empty prefix is always valid (it disables prefixing entirely).
var metricsPrefixRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// DefaultLocation is the default IANA timezone name used by the Manager for timestamp
// formatting. "UTC" is recommended for distributed systems and log aggregation.
const DefaultLocation = "UTC"

// DefaultKeyPrefix is the default Redis key namespace used by all slackmgr components.
// It is applied to cache keys, queue keys, and distributed gate keys.
// Override it when running multiple independent slackmgr deployments against the same Redis instance.
const DefaultKeyPrefix = "slack-manager:"

// DefaultMetricsPrefix is the default prefix prepended to all metric names registered by
// the Manager. It namespaces slackmgr metrics so they are easy to identify and filter
// in dashboards. Set MetricsPrefix to an empty string to disable prefixing.
const DefaultMetricsPrefix = "slackmgr_"

// Validation constants for prefix fields shared by both APIConfig and ManagerConfig.
const (
	// MaxCacheKeyPrefixLen is the maximum allowed length for CacheKeyPrefix.
	// Long prefixes waste Redis memory and complicate key inspection; 100 characters
	// is generous enough to accommodate any realistic multi-deployment naming scheme.
	MaxCacheKeyPrefixLen = 100

	// MaxMetricsPrefixLen is the maximum allowed length for MetricsPrefix.
	// Prometheus has no hard limit on metric name length, but names beyond this length
	// become impractical in dashboards and alerting rules.
	MaxMetricsPrefixLen = 64
)

// Validation constants for ManagerConfig drain timeout fields.
// Drain timeouts control how long the system waits during graceful shutdown
// to process remaining messages before forcefully terminating.
const (
	// MinDrainTimeoutMs is the minimum allowed value for drain timeout fields, in milliseconds.
	// This must be at least 2 seconds because internal operations (message acknowledgment,
	// negative acknowledgment, and distributed lock release) each have 2-second timeouts.
	// Setting a drain timeout shorter than this could cause message loss during shutdown.
	MinDrainTimeoutMs = 2_000

	// MaxDrainTimeoutMs is the maximum allowed value for drain timeout fields, in milliseconds.
	// A 5-minute maximum prevents excessively long shutdown times that could delay
	// deployments or cause orchestration systems (like Kubernetes) to forcefully
	// terminate the process. In practice, if draining takes longer than a few minutes,
	// there is likely a deeper issue that won't be resolved by waiting longer.
	MaxDrainTimeoutMs = 300_000

	// DefaultCoordinatorDrainTimeoutMs is the default drain timeout for the coordinator,
	// in milliseconds. 5 seconds provides enough time to process in-flight messages while
	// keeping shutdown times reasonable for typical deployments.
	DefaultCoordinatorDrainTimeoutMs = 5_000

	// DefaultChannelManagerDrainTimeoutMs is the default drain timeout for channel managers,
	// in milliseconds. 3 seconds is typically sufficient for channel managers, which have
	// smaller internal buffers than the coordinator. This is intentionally shorter than the
	// coordinator timeout to allow for sequential draining if needed.
	DefaultChannelManagerDrainTimeoutMs = 3_000

	// MinSocketModeMaxWorkers is the minimum allowed value for concurrent socket mode handlers.
	// At least 10 workers ensures the system can handle basic event processing even under
	// constrained resource environments.
	MinSocketModeMaxWorkers = int64(10)

	// MaxSocketModeMaxWorkers is the maximum allowed value for concurrent socket mode handlers.
	// 1000 workers is a reasonable upper bound that prevents excessive goroutine spawning
	// while still allowing high-throughput event processing.
	MaxSocketModeMaxWorkers = int64(1000)

	// DefaultSocketModeMaxWorkers is the default number of concurrent socket mode handlers.
	// 100 workers provides a good balance between throughput and resource usage for
	// typical Slack workspaces.
	DefaultSocketModeMaxWorkers = int64(100)

	// DefaultSocketModeDrainTimeoutMs is the default drain timeout for socket mode handlers,
	// in milliseconds. 5 seconds provides enough time for most handlers to complete their
	// work (Slack API calls, queue writes) during graceful shutdown.
	DefaultSocketModeDrainTimeoutMs = 5_000
)

// ManagerConfig holds configuration for the Slack Manager service.
//
// The Manager service is responsible for processing alerts from the queue, managing
// issue lifecycle, and handling Slack events via Socket Mode. This configuration
// controls startup-time settings that cannot be changed without restarting the service.
//
// # Relationship with APIConfig
//
// The Manager and API services share some configuration values that must match:
//   - EncryptionKey: Must be identical for encrypted payloads to be decrypted correctly
//   - CacheKeyPrefix: Should be identical to share cached Slack channel data and reduce API calls
//
// # Graceful Shutdown
//
// The drain timeout settings bound the nacking phase that runs after the context is
// cancelled. During shutdown:
//  1. The coordinator nacks any messages still buffered in its internal channels,
//     returning them to the external queue immediately without further processing.
//  2. Each channel manager does the same for its own internal buffers.
//  3. Because each drain loop exits as soon as its channels are empty (via a default
//     select case), the timeout is only reached if the internal channels are heavily
//     backlogged at shutdown time. In normal operation both drains complete instantly.
type ManagerConfig struct {
	// EncryptionKey is a 32-character alphanumeric key used for AES-256 encryption of
	// sensitive data in alert payloads. This key must be identical to the key configured
	// in APIConfig to ensure the Manager can decrypt payloads encrypted by the API.
	//
	// Security considerations:
	//   - Generate a cryptographically secure random key for production
	//   - Never use predictable values like "test" repeated or sequential characters
	//   - Rotate keys by deploying new API and Manager instances simultaneously
	//   - Store the key securely (e.g., Kubernetes secrets, HashiCorp Vault)
	EncryptionKey string `json:"encryptionKey" mapstructure:"encryptionKey" yaml:"encryptionKey"`

	// CacheKeyPrefix is prepended to all Redis cache keys to namespace them. Using the
	// same prefix in both the API and Manager allows them to share cached data (such as
	// Slack channel information and user lookups), significantly reducing Slack API calls.
	//
	// Use different prefixes only if running multiple independent Slack Manager deployments
	// against the same Redis cluster that should not share cache data.
	CacheKeyPrefix string `json:"cacheKeyPrefix" mapstructure:"cacheKeyPrefix" yaml:"cacheKeyPrefix"`

	// MetricsPrefix is prepended to all metric names registered by the Manager, including
	// Slack API metrics, queue metrics, and channel manager metrics. This namespaces
	// slackmgr metrics so they are easy to identify and filter in dashboards.
	//
	// Defaults to "slackmgr_" (e.g. "slackmgr_slack_api_call_total").
	// Set to an empty string to disable prefixing and keep bare metric names.
	MetricsPrefix string `json:"metricsPrefix" mapstructure:"metricsPrefix" yaml:"metricsPrefix"`

	// IsSingleInstanceDeployment disables certain safeguards that exist to protect
	// correctness in multi-instance deployments. Setting this to true is ONLY safe
	// when exactly one manager instance will be running at any given time.
	//
	// Specifically, setting this to true:
	//   - Allows a nil ChannelLocker (a no-op in-process locker will be used automatically).
	//   - Allows an in-memory cache store without requiring SkipDatabaseCache=true.
	//
	// WARNING: Do NOT set this to true in production environments that run more than
	// one manager instance. Doing so will cause race conditions and data inconsistency.
	IsSingleInstanceDeployment bool `json:"isSingleInstanceDeployment" mapstructure:"isSingleInstanceDeployment" yaml:"isSingleInstanceDeployment"`

	// SkipDatabaseCache disables the in-memory database query cache when set to true.
	// The database cache reduces load on the database by caching frequently accessed
	// data like issue states and channel configurations.
	//
	// Set to true only for:
	//   - Debugging cache-related issues
	//   - Development environments where you need immediate database consistency
	//   - Testing scenarios that require predictable database behavior
	//
	// In production, keep this false (the default) for optimal performance.
	SkipDatabaseCache bool `json:"skipDatabaseCache" mapstructure:"skipDatabaseCache" yaml:"skipDatabaseCache"`

	// Location specifies the IANA timezone name used for timestamp formatting in
	// Slack messages, logs, and issue metadata. All time-related operations use this
	// location for consistency.
	//
	// Must be a valid IANA timezone name (validated by time.LoadLocation on startup).
	// Common values: "UTC" (default), "America/New_York", "Europe/London", "Asia/Tokyo".
	// Use "Local" to inherit the server's local timezone (not recommended for production).
	Location string `json:"location" mapstructure:"location" yaml:"location"`

	// SlackClient contains configuration for connecting to the Slack API, including
	// authentication tokens, retry behavior, and timeout settings. See SlackClientConfig
	// for detailed documentation of each field.
	SlackClient *SlackClientConfig `json:"slackClient" mapstructure:"slackClient" yaml:"slackClient"`

	// CoordinatorDrainTimeoutMs is the maximum time the coordinator spends nacking
	// buffered messages during shutdown, in milliseconds.
	//
	// When the context is cancelled, the coordinator exits its main processing loop and
	// immediately nacks any messages still sitting in its internal alert and command
	// channels, returning them to the external queue for reprocessing. No message
	// delivery to channel managers occurs during this phase.
	//
	// Because the drain loop exits as soon as both channels are empty (via a default
	// select case), this timeout is only reached if the internal channels are heavily
	// backlogged at shutdown time. In normal operation the drain completes instantly.
	//
	// Default: 5000 (5s). Must be between 2000 (2s) and 300000 (5m).
	CoordinatorDrainTimeoutMs int `json:"coordinatorDrainTimeoutMs" mapstructure:"coordinatorDrainTimeoutMs" yaml:"coordinatorDrainTimeoutMs"`

	// ChannelManagerDrainTimeoutMs is the maximum time each channel manager spends
	// nacking buffered messages during shutdown, in milliseconds.
	//
	// When the context is cancelled, each channel manager exits its run loop and
	// immediately nacks any messages still sitting in its internal alert and command
	// channels, returning them to the external queue for reprocessing. No processing
	// occurs during this phase — messages are not passed to issue creation or Slack API
	// logic; they are simply returned to the queue.
	//
	// Because the drain loop exits as soon as both channels are empty (via a default
	// select case), this timeout is only reached if the internal channels are heavily
	// backlogged at shutdown time. In normal operation the drain completes instantly.
	//
	// Default: 3000 (3s). Must be between 2000 (2s) and 300000 (5m).
	ChannelManagerDrainTimeoutMs int `json:"channelManagerDrainTimeoutMs" mapstructure:"channelManagerDrainTimeoutMs" yaml:"channelManagerDrainTimeoutMs"`

	// SocketModeMaxWorkers limits the number of concurrent socket mode event handlers.
	// This prevents goroutine explosion under high load by using a semaphore to limit
	// the number of handlers that can run simultaneously.
	//
	// Each Slack event (reactions, interactions, slash commands, etc.) is processed
	// by a separate goroutine. Without this limit, a burst of events could spawn
	// thousands of goroutines, exhausting system resources.
	//
	// Default: 100. Must be between 10 and 1000.
	SocketModeMaxWorkers int64 `json:"socketModeMaxWorkers" mapstructure:"socketModeMaxWorkers" yaml:"socketModeMaxWorkers"`

	// SocketModeDrainTimeoutMs is the maximum time to wait for in-flight socket mode
	// handlers to complete during graceful shutdown, in milliseconds.
	//
	// During graceful shutdown, the socket mode handler:
	//  1. Stops accepting new events from the Slack socket
	//  2. Waits for all in-flight handlers to complete their work
	//  3. If timeout is exceeded, logs a warning and proceeds with shutdown
	//
	// Handlers that don't complete within this timeout may have their work interrupted.
	// For critical operations (like queue writes), handlers should complete quickly.
	//
	// Default: 5000 (5s). Must be between 2000 (2s) and 300000 (5m).
	SocketModeDrainTimeoutMs int `json:"socketModeDrainTimeoutMs" mapstructure:"socketModeDrainTimeoutMs" yaml:"socketModeDrainTimeoutMs"`

	// location is the parsed *time.Location for the Location field, cached by Validate().
	location *time.Location
}

// NewDefaultManagerConfig returns a ManagerConfig populated with sensible default values.
//
// The defaults are configured for a typical production deployment:
//   - Cache key prefix "slack-manager:" for Redis key namespacing
//   - UTC timezone for consistent timestamp handling
//   - Coordinator drain timeout of 5 seconds
//   - Channel manager drain timeout of 3 seconds
//   - Socket mode max workers of 100
//   - Socket mode drain timeout of 5 seconds
//   - Database cache enabled (SkipDatabaseCache = false)
//
// The EncryptionKey is intentionally left empty and must be set before use.
// The SlackClient tokens are also empty and must be configured.
func NewDefaultManagerConfig() *ManagerConfig {
	return &ManagerConfig{
		CacheKeyPrefix:               DefaultKeyPrefix,
		MetricsPrefix:                DefaultMetricsPrefix,
		Location:                     DefaultLocation,
		SlackClient:                  NewDefaultSlackClientConfig(),
		CoordinatorDrainTimeoutMs:    DefaultCoordinatorDrainTimeoutMs,
		ChannelManagerDrainTimeoutMs: DefaultChannelManagerDrainTimeoutMs,
		SocketModeMaxWorkers:         DefaultSocketModeMaxWorkers,
		SocketModeDrainTimeoutMs:     DefaultSocketModeDrainTimeoutMs,
	}
}

// Validate checks that all required fields are present and all values are within
// acceptable ranges. It returns a descriptive error for the first validation failure
// encountered, or nil if the configuration is valid.
//
// Validation includes:
//   - EncryptionKey: if non-empty, must be exactly 32 alphanumeric characters
//   - CacheKeyPrefix: required; max 100 chars; only letters, digits, '.', '_', ':', '-'
//   - MetricsPrefix: if non-empty, must be a valid Prometheus name prefix (letters, digits, '_'; max 64 chars)
//   - Location: must not be empty and must be a valid IANA timezone name
//   - SlackClient: must not be nil, and must pass its own validation
//   - CoordinatorDrainTimeoutMs: must be between 2000 ms and 300000 ms
//   - ChannelManagerDrainTimeoutMs: must be between 2000 ms and 300000 ms
//   - SocketModeMaxWorkers: must be between 10 and 1000
//   - SocketModeDrainTimeoutMs: must be between 2000 ms and 300000 ms
func (c *ManagerConfig) Validate() error {
	if c.EncryptionKey != "" && !encryptionKeyRegex.MatchString(c.EncryptionKey) {
		return fmt.Errorf("encryption key must be a %d character alphanumeric string", EncryptionKeyLength)
	}

	if err := validateCacheKeyPrefix(c.CacheKeyPrefix); err != nil {
		return err
	}

	if err := validateMetricsPrefix(c.MetricsPrefix); err != nil {
		return err
	}

	if c.Location == "" {
		return errors.New("location is required")
	}

	loc, err := time.LoadLocation(c.Location)
	if err != nil {
		return fmt.Errorf("location %q is not a valid IANA timezone: %w", c.Location, err)
	}

	c.location = loc

	if c.SlackClient == nil {
		return errors.New("slack client config is required")
	}

	if err := c.SlackClient.Validate(); err != nil {
		return fmt.Errorf("slack client config is invalid: %w", err)
	}

	if c.CoordinatorDrainTimeoutMs < MinDrainTimeoutMs || c.CoordinatorDrainTimeoutMs > MaxDrainTimeoutMs {
		return fmt.Errorf("coordinator drain timeout must be between %d ms and %d ms", MinDrainTimeoutMs, MaxDrainTimeoutMs)
	}

	if c.ChannelManagerDrainTimeoutMs < MinDrainTimeoutMs || c.ChannelManagerDrainTimeoutMs > MaxDrainTimeoutMs {
		return fmt.Errorf("channel manager drain timeout must be between %d ms and %d ms", MinDrainTimeoutMs, MaxDrainTimeoutMs)
	}

	if c.SocketModeMaxWorkers < MinSocketModeMaxWorkers || c.SocketModeMaxWorkers > MaxSocketModeMaxWorkers {
		return fmt.Errorf("socket mode max workers must be between %d and %d", MinSocketModeMaxWorkers, MaxSocketModeMaxWorkers)
	}

	if c.SocketModeDrainTimeoutMs < MinDrainTimeoutMs || c.SocketModeDrainTimeoutMs > MaxDrainTimeoutMs {
		return fmt.Errorf("socket mode drain timeout must be between %d ms and %d ms", MinDrainTimeoutMs, MaxDrainTimeoutMs)
	}

	return nil
}

// GetLocation returns the parsed *time.Location for the Location field. The result is
// cached by Validate(), so after a successful Validate() call this is a simple pointer
// return with no I/O. If called before Validate(), the location is parsed on demand
// and time.UTC is returned as a fallback on error.
func (c *ManagerConfig) GetLocation() *time.Location {
	if c.location != nil {
		return c.location
	}

	loc, err := time.LoadLocation(c.Location)
	if err != nil {
		return time.UTC
	}

	return loc
}

// validateCacheKeyPrefix checks that a Redis cache key prefix is non-empty, within
// [MaxCacheKeyPrefixLen] characters, and contains only characters that are safe and
// conventional in Redis key naming: letters, digits, '.', '_', ':', and '-'.
func validateCacheKeyPrefix(prefix string) error {
	if prefix == "" {
		return errors.New("cache key prefix is required")
	}

	if len(prefix) > MaxCacheKeyPrefixLen {
		return fmt.Errorf("cache key prefix must not exceed %d characters", MaxCacheKeyPrefixLen)
	}

	if !cacheKeyPrefixRegex.MatchString(prefix) {
		return errors.New("cache key prefix may only contain letters, digits, '.', '_', ':', or '-'")
	}

	return nil
}

// validateMetricsPrefix checks that a Prometheus metrics prefix is either empty
// (disabling prefixing) or a valid Prometheus metric name component: must start with
// a letter or underscore, contain only letters, digits, or underscores, and not exceed
// [MaxMetricsPrefixLen] characters. Colons are intentionally excluded because the
// Prometheus data model reserves them for recording rules.
func validateMetricsPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}

	if len(prefix) > MaxMetricsPrefixLen {
		return fmt.Errorf("metrics prefix must not exceed %d characters", MaxMetricsPrefixLen)
	}

	if !metricsPrefixRegex.MatchString(prefix) {
		return errors.New("metrics prefix must start with a letter or underscore and contain only letters, digits, or underscores")
	}

	return nil
}
