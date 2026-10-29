package internal

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"

	slackapi "github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
)

// TestGetSlackErrorMetricLabel verifies the metric label returned for each error category.
//
// The label logic is:
//   - *slack.RateLimitedError (typed) → "rate_limited"
//   - Any error whose string matches ^[a-z_]+$ → the error string itself
//   - Anything else (complex strings, empty string, nil) → "unknown"
func TestGetSlackErrorMetricLabel(t *testing.T) {
	t.Parallel()

	timeoutURLErr := &url.Error{Op: "Post", URL: "https://slack.com/api/test", Err: context.DeadlineExceeded}
	nonTimeoutURLErr := &url.Error{Op: "Post", URL: "https://slack.com/api/test", Err: errors.New("connection refused")}

	tests := []struct {
		name      string
		err       error
		wantLabel string
	}{
		// Typed rate limit error always wins.
		{name: "typed rate limit error", err: &slackapi.RateLimitedError{}, wantLabel: "rate_limited"},

		// Known Slack error code strings — pass through as-is (match ^[a-z_]+$).
		{name: "ratelimited string", err: errors.New(SlackErrRateLimited), wantLabel: "ratelimited"},
		{name: "rate_limited string", err: errors.New(SlackErrRateLimitedV2), wantLabel: "rate_limited"},
		{name: "internal_error string", err: errors.New(SlackErrInternalError), wantLabel: "internal_error"},
		{name: "fatal_error string", err: errors.New(SlackErrFatalError), wantLabel: "fatal_error"},
		{name: "service_unavailable string", err: errors.New(SlackErrServiceUnavailable), wantLabel: "service_unavailable"},
		{name: "request_timeout string", err: errors.New(SlackErrRequestTimeout), wantLabel: "request_timeout"},
		{name: "channel_not_found string", err: errors.New(SlackErrChannelNotFound), wantLabel: "channel_not_found"},
		{name: "thread_not_found string", err: errors.New(SlackErrThreadNotFound), wantLabel: "thread_not_found"},
		{name: "no_such_subteam string", err: errors.New(SlackErrNoSuchSubTeam), wantLabel: "no_such_subteam"},
		{name: "cant_update_message string", err: errors.New(SlackErrCantUpdateMessage), wantLabel: "cant_update_message"},
		{name: "cant_delete_message string", err: errors.New(SlackErrCantDeleteMessage), wantLabel: "cant_delete_message"},
		{name: "message_not_found string", err: errors.New(SlackErrMessageNotFound), wantLabel: "message_not_found"},
		{name: "not_in_channel string", err: errors.New(SlackErrNotInChannel), wantLabel: "not_in_channel"},
		{name: "invalid_blocks string", err: errors.New(SlackErrInvalidBlocks), wantLabel: "invalid_blocks"},
		{name: "is_archived string", err: errors.New(SlackErrIsArchived), wantLabel: "is_archived"},
		{name: "arbitrary lowercase_underscore string", err: errors.New("some_slack_error"), wantLabel: "some_slack_error"},

		// Complex error strings that do NOT match ^[a-z_]+$ → "unknown".
		{name: "url timeout error", err: timeoutURLErr, wantLabel: "unknown"},
		{name: "url non-timeout error", err: nonTimeoutURLErr, wantLabel: "unknown"},
		{name: "retryable interface true (empty string)", err: &testError{Code: 503}, wantLabel: "unknown"},
		{name: "retryable interface false (empty string)", err: &testError{Code: 499}, wantLabel: "unknown"},
		{name: "string with uppercase", err: errors.New("ChannelNotFound"), wantLabel: "unknown"},
		{name: "string with digits", err: errors.New("error_404"), wantLabel: "unknown"},
		{name: "string with space", err: errors.New("channel not found"), wantLabel: "unknown"},
		{name: "empty string", err: errors.New(""), wantLabel: "unknown"},
		{name: "nil error", err: nil, wantLabel: "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.wantLabel, getSlackErrorMetricLabel(tc.err))
		})
	}
}

// TestIsTransientError verifies which error types trigger the transient retry path.
//
// Transient errors are retried with linear backoff. Note that *slack.RateLimitedError
// is also classified as transient (via the Retryable() interface), but waitForAPIError
// handles rate limit errors before ever calling IsTransientError.
func TestIsTransientError(t *testing.T) {
	t.Parallel()

	timeoutURLErr := &url.Error{Op: "Post", URL: "https://slack.com/api/test", Err: context.DeadlineExceeded}
	nonTimeoutURLErr := &url.Error{Op: "Post", URL: "https://slack.com/api/test", Err: errors.New("connection refused")}

	transient := []struct {
		name string
		err  error
	}{
		{name: "url timeout", err: timeoutURLErr},
		{name: "Retryable() == true (HTTP 5xx)", err: &testError{Code: 503}},
		{name: "rate limit typed (Retryable() == true)", err: &slackapi.RateLimitedError{}},
		{name: SlackErrInternalError, err: errors.New(SlackErrInternalError)},
		{name: SlackErrFatalError, err: errors.New(SlackErrFatalError)},
		{name: SlackErrServiceUnavailable, err: errors.New(SlackErrServiceUnavailable)},
		{name: SlackErrRequestTimeout, err: errors.New(SlackErrRequestTimeout)},
		{name: SlackErrRateLimited, err: errors.New(SlackErrRateLimited)},
		{name: SlackErrRateLimitedV2, err: errors.New(SlackErrRateLimitedV2)},
		{name: SlackErrUpdateFailed, err: errors.New(SlackErrUpdateFailed)},
	}

	nonTransient := []struct {
		name string
		err  error
	}{
		{name: "nil", err: nil},
		{name: "url non-timeout", err: nonTimeoutURLErr},
		{name: "Retryable() == false (HTTP 4xx)", err: &testError{Code: 499}},
		{name: SlackErrChannelNotFound, err: errors.New(SlackErrChannelNotFound)},
		{name: SlackErrMessageNotFound, err: errors.New(SlackErrMessageNotFound)},
		{name: SlackErrNotInChannel, err: errors.New(SlackErrNotInChannel)},
		{name: SlackErrIsArchived, err: errors.New(SlackErrIsArchived)},
		{name: SlackErrIsInactive, err: errors.New(SlackErrIsInactive)},
		{name: SlackErrEditWindowClosed, err: errors.New(SlackErrEditWindowClosed)},
		{name: SlackErrMsgBlocksTooLong, err: errors.New(SlackErrMsgBlocksTooLong)},
		{name: "unknown error string", err: errors.New("foo")},
	}

	for _, tc := range transient {
		t.Run("transient/"+tc.name, func(t *testing.T) {
			t.Parallel()
			assert.True(t, IsTransientError(tc.err))
		})
	}

	for _, tc := range nonTransient {
		t.Run("non_transient/"+tc.name, func(t *testing.T) {
			t.Parallel()
			assert.False(t, IsTransientError(tc.err))
		})
	}
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

// TestClassifySlackError verifies the class of every known Slack error code, and the fallback for others.
func TestClassifySlackError(t *testing.T) {
	t.Parallel()

	tests := map[string]SlackErrorClass{
		SlackErrInternalError:      SlackErrorClassTransient,
		SlackErrFatalError:         SlackErrorClassTransient,
		SlackErrServiceUnavailable: SlackErrorClassTransient,
		SlackErrRequestTimeout:     SlackErrorClassTransient,
		SlackErrUpdateFailed:       SlackErrorClassTransient,

		SlackErrRateLimited:   SlackErrorClassRateLimited,
		SlackErrRateLimitedV2: SlackErrorClassRateLimited,

		SlackErrNotAuthed:       SlackErrorClassAuth,
		SlackErrInvalidAuth:     SlackErrorClassAuth,
		SlackErrAccountInactive: SlackErrorClassAuth,
		SlackErrTokenRevoked:    SlackErrorClassAuth,
		SlackErrTokenExpired:    SlackErrorClassAuth,
		SlackErrMissingScope:    SlackErrorClassAuth,

		SlackErrChannelNotFound:                   SlackErrorClassChannelUnavailable,
		SlackErrInvalidChannelID:                  SlackErrorClassChannelUnavailable,
		SlackErrIsArchived:                        SlackErrorClassChannelUnavailable,
		SlackErrIsInactive:                        SlackErrorClassChannelUnavailable,
		SlackErrRestrictedAction:                  SlackErrorClassChannelUnavailable,
		SlackErrRestrictedActionReadOnlyChannel:   SlackErrorClassChannelUnavailable,
		SlackErrRestrictedActionThreadOnlyChannel: SlackErrorClassChannelUnavailable,
		SlackErrEKMAccessDenied:                   SlackErrorClassChannelUnavailable,

		SlackErrNotInChannel: SlackErrorClassNotInChannel,

		SlackErrMessageNotFound:   SlackErrorClassMessageUnavailable,
		SlackErrCantUpdateMessage: SlackErrorClassMessageUnavailable,
		SlackErrCantDeleteMessage: SlackErrorClassMessageUnavailable,
		SlackErrEditWindowClosed:  SlackErrorClassMessageUnavailable,

		SlackErrInvalidBlocks:                  SlackErrorClassInvalidContent,
		SlackErrInvalidBlocksFormat:            SlackErrorClassInvalidContent,
		SlackErrMsgBlocksTooLong:               SlackErrorClassInvalidContent,
		SlackErrMsgTooLong:                     SlackErrorClassInvalidContent,
		SlackErrTooManyAttachments:             SlackErrorClassInvalidContent,
		SlackErrAttachmentPayloadLimitExceeded: SlackErrorClassInvalidContent,
		SlackErrBlockMismatch:                  SlackErrorClassInvalidContent,
		SlackErrNoText:                         SlackErrorClassInvalidContent,

		SlackErrNoSuchSubTeam:  SlackErrorClassOther,
		SlackErrThreadNotFound: SlackErrorClassOther,
		"unknown error":        SlackErrorClassOther,
	}

	for slackErr, want := range tests {
		assert.Equal(t, want, ClassifySlackError(errors.New(slackErr)), slackErr)
	}

	assert.Equal(t, SlackErrorClassOther, ClassifySlackError(nil))
}

// TestClassifySlackError_Wrapped verifies that wrapped Slack errors are classified by their underlying code.
func TestClassifySlackError_Wrapped(t *testing.T) {
	t.Parallel()

	wrappedResponse := fmt.Errorf("failed to call Slack API: transient error: %w", slackapi.SlackErrorResponse{Err: SlackErrInternalError})
	assert.Equal(t, SlackErrorClassTransient, ClassifySlackError(wrappedResponse))

	wrappedAuth := fmt.Errorf("failed: %w", slackapi.SlackErrorResponse{Err: SlackErrInvalidAuth})
	assert.Equal(t, SlackErrorClassAuth, ClassifySlackError(wrappedAuth))

	wrappedUnknown := fmt.Errorf("failed: %w", slackapi.SlackErrorResponse{Err: "something_else"})
	assert.Equal(t, SlackErrorClassOther, ClassifySlackError(wrappedUnknown))

	wrappedRateLimit := fmt.Errorf("failed: rate limit error: %w", &slackapi.RateLimitedError{})
	assert.Equal(t, SlackErrorClassRateLimited, ClassifySlackError(wrappedRateLimit))

	// A bare code created with errors.New cannot be unwrapped to a Slack error, and is not classified.
	assert.Equal(t, SlackErrorClassOther, ClassifySlackError(fmt.Errorf("failed: %w", errors.New(SlackErrInternalError))))
}

// TestIsTransientError_Wrapped verifies that transient errors are still detected after wrapping.
func TestIsTransientError_Wrapped(t *testing.T) {
	t.Parallel()

	timeoutURLErr := &url.Error{Op: "Post", URL: "https://slack.com/api/test", Err: context.DeadlineExceeded}

	assert.True(t, IsTransientError(fmt.Errorf("wrapped: %w", timeoutURLErr)))
	assert.True(t, IsTransientError(fmt.Errorf("wrapped: %w", &testError{Code: 503})))
	assert.True(t, IsTransientError(fmt.Errorf("wrapped: %w", &slackapi.RateLimitedError{})))
	assert.True(t, IsTransientError(fmt.Errorf("wrapped: %w", slackapi.SlackErrorResponse{Err: SlackErrServiceUnavailable})))
	assert.False(t, IsTransientError(fmt.Errorf("wrapped: %w", slackapi.SlackErrorResponse{Err: SlackErrIsArchived})))
	assert.False(t, IsTransientError(fmt.Errorf("wrapped: %w", &testError{Code: 499})))
}
