package common

import "time"

type SlackOptions struct {
	AppToken     string
	BotToken     string
	DebugLogging bool
	DryRun       bool
	Concurrency  int

	MaxAttemtsForRateLimitError  int
	MaxAttemptsForTransientError int
	MaxAttemptsForFatalError     int

	MaxRateLimitErrorWaitTime time.Duration
	MaxTransientErrorWaitTime time.Duration
	MaxFatalErrorWaitTime     time.Duration

	HTTPTimeout time.Duration
}

func (o *SlackOptions) SetDefaults() {
	if o.MaxAttemtsForRateLimitError <= 0 {
		o.MaxAttemtsForRateLimitError = 10
	}

	if o.MaxAttemptsForTransientError <= 0 {
		o.MaxAttemptsForTransientError = 5
	}

	if o.MaxAttemptsForFatalError <= 0 {
		o.MaxAttemptsForFatalError = 5
	}

	if o.MaxRateLimitErrorWaitTime <= 0 {
		o.MaxRateLimitErrorWaitTime = 2 * time.Minute
	}

	if o.MaxTransientErrorWaitTime <= 0 {
		o.MaxTransientErrorWaitTime = 30 * time.Second
	}

	if o.MaxFatalErrorWaitTime <= 0 {
		o.MaxFatalErrorWaitTime = 30 * time.Second
	}

	if o.HTTPTimeout <= 0 {
		o.HTTPTimeout = 30 * time.Second
	}
}
