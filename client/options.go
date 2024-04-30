package client

import "time"

type Options func(*options)

type options struct {
	retryCount       int
	retryWaitTime    time.Duration
	retryMaxWaitTime time.Duration
}

func newClientOptions() *options {
	return &options{
		retryCount:       8,
		retryWaitTime:    500 * time.Millisecond,
		retryMaxWaitTime: 3 * time.Second,
	}
}

func RetryCount(count int) Options {
	return func(conf *options) {
		conf.retryCount = count
	}
}

func RetryWaitTime(duration time.Duration) Options {
	return func(conf *options) {
		conf.retryWaitTime = duration
	}
}

func RetryMaxWaitTime(duration time.Duration) Options {
	return func(conf *options) {
		conf.retryMaxWaitTime = duration
	}
}
