package config

import "errors"

type SlackClientConfig struct {
	AppToken                         string `json:"appToken"                         yaml:"appToken"`
	BotToken                         string `json:"botToken"                         yaml:"botToken"`
	DebugLogging                     bool   `json:"debugLogging"                     yaml:"debugLogging"`
	DryRun                           bool   `json:"dryRun"                           yaml:"dryRun"`
	Concurrency                      int    `json:"concurrency"                      yaml:"concurrency"`
	MaxAttemptsForRateLimitError     int    `json:"maxAttemptsForRateLimitError"      yaml:"maxAttemptsForRateLimitError"`
	MaxAttemptsForTransientError     int    `json:"maxAttemptsForTransientError"     yaml:"maxAttemptsForTransientError"`
	MaxAttemptsForFatalError         int    `json:"maxAttemptsForFatalError"         yaml:"maxAttemptsForFatalError"`
	MaxRateLimitErrorWaitTimeSeconds int    `json:"maxRateLimitErrorWaitTimeSeconds" yaml:"maxRateLimitErrorWaitTimeSeconds"`
	MaxTransientErrorWaitTimeSeconds int    `json:"maxTransientErrorWaitTimeSeconds" yaml:"maxTransientErrorWaitTimeSeconds"`
	MaxFatalErrorWaitTimeSeconds     int    `json:"maxFatalErrorWaitTimeSeconds"     yaml:"maxFatalErrorWaitTimeSeconds"`
	HTTPTimeoutSeconds               int    `json:"httpTimeoutSeconds"               yaml:"httpTimeoutSeconds"`
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

	if c.MaxAttemptsForRateLimitError <= 0 {
		c.MaxAttemptsForRateLimitError = 10
	}

	if c.MaxAttemptsForTransientError <= 0 {
		c.MaxAttemptsForTransientError = 5
	}

	if c.MaxAttemptsForFatalError <= 0 {
		c.MaxAttemptsForFatalError = 5
	}

	if c.MaxRateLimitErrorWaitTimeSeconds <= 0 {
		c.MaxRateLimitErrorWaitTimeSeconds = 120
	}

	if c.MaxTransientErrorWaitTimeSeconds <= 0 {
		c.MaxTransientErrorWaitTimeSeconds = 30
	}

	if c.MaxFatalErrorWaitTimeSeconds <= 0 {
		c.MaxFatalErrorWaitTimeSeconds = 30
	}

	if c.HTTPTimeoutSeconds <= 0 {
		c.HTTPTimeoutSeconds = 30
	}
}

func (c *SlackClientConfig) Validate() error {
	if c.AppToken == "" {
		return errors.New("app token is empty")
	}

	if c.BotToken == "" {
		return errors.New("bot token is empty")
	}

	if c.HTTPTimeoutSeconds <= 0 {
		return errors.New("http timeout must be greater than 0")
	}

	return nil
}
