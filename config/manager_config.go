package config

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

var encryptionKeyRegex = regexp.MustCompile(`^[a-zA-Z0-9]{32}$`)

// Validation constants for ManagerConfig drain timeout fields.
// Drain timeouts control how long the system waits during graceful shutdown
// to process remaining messages before forcefully terminating.
const (
	// MinDrainTimeout is the minimum allowed value for drain timeout fields.
	// This must be at least 2 seconds because internal operations (message acknowledgment,
	// negative acknowledgment, and distributed lock release) each have 2-second timeouts.
	// Setting a drain timeout shorter than this could cause message loss during shutdown.
	MinDrainTimeout = 2 * time.Second

	// MaxDrainTimeout is the maximum allowed value for drain timeout fields.
	// A 5-minute maximum prevents excessively long shutdown times that could delay
	// deployments or cause orchestration systems (like Kubernetes) to forcefully
	// terminate the process. In practice, if draining takes longer than a few minutes,
	// there is likely a deeper issue that won't be resolved by waiting longer.
	MaxDrainTimeout = 5 * time.Minute

	// DefaultCoordinatorDrainTimeout is the default drain timeout for the coordinator.
	// 5 seconds provides enough time to process in-flight messages while keeping
	// shutdown times reasonable for typical deployments.
	DefaultCoordinatorDrainTimeout = 5 * time.Second

	// DefaultChannelManagerDrainTimeout is the default drain timeout for channel managers.
	// 3 seconds is typically sufficient for channel managers, which have smaller internal
	// buffers than the coordinator. This is intentionally shorter than the coordinator
	// timeout to allow for sequential draining if needed.
	DefaultChannelManagerDrainTimeout = 3 * time.Second

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

	// DefaultSocketModeDrainTimeout is the default drain timeout for socket mode handlers.
	// 5 seconds provides enough time for most handlers to complete their work (Slack API
	// calls, queue writes) during graceful shutdown.
	DefaultSocketModeDrainTimeout = 5 * time.Second
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
// The drain timeout settings control graceful shutdown behavior. During shutdown:
//  1. The coordinator stops accepting new messages and drains its internal channels
//  2. Each channel manager drains its internal buffers
//  3. Messages that cannot be processed within the timeout are left in the queue
//     for reprocessing by another instance (in a multi-instance deployment)
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
	EncryptionKey string `json:"encryptionKey" yaml:"encryptionKey"`

	// CacheKeyPrefix is prepended to all Redis cache keys to namespace them. Using the
	// same prefix in both the API and Manager allows them to share cached data (such as
	// Slack channel information and user lookups), significantly reducing Slack API calls.
	//
	// Use different prefixes only if running multiple independent Slack Manager deployments
	// against the same Redis cluster that should not share cache data.
	CacheKeyPrefix string `json:"cacheKeyPrefix" yaml:"cacheKeyPrefix"`

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
	SkipDatabaseCache bool `json:"skipDatabaseCache" yaml:"skipDatabaseCache"`

	// Location specifies the timezone used for timestamp parsing and formatting in
	// Slack messages, logs, and issue metadata. All time-related operations use this
	// location for consistency.
	//
	// Common values:
	//   - time.UTC (default): Recommended for distributed systems and log aggregation
	//   - time.LoadLocation("America/New_York"): For teams in a specific timezone
	//   - time.Local: Uses the server's local timezone (not recommended for production)
	//
	// Must not be nil. The default is time.UTC.
	Location *time.Location `json:"location" yaml:"location"`

	// SlackClient contains configuration for connecting to the Slack API, including
	// authentication tokens, retry behavior, and timeout settings. See SlackClientConfig
	// for detailed documentation of each field.
	SlackClient *SlackClientConfig `json:"slackClient" yaml:"slackClient"`

	// CoordinatorDrainTimeout is the maximum time the coordinator waits during shutdown
	// to drain messages from its internal channels before terminating.
	//
	// During graceful shutdown, the coordinator:
	//  1. Stops accepting new messages from the queue
	//  2. Attempts to deliver all buffered messages to channel managers
	//  3. Waits up to this timeout for channel managers to acknowledge receipt
	//
	// Messages not delivered within this timeout remain in the external queue and will
	// be reprocessed by another Manager instance (or the same instance after restart).
	//
	// Default: 5 seconds. Must be between 2 seconds and 5 minutes.
	CoordinatorDrainTimeout time.Duration `json:"coordinatorDrainTimeout" yaml:"coordinatorDrainTimeout"`

	// ChannelManagerDrainTimeout is the maximum time each channel manager waits during
	// shutdown to process messages from its internal buffer before terminating.
	//
	// During graceful shutdown, each channel manager:
	//  1. Stops accepting new messages from the coordinator
	//  2. Processes all buffered messages (creating issues, updating Slack, etc.)
	//  3. Acknowledges processed messages to remove them from the queue
	//
	// Messages not processed within this timeout are negatively acknowledged (nack'd)
	// and will be redelivered to the queue for reprocessing.
	//
	// Default: 3 seconds. Must be between 2 seconds and 5 minutes.
	ChannelManagerDrainTimeout time.Duration `json:"channelManagerDrainTimeout" yaml:"channelManagerDrainTimeout"`

	// SocketModeMaxWorkers limits the number of concurrent socket mode event handlers.
	// This prevents goroutine explosion under high load by using a semaphore to limit
	// the number of handlers that can run simultaneously.
	//
	// Each Slack event (reactions, interactions, slash commands, etc.) is processed
	// by a separate goroutine. Without this limit, a burst of events could spawn
	// thousands of goroutines, exhausting system resources.
	//
	// Default: 100. Must be between 10 and 1000.
	SocketModeMaxWorkers int64 `json:"socketModeMaxWorkers" yaml:"socketModeMaxWorkers"`

	// SocketModeDrainTimeout is the maximum time to wait for in-flight socket mode
	// handlers to complete during graceful shutdown.
	//
	// During graceful shutdown, the socket mode handler:
	//  1. Stops accepting new events from the Slack socket
	//  2. Waits for all in-flight handlers to complete their work
	//  3. If timeout is exceeded, logs a warning and proceeds with shutdown
	//
	// Handlers that don't complete within this timeout may have their work interrupted.
	// For critical operations (like queue writes), handlers should complete quickly.
	//
	// Default: 5 seconds. Must be between 2 seconds and 5 minutes.
	SocketModeDrainTimeout time.Duration `json:"socketModeDrainTimeout" yaml:"socketModeDrainTimeout"`
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
		CacheKeyPrefix:             "slack-manager:",
		Location:                   time.UTC,
		SlackClient:                NewDefaultSlackClientConfig(),
		CoordinatorDrainTimeout:    DefaultCoordinatorDrainTimeout,
		ChannelManagerDrainTimeout: DefaultChannelManagerDrainTimeout,
		SocketModeMaxWorkers:       DefaultSocketModeMaxWorkers,
		SocketModeDrainTimeout:     DefaultSocketModeDrainTimeout,
	}
}

// SetDefaults sets default values for any fields that have zero values.
// This is useful when the config is loaded from a file or environment variables
// where some fields may not be specified.
//
// Fields that receive defaults:
//   - CoordinatorDrainTimeout: 5 seconds
//   - ChannelManagerDrainTimeout: 3 seconds
//   - SocketModeMaxWorkers: 100
//   - SocketModeDrainTimeout: 5 seconds
//
// This method does not set defaults for required fields like EncryptionKey
// or SlackClient tokens, as those must be explicitly configured.
func (c *ManagerConfig) SetDefaults() {
	if c.CoordinatorDrainTimeout == 0 {
		c.CoordinatorDrainTimeout = DefaultCoordinatorDrainTimeout
	}

	if c.ChannelManagerDrainTimeout == 0 {
		c.ChannelManagerDrainTimeout = DefaultChannelManagerDrainTimeout
	}

	if c.SocketModeMaxWorkers == 0 {
		c.SocketModeMaxWorkers = DefaultSocketModeMaxWorkers
	}

	if c.SocketModeDrainTimeout == 0 {
		c.SocketModeDrainTimeout = DefaultSocketModeDrainTimeout
	}
}

// Validate checks that all required fields are present and all values are within
// acceptable ranges. It returns a descriptive error for the first validation failure
// encountered, or nil if the configuration is valid.
//
// Validation includes:
//   - EncryptionKey: must be exactly 32 alphanumeric characters
//   - CacheKeyPrefix: must not be empty (required for cache namespacing)
//   - Location: must not be nil
//   - SlackClient: must not be nil, and must pass its own validation
//   - CoordinatorDrainTimeout: must be between 2 seconds and 5 minutes
//   - ChannelManagerDrainTimeout: must be between 2 seconds and 5 minutes
//   - SocketModeMaxWorkers: must be between 10 and 1000
//   - SocketModeDrainTimeout: must be between 2 seconds and 5 minutes
func (c *ManagerConfig) Validate() error {
	if !encryptionKeyRegex.MatchString(c.EncryptionKey) {
		return fmt.Errorf("encryption key must be a %d character alphanumeric string", EncryptionKeyLength)
	}

	if c.CacheKeyPrefix == "" {
		return errors.New("cache key prefix is required")
	}

	if c.Location == nil {
		return errors.New("location is required")
	}

	if c.SlackClient == nil {
		return errors.New("slack client config is required")
	}

	if err := c.SlackClient.Validate(); err != nil {
		return fmt.Errorf("slack client config is invalid: %w", err)
	}

	if c.CoordinatorDrainTimeout < MinDrainTimeout || c.CoordinatorDrainTimeout > MaxDrainTimeout {
		return fmt.Errorf("coordinator drain timeout must be between %v and %v", MinDrainTimeout, MaxDrainTimeout)
	}

	if c.ChannelManagerDrainTimeout < MinDrainTimeout || c.ChannelManagerDrainTimeout > MaxDrainTimeout {
		return fmt.Errorf("channel manager drain timeout must be between %v and %v", MinDrainTimeout, MaxDrainTimeout)
	}

	if c.SocketModeMaxWorkers < MinSocketModeMaxWorkers || c.SocketModeMaxWorkers > MaxSocketModeMaxWorkers {
		return fmt.Errorf("socket mode max workers must be between %d and %d", MinSocketModeMaxWorkers, MaxSocketModeMaxWorkers)
	}

	if c.SocketModeDrainTimeout < MinDrainTimeout || c.SocketModeDrainTimeout > MaxDrainTimeout {
		return fmt.Errorf("socket mode drain timeout must be between %v and %v", MinDrainTimeout, MaxDrainTimeout)
	}

	return nil
}
