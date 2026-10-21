package config

import "fmt"

// Default and limit values for InsightsConfig.
const (
	// DefaultInsightsBufferSize is the default capacity of the in-memory insights event buffer.
	DefaultInsightsBufferSize = 10000

	// MinInsightsBufferSize is the minimum allowed insights buffer size.
	MinInsightsBufferSize = 100

	// MaxInsightsBufferSize is the maximum allowed insights buffer size.
	MaxInsightsBufferSize = 1000000

	// DefaultInsightsBatchSize is the default maximum number of insights events per database write.
	DefaultInsightsBatchSize = 500

	// MaxInsightsBatchSize is the maximum allowed insights batch size.
	MaxInsightsBatchSize = 10000

	// DefaultInsightsFlushIntervalMs is the default maximum time an insights event waits before it is written.
	DefaultInsightsFlushIntervalMs = 1000

	// MinInsightsFlushIntervalMs is the minimum allowed insights flush interval.
	MinInsightsFlushIntervalMs = 100

	// MaxInsightsFlushIntervalMs is the maximum allowed insights flush interval.
	MaxInsightsFlushIntervalMs = 60000

	// DefaultInsightsWriteTimeoutMs is the default deadline of a single insights database write.
	DefaultInsightsWriteTimeoutMs = 5000

	// MinInsightsWriteTimeoutMs is the minimum allowed insights write timeout.
	MinInsightsWriteTimeoutMs = 100

	// MaxInsightsWriteTimeoutMs is the maximum allowed insights write timeout.
	MaxInsightsWriteTimeoutMs = 60000

	// DefaultInsightsShutdownFlushTimeoutMs is the default maximum time spent flushing insights events at shutdown.
	DefaultInsightsShutdownFlushTimeoutMs = 2000

	// MaxInsightsShutdownFlushTimeoutMs is the maximum allowed insights shutdown flush timeout.
	MaxInsightsShutdownFlushTimeoutMs = 30000

	// DefaultInsightsChannelStatsIntervalSeconds is the default minimum interval between channel snapshots.
	DefaultInsightsChannelStatsIntervalSeconds = 300

	// MinInsightsChannelStatsIntervalSeconds is the minimum allowed channel snapshot interval.
	MinInsightsChannelStatsIntervalSeconds = 60

	// MaxInsightsChannelStatsIntervalSeconds is the maximum allowed channel snapshot interval.
	MaxInsightsChannelStatsIntervalSeconds = 86400
)

// InsightsConfig controls recording of insights events (see the insights package).
//
// Insights are recorded only when an insights sink is available: either discovered from the database
// (a DB implementing types.InsightsSinkProvider that returns a non-nil sink) or set explicitly by the host.
// Recording never blocks or slows down alert processing: events are buffered in memory and written in
// batches by a background goroutine, and are dropped (and counted in metrics) if the buffer is full.
//
// A nil *InsightsConfig is valid and means default values.
type InsightsConfig struct {
	// Disabled turns insights recording off, even if an insights sink is available.
	//
	// Default: false.
	Disabled bool `json:"disabled" mapstructure:"disabled" yaml:"disabled"`

	// BufferSize is the capacity of the in-memory event buffer. When the buffer is full, new events are dropped.
	//
	// Default: 10000. Must be between 100 and 1000000.
	BufferSize int `json:"bufferSize" mapstructure:"bufferSize" yaml:"bufferSize"`

	// BatchSize is the maximum number of events per database write.
	//
	// Default: 500. Must be between 1 and 10000.
	BatchSize int `json:"batchSize" mapstructure:"batchSize" yaml:"batchSize"`

	// FlushIntervalMs is the maximum time an event waits in a partial batch before it is written, in milliseconds.
	//
	// Default: 1000. Must be between 100 and 60000.
	FlushIntervalMs int `json:"flushIntervalMs" mapstructure:"flushIntervalMs" yaml:"flushIntervalMs"`

	// WriteTimeoutMs is the deadline of a single database write, in milliseconds.
	//
	// Default: 5000. Must be between 100 and 60000.
	WriteTimeoutMs int `json:"writeTimeoutMs" mapstructure:"writeTimeoutMs" yaml:"writeTimeoutMs"`

	// ShutdownFlushTimeoutMs is the maximum time spent writing buffered events during shutdown, in milliseconds.
	// Shutdown is never delayed by more than this. Events not written in time are dropped.
	//
	// Default: 2000. Must be between 1 and 30000.
	ShutdownFlushTimeoutMs int `json:"shutdownFlushTimeoutMs" mapstructure:"shutdownFlushTimeoutMs" yaml:"shutdownFlushTimeoutMs"`

	// ChannelStatsIntervalSeconds is the minimum interval between channel snapshot events (open issue counts)
	// per channel and manager instance. Only used by the manager.
	//
	// Default: 300 (5m). Must be between 60 and 86400.
	ChannelStatsIntervalSeconds int `json:"channelStatsIntervalSeconds" mapstructure:"channelStatsIntervalSeconds" yaml:"channelStatsIntervalSeconds"`
}

// NewDefaultInsightsConfig returns an InsightsConfig populated with default values.
func NewDefaultInsightsConfig() *InsightsConfig {
	c := &InsightsConfig{}
	c.SetDefaults()

	return c
}

// SetDefaults sets default values for any numeric fields that have zero or negative values.
func (c *InsightsConfig) SetDefaults() {
	if c.BufferSize <= 0 {
		c.BufferSize = DefaultInsightsBufferSize
	}

	if c.BatchSize <= 0 {
		c.BatchSize = DefaultInsightsBatchSize
	}

	if c.FlushIntervalMs <= 0 {
		c.FlushIntervalMs = DefaultInsightsFlushIntervalMs
	}

	if c.WriteTimeoutMs <= 0 {
		c.WriteTimeoutMs = DefaultInsightsWriteTimeoutMs
	}

	if c.ShutdownFlushTimeoutMs <= 0 {
		c.ShutdownFlushTimeoutMs = DefaultInsightsShutdownFlushTimeoutMs
	}

	if c.ChannelStatsIntervalSeconds <= 0 {
		c.ChannelStatsIntervalSeconds = DefaultInsightsChannelStatsIntervalSeconds
	}
}

// Validate checks that all values are within acceptable ranges. Call SetDefaults first to fill unset fields.
func (c *InsightsConfig) Validate() error {
	checks := []struct {
		name     string
		value    int
		min, max int
	}{
		{"buffer size", c.BufferSize, MinInsightsBufferSize, MaxInsightsBufferSize},
		{"batch size", c.BatchSize, 1, MaxInsightsBatchSize},
		{"flush interval (ms)", c.FlushIntervalMs, MinInsightsFlushIntervalMs, MaxInsightsFlushIntervalMs},
		{"write timeout (ms)", c.WriteTimeoutMs, MinInsightsWriteTimeoutMs, MaxInsightsWriteTimeoutMs},
		{"shutdown flush timeout (ms)", c.ShutdownFlushTimeoutMs, 1, MaxInsightsShutdownFlushTimeoutMs},
		{"channel stats interval (s)", c.ChannelStatsIntervalSeconds, MinInsightsChannelStatsIntervalSeconds, MaxInsightsChannelStatsIntervalSeconds},
	}

	for _, check := range checks {
		if check.value < check.min || check.value > check.max {
			return fmt.Errorf("insights %s must be between %d and %d", check.name, check.min, check.max)
		}
	}

	return nil
}
