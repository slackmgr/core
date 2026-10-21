package config_test

import (
	"testing"

	"github.com/slackmgr/core/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDefaultInsightsConfig(t *testing.T) {
	t.Parallel()

	c := config.NewDefaultInsightsConfig()

	assert.False(t, c.Disabled)
	assert.Equal(t, config.DefaultInsightsBufferSize, c.BufferSize)
	assert.Equal(t, config.DefaultInsightsBatchSize, c.BatchSize)
	assert.Equal(t, config.DefaultInsightsFlushIntervalMs, c.FlushIntervalMs)
	assert.Equal(t, config.DefaultInsightsWriteTimeoutMs, c.WriteTimeoutMs)
	assert.Equal(t, config.DefaultInsightsShutdownFlushTimeoutMs, c.ShutdownFlushTimeoutMs)
	assert.Equal(t, config.DefaultInsightsChannelStatsIntervalSeconds, c.ChannelStatsIntervalSeconds)
	require.NoError(t, c.Validate())
}

func TestInsightsConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		modify  func(c *config.InsightsConfig)
		wantErr string
	}{
		{"buffer too small", func(c *config.InsightsConfig) { c.BufferSize = config.MinInsightsBufferSize - 1 }, "insights buffer size must be between"},
		{"buffer too large", func(c *config.InsightsConfig) { c.BufferSize = config.MaxInsightsBufferSize + 1 }, "insights buffer size must be between"},
		{"batch too large", func(c *config.InsightsConfig) { c.BatchSize = config.MaxInsightsBatchSize + 1 }, "insights batch size must be between"},
		{"flush interval too small", func(c *config.InsightsConfig) { c.FlushIntervalMs = 1 }, "insights flush interval (ms) must be between"},
		{"write timeout too large", func(c *config.InsightsConfig) { c.WriteTimeoutMs = config.MaxInsightsWriteTimeoutMs + 1 }, "insights write timeout (ms) must be between"},
		{"shutdown timeout too large", func(c *config.InsightsConfig) {
			c.ShutdownFlushTimeoutMs = config.MaxInsightsShutdownFlushTimeoutMs + 1
		}, "insights shutdown flush timeout (ms) must be between"},
		{"stats interval too small", func(c *config.InsightsConfig) { c.ChannelStatsIntervalSeconds = 1 }, "insights channel stats interval (s) must be between"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := config.NewDefaultInsightsConfig()
			tt.modify(c)
			require.ErrorContains(t, c.Validate(), tt.wantErr)
		})
	}
}

func TestInsightsConfig_SetDefaultsKeepsExplicitValues(t *testing.T) {
	t.Parallel()

	c := &config.InsightsConfig{BufferSize: 123, BatchSize: -5}
	c.SetDefaults()

	assert.Equal(t, 123, c.BufferSize)
	assert.Equal(t, config.DefaultInsightsBatchSize, c.BatchSize)
}

func TestManagerAndAPIConfig_ValidateInsights(t *testing.T) {
	t.Parallel()

	mc := validManagerConfig()
	require.NoError(t, mc.Validate(), "nil insights config is valid")

	mc.Insights = &config.InsightsConfig{}
	require.NoError(t, mc.Validate())
	assert.Equal(t, config.DefaultInsightsBufferSize, mc.Insights.BufferSize, "defaults are applied by Validate")

	mc.Insights.BatchSize = config.MaxInsightsBatchSize + 1
	require.ErrorContains(t, mc.Validate(), "insights config is invalid")

	ac := validAPIConfig()
	require.NoError(t, ac.Validate())

	ac.Insights = &config.InsightsConfig{}
	require.NoError(t, ac.Validate())

	ac.Insights.BufferSize = 1
	require.ErrorContains(t, ac.Validate(), "insights config is invalid")
}
