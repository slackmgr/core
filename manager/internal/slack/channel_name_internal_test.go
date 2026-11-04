package slack

import (
	"context"
	"errors"
	"fmt"
	"testing"

	slackapi "github.com/slack-go/slack"
	"github.com/slackmgr/core/internal"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
)

// channelInfoSlackAPI is a [slackAPI] that returns a configured channel info result.
type channelInfoSlackAPI struct {
	slackAPI // Unused methods panic

	channel *slackapi.Channel
	err     error
}

func (c *channelInfoSlackAPI) GetChannelInfo(_ context.Context, _ string) (*slackapi.Channel, error) {
	return c.channel, c.err
}

func TestClient_GetChannelName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		channel string
		api     *channelInfoSlackAPI
		want    string
	}{
		{
			name:    "found",
			channel: "C1",
			api:     &channelInfoSlackAPI{channel: &slackapi.Channel{GroupConversation: slackapi.GroupConversation{Name: "alerts"}}},
			want:    "alerts",
		},
		{name: "empty channel ID", channel: "", api: &channelInfoSlackAPI{}, want: ""},
		{name: "channel not found", channel: "C1", api: &channelInfoSlackAPI{err: errors.New(internal.SlackErrChannelNotFound)}, want: ""},
		{name: "rate limited", channel: "C1", api: &channelInfoSlackAPI{err: fmt.Errorf("%w: conversations.info", internal.ErrRateLimitBackoff)}, want: ""},
		{name: "other error", channel: "C1", api: &channelInfoSlackAPI{err: errors.New("invalid_auth")}, want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := &Client{api: tc.api, logger: &types.NoopLogger{}}

			assert.Equal(t, tc.want, c.GetChannelName(context.Background(), tc.channel))
		})
	}
}
