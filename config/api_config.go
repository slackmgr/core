package config

import (
	"fmt"
	"time"
)

type RateLimitConfig struct {
	AlertsPerSecond   float64
	AllowedBurst      int
	MaxWaitPerAttempt time.Duration
	MaxAttempts       int
}

type APIConfig struct {
	RestPort               string
	EncryptionKey          string
	CachePrefix            string
	ErrorReportChannelID   string
	MaxUsersInAlertChannel int
	RateLimit              RateLimitConfig
	SlackClient            SlackClientConfig
	alertMapping           *AlertMapping
}

func NewDefaultAPIConfig() *APIConfig {
	return &APIConfig{
		RestPort:               "8080",
		EncryptionKey:          "",
		CachePrefix:            "slack-manager",
		ErrorReportChannelID:   "",
		MaxUsersInAlertChannel: 100,
		RateLimit: RateLimitConfig{
			AlertsPerSecond:   0.5,
			AllowedBurst:      10,
			MaxWaitPerAttempt: 10 * time.Second,
			MaxAttempts:       3,
		},
		SlackClient: NewDefaultSlackClientConfig(),
	}
}

func (c *APIConfig) SetAlertMapping(alertMapping *AlertMapping) error {
	if err := alertMapping.Init(); err != nil {
		return fmt.Errorf("failed to update alert mapping: %w", err)
	}

	c.alertMapping = alertMapping

	return nil
}

func (c *APIConfig) GetAlertMappings() *AlertMapping {
	return c.alertMapping
}

func (c *APIConfig) GetRecommendedRequestTimeout() time.Duration {
	apiRateLimitMaxTime := c.RateLimit.MaxWaitPerAttempt * time.Duration(c.RateLimit.MaxAttempts)

	return max(apiRateLimitMaxTime, c.SlackClient.MaxRateLimitErrorWaitTime, c.SlackClient.MaxTransientErrorWaitTime, c.SlackClient.MaxFatalErrorWaitTime) + time.Second
}

func max(values ...time.Duration) time.Duration {
	max := time.Duration(0)

	for _, v := range values {
		if v > max {
			max = v
		}
	}

	return max
}
