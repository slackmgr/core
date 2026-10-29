package slack

import (
	"errors"
	"testing"

	"github.com/slackmgr/core/internal"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
)

func TestPostFailureReason(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		internal.SlackErrChannelNotFound:   types.InsightsReasonChannelUnavailable,
		internal.SlackErrIsArchived:        types.InsightsReasonChannelUnavailable,
		internal.SlackErrIsInactive:        types.InsightsReasonChannelUnavailable,
		internal.SlackErrEKMAccessDenied:   types.InsightsReasonChannelUnavailable,
		internal.SlackErrMsgBlocksTooLong:  types.InsightsReasonInvalidBlocks,
		internal.SlackErrEditWindowClosed:  types.InsightsReasonMessageNotFound,
		internal.SlackErrInvalidBlocks:     types.InsightsReasonInvalidBlocks,
		internal.SlackErrNotInChannel:      types.InsightsReasonNotInChannel,
		internal.SlackErrMessageNotFound:   types.InsightsReasonMessageNotFound,
		internal.SlackErrCantUpdateMessage: types.InsightsReasonMessageNotFound,
		internal.SlackErrCantDeleteMessage: types.InsightsReasonMessageNotFound,
		"ratelimited":                      types.InsightsReasonAPIError,
	}

	for slackErr, want := range tests {
		assert.Equal(t, want, postFailureReason(errors.New(slackErr)), slackErr)
	}
}

func TestIsIgnorableDeleteError(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		internal.SlackErrChannelNotFound:   true,
		internal.SlackErrIsArchived:        true,
		internal.SlackErrIsInactive:        true,
		internal.SlackErrMessageNotFound:   true,
		internal.SlackErrCantUpdateMessage: true,
		internal.SlackErrCantDeleteMessage: true,
		internal.SlackErrEditWindowClosed:  true,
		internal.SlackErrNotInChannel:      false,
		internal.SlackErrInvalidBlocks:     false,
		internal.SlackErrFatalError:        false,
		"unknown":                          false,
	}

	for slackErr, want := range tests {
		assert.Equal(t, want, isIgnorableDeleteError(errors.New(slackErr)), slackErr)
	}
}
