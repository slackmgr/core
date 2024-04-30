package client

import "time"

type Option func(*Options)

type Options struct {
	retryCount       int
	retryWaitTime    time.Duration
	retryMaxWaitTime time.Duration
}

func newClientOptions() *Options {
	return &Options{
		retryCount:       8,
		retryWaitTime:    500 * time.Millisecond,
		retryMaxWaitTime: 3 * time.Second,
	}
}

func RetryCount(count int) Option {
	return func(conf *Options) {
		conf.retryCount = count
	}
}

func RetryWaitTime(duration time.Duration) Option {
	return func(conf *Options) {
		conf.retryWaitTime = duration
	}
}

func RetryMaxWaitTime(duration time.Duration) Option {
	return func(conf *Options) {
		conf.retryMaxWaitTime = duration
	}
}
