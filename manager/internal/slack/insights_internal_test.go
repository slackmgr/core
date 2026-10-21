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
