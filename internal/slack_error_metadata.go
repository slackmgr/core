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

	// SlackErrIsInactive is returned when a message cannot be edited or deleted
	// because the channel is frozen, inactive or archived. Non-retryable.
	SlackErrIsInactive = "is_inactive"

	// SlackErrInvalidChannelID is returned when the channel value is not a valid
	// channel ID. Non-retryable.
	SlackErrInvalidChannelID = "invalid_channel_id"

	// SlackErrRestrictedAction is returned when a workspace preference prevents the
	// bot from posting. Non-retryable.
	SlackErrRestrictedAction = "restricted_action"

	// SlackErrRestrictedActionReadOnlyChannel is returned when posting to a read-only
	// channel. Non-retryable.
	SlackErrRestrictedActionReadOnlyChannel = "restricted_action_read_only_channel"

	// SlackErrRestrictedActionThreadOnlyChannel is returned when posting a top-level
	// message to a thread-only channel. Non-retryable.
	SlackErrRestrictedActionThreadOnlyChannel = "restricted_action_thread_only_channel"

	// SlackErrEKMAccessDenied is returned when admins have suspended posting to the
	// channel. Non-retryable.
	SlackErrEKMAccessDenied = "ekm_access_denied"

	// SlackErrEditWindowClosed is returned when workspace settings no longer allow
	// the message to be edited. Non-retryable.
	SlackErrEditWindowClosed = "edit_window_closed"

	// SlackErrInvalidBlocksFormat is returned when the blocks payload is not valid
	// JSON or Block Kit syntax. Non-retryable.
	SlackErrInvalidBlocksFormat = "invalid_blocks_format"

	// SlackErrMsgBlocksTooLong is returned when the blocks payload is too long. Non-retryable.
	SlackErrMsgBlocksTooLong = "msg_blocks_too_long"

	// SlackErrMsgTooLong is returned when the message text is too long. Non-retryable.
	SlackErrMsgTooLong = "msg_too_long"

	// SlackErrTooManyAttachments is returned when a message has more than 100
	// attachments. Non-retryable.
	SlackErrTooManyAttachments = "too_many_attachments"

	// SlackErrAttachmentPayloadLimitExceeded is returned when the attachment payload
	// is too large. Non-retryable.
	SlackErrAttachmentPayloadLimitExceeded = "attachment_payload_limit_exceeded"

	// SlackErrBlockMismatch is returned when rich-text blocks are replaced with
	// non-rich-text blocks in an update. Non-retryable.
	SlackErrBlockMismatch = "block_mismatch"

	// SlackErrNoText is returned when a message has no text. Non-retryable.
	SlackErrNoText = "no_text"

	// SlackErrUpdateFailed is an internal Slack failure when updating a message.
	// Safe to retry.
	SlackErrUpdateFailed = "update_failed"

	// SlackErrNotAuthed is returned when no authentication token was provided. Non-retryable.
	SlackErrNotAuthed = "not_authed"

	// SlackErrInvalidAuth is returned when the token is invalid or the request comes
	// from a disallowed IP. Non-retryable.
	SlackErrInvalidAuth = "invalid_auth"

	// SlackErrAccountInactive is returned when the token belongs to a deleted user or
	// workspace. Non-retryable.
	SlackErrAccountInactive = "account_inactive"

	// SlackErrTokenRevoked is returned when the token has been revoked, or the app has
	// been removed. Non-retryable.
	SlackErrTokenRevoked = "token_revoked"

	// SlackErrTokenExpired is returned when the token has expired. Non-retryable.
	SlackErrTokenExpired = "token_expired"

	// SlackErrMissingScope is returned when the token lacks a scope required by the
	// method. Non-retryable.
	SlackErrMissingScope = "missing_scope"

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

// SlackErrorClass groups Slack API error codes by how callers should react to them.
type SlackErrorClass int

const (
	// SlackErrorClassOther covers unknown errors and errors without special handling.
	SlackErrorClassOther SlackErrorClass = iota

	// SlackErrorClassTransient covers server-side errors that are safe to retry.
	SlackErrorClassTransient

	// SlackErrorClassRateLimited covers rate limit errors. Safe to retry after a delay.
	SlackErrorClassRateLimited

	// SlackErrorClassAuth covers authentication and authorization errors that affect
	// every API call made with the token, not just the current request. The exception is
	// [SlackErrMissingScope], which only affects the methods that require the missing scope.
	SlackErrorClassAuth

	// SlackErrorClassChannelUnavailable covers errors where the channel does not exist
	// or no longer accepts posts from the bot (archived, inactive, read-only, restricted).
	SlackErrorClassChannelUnavailable

	// SlackErrorClassNotInChannel covers errors where the bot is not a member of the channel.
	SlackErrorClassNotInChannel

	// SlackErrorClassMessageUnavailable covers errors where an existing message is gone
	// or can no longer be edited or deleted by the bot.
	SlackErrorClassMessageUnavailable

	// SlackErrorClassInvalidContent covers errors where the message payload itself is
	// rejected (malformed or too large blocks, text or attachments).
	SlackErrorClassInvalidContent
)

// slackErrorClasses maps Slack API error codes to their [SlackErrorClass].
// Codes not listed here are classified as [SlackErrorClassOther].
//
//nolint:gochecknoglobals
var slackErrorClasses = map[string]SlackErrorClass{
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
}

// ClassifySlackError returns the [SlackErrorClass] of a Slack API error.
// The error may be a bare Slack error code, or wrap a [slack.SlackErrorResponse] or [slack.RateLimitedError]
// (e.g. after exhausted retries). A nil error, or an error that is not a known Slack error, returns [SlackErrorClassOther].
func ClassifySlackError(err error) SlackErrorClass {
	if err == nil {
		return SlackErrorClassOther
	}

	if class, ok := slackErrorClasses[SlackErrorCode(err)]; ok {
		return class
	}

	if _, ok := errors.AsType[*slack.RateLimitedError](err); ok {
		return SlackErrorClassRateLimited
	}

	return SlackErrorClassOther
}

// SlackErrorCode returns the Slack API error code of err, e.g. "channel_not_found". The error may wrap a
// [slack.SlackErrorResponse], or be a bare known Slack error code. An empty string is returned for other errors.
func SlackErrorCode(err error) string {
	if err == nil {
		return ""
	}

	if slackErr, ok := errors.AsType[slack.SlackErrorResponse](err); ok {
		return slackErr.Err
	}

	if _, ok := slackErrorClasses[err.Error()]; ok {
		return err.Error()
	}

	return ""
}

// getSlackErrorMetricLabel returns a metric label for a Slack API error.
func getSlackErrorMetricLabel(err error) string {
	if err == nil {
		return "unknown"
	}

	if _, ok := errors.AsType[*slack.RateLimitedError](err); ok {
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

// IsTransientError reports whether err should be considered transient and safe to retry.
// This includes both errors that Slack explicitly classifies as retryable, as well as
// certain client-side errors like timeouts that may indicate a temporary issue.
// Wrapped errors are unwrapped, so errors returned after exhausted retries are still reported as transient.
func IsTransientError(err error) bool {
	if err == nil {
		return false
	}

	if urlErr, ok := errors.AsType[*url.Error](err); ok && urlErr.Timeout() {
		return true
	}

	var r retryable
	if errors.As(err, &r) && r.Retryable() {
		return true
	}

	class := ClassifySlackError(err)

	return class == SlackErrorClassTransient || class == SlackErrorClassRateLimited
}

// isPostOutcomeUnknown reports whether a failed chat.postMessage call may still have posted the message.
// Only errors that prove the message was not posted return false: Slack error responses (except fatal_error
// and internal_error, where Slack says part of the operation may have succeeded), rate limits, and HTTP 4xx.
// Everything else, including HTTP 5xx, timeouts and other transport errors, is treated as unknown.
func isPostOutcomeUnknown(err error) bool {
	if err == nil {
		return false
	}

	if _, ok := errors.AsType[*slack.RateLimitedError](err); ok {
		return false
	}

	if statusErr, ok := errors.AsType[slack.StatusCodeError](err); ok {
		return statusErr.Code >= 500
	}

	code := err.Error()

	if slackErr, ok := errors.AsType[slack.SlackErrorResponse](err); ok {
		code = slackErr.Err
	} else if _, known := slackErrorClasses[code]; !known {
		return true
	}

	return code == SlackErrFatalError || code == SlackErrInternalError
}
