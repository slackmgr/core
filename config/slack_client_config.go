package config

import (
	"fmt"
	"time"
)

type SlackClientConfig struct {
	AppToken                     string        `json:"appToken"                     yaml:"appToken"`
	BotToken                     string        `json:"botToken"                     yaml:"botToken"`
	DebugLogging                 bool          `json:"debugLogging"                 yaml:"debugLogging"`
	DryRun                       bool          `json:"dryRun"                       yaml:"dryRun"`
	Concurrency                  int           `json:"concurrency"                  yaml:"concurrency"`
	MaxAttemtsForRateLimitError  int           `json:"maxAttemtsForRateLimitError"  yaml:"maxAttemtsForRateLimitError"`
	MaxAttemptsForTransientError int           `json:"maxAttemptsForTransientError" yaml:"maxAttemptsForTransientError"`
	MaxAttemptsForFatalError     int           `json:"maxAttemptsForFatalError"     yaml:"maxAttemptsForFatalError"`
	MaxRateLimitErrorWaitTime    time.Duration `json:"maxRateLimitErrorWaitTime"    yaml:"maxRateLimitErrorWaitTime"`
	MaxTransientErrorWaitTime    time.Duration `json:"maxTransientErrorWaitTime"    yaml:"maxTransientErrorWaitTime"`
	MaxFatalErrorWaitTime        time.Duration `json:"maxFatalErrorWaitTime"        yaml:"maxFatalErrorWaitTime"`
	HTTPTimeout                  time.Duration `json:"httpTimeout"                  yaml:"httpTimeout"`
}

func NewDefaultSlackClientConfig() *SlackClientConfig {
	c := &SlackClientConfig{}
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

func (c *SlackClientConfig) Validate() error {
	if c.AppToken == "" {
		return fmt.Errorf("app token is empty")
	}

	if c.BotToken == "" {
		return fmt.Errorf("bot token is empty")
	}

	if c.HTTPTimeout <= 0 {
		return fmt.Errorf("http timeout must be greater than 0")
	}

	return nil
}
