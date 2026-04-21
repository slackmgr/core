package internal

import (
	"errors"
	"net/url"

	"github.com/slack-go/slack"
)

const (
	// SlackChannelNotFoundError is returned when the target channel does not exist or
	// the bot lacks visibility of it. Non-retryable.
	SlackChannelNotFoundError = "channel_not_found"

	// SlackThreadNotFoundError is returned when the target thread does not exist,
	// typically because the parent message was deleted. Non-retryable.
	SlackThreadNotFoundError = "thread_not_found"

	// SlackNoSuchSubTeamError is returned when a referenced user group does not exist
	// in the workspace. Non-retryable.
	SlackNoSuchSubTeamError = "no_such_subteam"

	// SlackCantUpdateMessageError is returned when the bot attempts to edit a message
	// it is not permitted to modify (e.g. another user's message). Non-retryable.
	SlackCantUpdateMessageError = "cant_update_message"

	// SlackCantDeleteMessageError is returned when the bot attempts to delete a message
	// it is not permitted to remove (e.g. another user's message). Non-retryable.
	SlackCantDeleteMessageError = "cant_delete_message"

	// SlackMessageNotFoundError is returned when the target message does not exist,
	// typically because it was already deleted. Non-retryable.
	SlackMessageNotFoundError = "message_not_found"

	// SlackNotInChannelError is returned when the bot is not a member of the channel
	// it is trying to interact with. Non-retryable.
	SlackNotInChannelError = "not_in_channel"

	// SlackInvalidBlocksError is returned when a message payload contains malformed
	// Block Kit JSON. Non-retryable.
	SlackInvalidBlocksError = "invalid_blocks"

	// SlackIsArchivedError is returned when the target channel has been archived and
	// no longer accepts messages. Non-retryable.
	SlackIsArchivedError = "is_archived"

	// SlackRestrictedActionError is returned when a workspace policy prevents the
	// requested action (e.g. posting is restricted to certain roles). Non-retryable.
	SlackRestrictedActionError = "restricted_action"

	// SlackInternalError is a transient server-side error returned by Slack when
	// something went wrong on their end. Safe to retry.
	SlackInternalError = "internal_error"

	// SlackFatalError is a transient error string returned by Slack for unspecified
	// server-side failures. Despite the name, Slack treats this as retryable.
	SlackFatalError = "fatal_error"

	// SlackServiceUnavailableError is a transient error returned when the Slack API
	// is temporarily unavailable. Safe to retry.
	SlackServiceUnavailableError = "service_unavailable"

	// SlackRequestTimeoutError is a transient error returned when Slack's own
	// processing timed out handling the request. Safe to retry.
	SlackRequestTimeoutError = "request_timeout"

	// SlackRateLimitedError is returned when the client has hit a rate limit.
	// Safe to retry after a delay, using the Retry-After header if present.
	SlackRateLimitedError = "ratelimited"

	// SlackRateLimitedV2Error is returned when the client has hit a rate limit.
	// Safe to retry after a delay, using the Retry-After header if present.
	SlackRateLimitedV2Error = "rate_limited"
)

//nolint:gochecknoglobals
var transientErrorStrings = map[string]struct{}{
	SlackInternalError:           {},
	SlackFatalError:              {},
	SlackServiceUnavailableError: {},
	SlackRequestTimeoutError:     {},
}

// classifySlackError returns a metric label for a Slack API error.
func classifySlackError(err error) string {
	if err == nil {
		return "non_retryable"
	}

	var rateLimitErr *slack.RateLimitedError
	if errors.As(err, &rateLimitErr) {
		return "rate_limited"
	}

	if isTransientError(err) {
		return "transient"
	}

	return "non_retryable"
}

// isTransientError reports whether err is a known-transient Slack API error that is
// safe to retry with linear backoff. Rate limit errors are NOT transient — they have
// their own retry path via errors.As(*slack.RateLimitedError).
func isTransientError(err error) bool {
	if err == nil {
		return false
	}

	// Rate limit errors have their own retry path — exclude them from transient classification.
	var rateLimitErr *slack.RateLimitedError
	if errors.As(err, &rateLimitErr) {
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
