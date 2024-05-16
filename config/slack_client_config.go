package config

import "time"

type SlackClientConfig struct {
	AppToken                     string
	BotToken                     string
	DebugLogging                 bool
	DryRun                       bool
	Concurrency                  int
	MaxAttemtsForRateLimitError  int
	MaxAttemptsForTransientError int
	MaxAttemptsForFatalError     int
	MaxRateLimitErrorWaitTime    time.Duration
	MaxTransientErrorWaitTime    time.Duration
	MaxFatalErrorWaitTime        time.Duration
	HTTPTimeout                  time.Duration
}

func NewDefaultSlackClientConfig() SlackClientConfig {
	c := SlackClientConfig{}
	c.SetDefaults()
	return c
}

func (c *SlackClientConfig) SetDefaults() {
	if c.Concurrency <= 0 {
		c.Concurrency = 3
	}

	if c.MaxAttemtsForRateLimitError <= 0 {
		c.MaxAttemtsForRateLimitError = 10
	}

	if c.MaxAttemptsForTransientError <= 0 {
		c.MaxAttemptsForTransientError = 5
	}

	if c.MaxAttemptsForFatalError <= 0 {
		c.MaxAttemptsForFatalError = 5
	}

	if c.MaxRateLimitErrorWaitTime <= 0 {
		c.MaxRateLimitErrorWaitTime = 2 * time.Minute
	}

	if c.MaxTransientErrorWaitTime <= 0 {
		c.MaxTransientErrorWaitTime = 30 * time.Second
	}

	if c.MaxFatalErrorWaitTime <= 0 {
		c.MaxFatalErrorWaitTime = 30 * time.Second
	}

	if c.HTTPTimeout <= 0 {
		c.HTTPTimeout = 30 * time.Second
	}
}
