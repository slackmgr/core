package internal

import (
	"context"
	"errors"
	"net/url"
	"testing"

	slackapi "github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
)

func TestClassifySlackError(t *testing.T) {
	t.Parallel()

	timeoutURLErr := &url.Error{Op: "Post", URL: "https://slack.com/api/test", Err: context.DeadlineExceeded}

	tests := []struct {
		name      string
		err       error
		wantLabel string
	}{
		{name: "rate limit typed error", err: &slackapi.RateLimitedError{}, wantLabel: "rate_limited"},
		{name: "url timeout", err: timeoutURLErr, wantLabel: "transient"},
		{name: "retryable interface true", err: &testError{Code: 503}, wantLabel: "transient"},
		{name: "internal_error string", err: errors.New("internal_error"), wantLabel: "transient"},
		{name: "service_unavailable string", err: errors.New("service_unavailable"), wantLabel: "transient"},
		{name: "channel_not_found", err: errors.New("channel_not_found"), wantLabel: "non_retryable"},
		{name: "url error without timeout", err: &url.Error{Op: "Post", URL: "https://slack.com/api/test", Err: errors.New("connection refused")}, wantLabel: "non_retryable"},
		{name: "retryable interface false", err: &testError{Code: 499}, wantLabel: "non_retryable"},
		{name: "unknown error string", err: errors.New("foo"), wantLabel: "non_retryable"},
		{name: "nil error", err: nil, wantLabel: "non_retryable"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.wantLabel, classifySlackError(tc.err))
		})
	}
}

func TestIsTransientError(t *testing.T) {
	t.Parallel()

	timeoutURLErr := &url.Error{Op: "Post", URL: "https://slack.com/api/test", Err: context.DeadlineExceeded}
	nonTimeoutURLErr := &url.Error{Op: "Post", URL: "https://slack.com/api/test", Err: errors.New("connection refused")}

	assert.True(t, isTransientError(timeoutURLErr), "url timeout should be transient")
	assert.True(t, isTransientError(errors.New("internal_error")), "internal_error should be transient")
	assert.True(t, isTransientError(errors.New("fatal_error")), "fatal_error should be transient")
	assert.True(t, isTransientError(errors.New("service_unavailable")), "service_unavailable should be transient")
	assert.True(t, isTransientError(errors.New("request_timeout")), "request_timeout should be transient")
	assert.True(t, isTransientError(&testError{Code: 503}), "Retryable() true should be transient")
	assert.False(t, isTransientError(&slackapi.RateLimitedError{}), "rate limit is not transient (has own retry path)")
	assert.False(t, isTransientError(nonTimeoutURLErr), "url error without timeout is not transient")
	assert.False(t, isTransientError(&testError{Code: 499}), "Retryable() false is not transient")
	assert.False(t, isTransientError(errors.New("channel_not_found")), "channel_not_found is not transient")
	assert.False(t, isTransientError(errors.New("foo")), "unknown error is not transient")
	assert.False(t, isTransientError(nil), "nil is not transient")
}

type testError struct {
	Code   int
	Status string
}

func (t testError) Error() string {
	return ""
}

func (t testError) Retryable() bool {
	return t.Code >= 500
}
