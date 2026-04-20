package internal

import (
	"context"
	"errors"
	"net/url"
	"testing"

	slackapi "github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
)

func TestClassifyError(t *testing.T) {
	t.Parallel()

	timeoutURLErr := &url.Error{Op: "Post", URL: "https://slack.com/api/test", Err: context.DeadlineExceeded}
	nonTimeoutURLErr := &url.Error{Op: "Post", URL: "https://slack.com/api/test", Err: errors.New("connection refused")}

	tests := []struct {
		name      string
		err       error
		wantLabel string
		wantTrans bool
		wantRetry bool
	}{
		{
			name:      "rate limit error",
			err:       &slackapi.RateLimitedError{},
			wantLabel: "rate_limited",
			wantTrans: false,
			wantRetry: true,
		},
		{
			name:      "url error with timeout",
			err:       timeoutURLErr,
			wantLabel: "transient",
			wantTrans: true,
			wantRetry: true,
		},
		{
			name:      "url error without timeout",
			err:       nonTimeoutURLErr,
			wantLabel: "unknown",
			wantTrans: false,
			wantRetry: true,
		},
		{
			name:      "retryable interface true",
			err:       &testError{Code: 503},
			wantLabel: "transient",
			wantTrans: true,
			wantRetry: true,
		},
		{
			name:      "retryable interface false",
			err:       &testError{Code: 499},
			wantLabel: "unknown",
			wantTrans: false,
			wantRetry: true,
		},
		{
			name:      "internal_error string",
			err:       errors.New("internal_error"),
			wantLabel: "transient",
			wantTrans: true,
			wantRetry: true,
		},
		{
			name:      "service_unavailable string",
			err:       errors.New("service_unavailable"),
			wantLabel: "transient",
			wantTrans: true,
			wantRetry: true,
		},
		{
			name:      "ratelimited string",
			err:       errors.New("ratelimited"),
			wantLabel: "transient",
			wantTrans: true,
			wantRetry: true,
		},
		{
			name:      "channel_not_found",
			err:       errors.New("channel_not_found"),
			wantLabel: "channel_not_found",
			wantTrans: false,
			wantRetry: false,
		},
		{
			name:      "cant_delete_message",
			err:       errors.New("cant_delete_message"),
			wantLabel: "cant_delete_message",
			wantTrans: false,
			wantRetry: false,
		},
		{
			name:      "invalid_blocks",
			err:       errors.New("invalid_blocks"),
			wantLabel: "invalid_blocks",
			wantTrans: false,
			wantRetry: false,
		},
		{
			name:      "is_archived",
			err:       errors.New("is_archived"),
			wantLabel: "is_archived",
			wantTrans: false,
			wantRetry: false,
		},
		{
			name:      "restricted_action",
			err:       errors.New("restricted_action"),
			wantLabel: "restricted_action",
			wantTrans: false,
			wantRetry: false,
		},
		{
			name:      "unknown error string",
			err:       errors.New("foo"),
			wantLabel: "unknown",
			wantTrans: false,
			wantRetry: true,
		},
		{
			name:      "nil error",
			err:       nil,
			wantLabel: "unknown",
			wantTrans: false,
			wantRetry: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			meta := classifyError(tc.err)

			assert.Equal(t, tc.wantLabel, meta.label, "label")
			assert.Equal(t, tc.wantTrans, meta.isTransient, "transient")
			assert.Equal(t, tc.wantRetry, meta.isRetryable, "retryable")
		})
	}
}

func TestIsTransientError(t *testing.T) {
	t.Parallel()

	timeoutURLErr := &url.Error{Op: "Post", URL: "https://slack.com/api/test", Err: context.DeadlineExceeded}

	assert.True(t, isTransientError(timeoutURLErr), "url timeout should be transient")
	assert.True(t, isTransientError(errors.New("internal_error")), "internal_error should be transient")
	assert.False(t, isTransientError(&slackapi.RateLimitedError{}), "rate limit is not transient (has own retry path)")
	assert.False(t, isTransientError(errors.New("channel_not_found")), "channel_not_found is not transient")
	assert.False(t, isTransientError(errors.New("foo")), "unknown error is not transient")
}

func TestIsNonRetryableError(t *testing.T) {
	t.Parallel()

	assert.False(t, isRetryableError(errors.New("channel_not_found")), "channel_not_found is non-retryable")
	assert.False(t, isRetryableError(errors.New("restricted_action")), "restricted_action is non-retryable")
	assert.True(t, isRetryableError(errors.New("internal_error")), "internal_error is retryable")
	assert.True(t, isRetryableError(errors.New("foo")), "unknown error is retryable (falls to fatal path)")
	assert.True(t, isRetryableError(nil), "nil is not non-retryable")
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
