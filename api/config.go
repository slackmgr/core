package api

import (
	"time"
)

type RateLimitConfig struct {
	AlertsPerSecond   float64
	AllowedBurst      int
	MaxWaitPerAttempt time.Duration
	MaxAttempts       int
}

type SlackConfig struct {
	BotToken                     string
	AppToken                     string
	Debug                        bool
	MaxChannelUserCount          int
	MaxAttemtsForRateLimitError  int
	MaxAttemptsForTransientError int
	MaxAttemptsForFatalError     int
	MaxRateLimitErrorWaitTime    time.Duration
	MaxTransientErrorWaitTime    time.Duration
	MaxFatalErrorWaitTime        time.Duration
	HTTPTimeout                  time.Duration
}

type Config struct {
	RestPort             string
	EncryptionKey        string
	CachePrefix          string
	ErrorReportChannelID string
	RateLimit            RateLimitConfig
	Slack                SlackConfig

	alertMappings *RouteMapping
}

func NewConfig() *Config {
	return &Config{}
}

func (c *Config) SetAlertMappings(alertMappings *RouteMapping) error {
	if err := alertMappings.Init(); err != nil {
		return err
	}

	c.alertMappings = alertMappings

	return nil
}

func (c *Config) GetAlertMappings() *RouteMapping {
	return c.alertMappings
}

func (c *Config) GetRecommendedRequestTimeout() time.Duration {
	apiRateLimitMaxTime := c.RateLimit.MaxWaitPerAttempt * time.Duration(c.RateLimit.MaxAttempts)

	return max(apiRateLimitMaxTime, c.Slack.MaxRateLimitErrorWaitTime, c.Slack.MaxTransientErrorWaitTime, c.Slack.MaxFatalErrorWaitTime) + time.Second
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
