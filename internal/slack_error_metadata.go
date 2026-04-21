package internal

import (
	"errors"
	"net/url"
	"regexp"

	"github.com/slack-go/slack"
)

const (
	// SlackErrChannelNotFound is returned when the target channel does not exist or
	// the bot lacks visibility of it. Non-retryable.
	SlackErrChannelNotFound = "channel_not_found"

	// SlackErrThreadNotFound is returned when the target thread does not exist,
	// typically because the parent message was deleted. Non-retryable.
	SlackErrThreadNotFound = "thread_not_found"

	// SlackErrNoSuchSubTeam is returned when a referenced user group does not exist
	// in the workspace. Non-retryable.
	SlackErrNoSuchSubTeam = "no_such_subteam"

	// SlackErrCantUpdateMessage is returned when the bot attempts to edit a message
	// it is not permitted to modify (e.g. another user's message). Non-retryable.
	SlackErrCantUpdateMessage = "cant_update_message"

	// SlackErrCantDeleteMessage is returned when the bot attempts to delete a message
	// it is not permitted to remove (e.g. another user's message). Non-retryable.
	SlackErrCantDeleteMessage = "cant_delete_message"

	// SlackErrMessageNotFound is returned when the target message does not exist,
	// typically because it was already deleted. Non-retryable.
	SlackErrMessageNotFound = "message_not_found"

	// SlackErrNotInChannel is returned when the bot is not a member of the channel
	// it is trying to interact with. Non-retryable.
	SlackErrNotInChannel = "not_in_channel"

	// SlackErrInvalidBlocks is returned when a message payload contains malformed
	// Block Kit JSON. Non-retryable.
	SlackErrInvalidBlocks = "invalid_blocks"

	// SlackErrIsArchived is returned when the target channel has been archived and
	// no longer accepts messages. Non-retryable.
	SlackErrIsArchived = "is_archived"

	// SlackErrInternalError is a transient server-side error returned by Slack when
	// something went wrong on their end. Safe to retry.
	SlackErrInternalError = "internal_error"

	// SlackErrFatalError is a transient error string returned by Slack for unspecified
	// server-side failures. Despite the name, Slack treats this as retryable.
	SlackErrFatalError = "fatal_error"

	// SlackErrServiceUnavailable is a transient error returned when the Slack API
	// is temporarily unavailable. Safe to retry.
	SlackErrServiceUnavailable = "service_unavailable"

	// SlackErrRequestTimeout is a transient error returned when Slack's own
	// processing timed out handling the request. Safe to retry.
	SlackErrRequestTimeout = "request_timeout"

	// SlackErrRateLimited is returned when the client has hit a rate limit.
	// Safe to retry after a delay, using the Retry-After header if present.
	SlackErrRateLimited = "ratelimited"

	// SlackErrRateLimitedV2 is returned when the client has hit a rate limit.
	// Safe to retry after a delay, using the Retry-After header if present.
	SlackErrRateLimitedV2 = "rate_limited"
)

// slackErrCodeRegex is used to validate that an error string represents a Slack API error code,
// which should be lowercase letters and underscores only.
var slackErrCodeRegex = regexp.MustCompile(`^[a-z_]+$`)

// retryable is satisfied by any error type that can indicate whether the error is safe to retry.
// This construction is used inside the slack-go package.
type retryable interface{ Retryable() bool }

//nolint:gochecknoglobals
var transientErrorStrings = map[string]struct{}{
	SlackErrInternalError:      {},
	SlackErrFatalError:         {},
	SlackErrServiceUnavailable: {},
	SlackErrRequestTimeout:     {},
	SlackErrRateLimited:        {},
	SlackErrRateLimitedV2:      {},
}

// getSlackErrorMetricLabel returns a metric label for a Slack API error.
func getSlackErrorMetricLabel(err error) string {
	if err == nil {
		return "unknown"
	}

	var rateLimitErr *slack.RateLimitedError
	if errors.As(err, &rateLimitErr) {
		return "rate_limited"
	}

	// For known Slack API errors, the error string is the error code.
	// Validate that it looks like a Slack error code before using it as a
	// metric label; otherwise, classify as "unknown" to avoid high-cardinality
	// metrics from unexpected error strings.
	if slackErrCodeRegex.MatchString(err.Error()) {
		return err.Error()
	}

	return "unknown"
}

// isTransientError reports whether err should be considered transient and safe to retry.
// This includes both errors that Slack explicitly classifies as retryable, as well as
// certain client-side errors like timeouts that may indicate a temporary issue.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return true
	}

	if v, ok := any(err).(retryable); ok && v.Retryable() {
		return true
	}

	_, ok := transientErrorStrings[err.Error()]

	return ok
}
