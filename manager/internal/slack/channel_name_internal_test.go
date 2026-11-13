package slack

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
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

// channelNameLogger is a [types.Logger] that records the levels of logged messages, also through WithField.
type channelNameLogger struct {
	types.NoopLogger

	mu     sync.Mutex
	levels []string
}

func (l *channelNameLogger) WithField(_ string, _ any) types.Logger   { return l }
func (l *channelNameLogger) WithFields(_ map[string]any) types.Logger { return l }
func (l *channelNameLogger) Info(_ string)                            { l.record("info") }
func (l *channelNameLogger) Infof(_ string, _ ...any)                 { l.record("info") }
func (l *channelNameLogger) Error(_ string)                           { l.record("error") }
func (l *channelNameLogger) Errorf(_ string, _ ...any)                { l.record("error") }

func (l *channelNameLogger) record(level string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.levels = append(l.levels, level)
}

func TestClient_GetChannelName(t *testing.T) {
	t.Parallel()

	timeoutErr := &url.Error{Op: "Post", URL: "https://slack.com/api/conversations.info", Err: context.DeadlineExceeded}

	tests := []struct {
		name       string
		channel    string
		api        *channelInfoSlackAPI
		want       string
		wantLevels []string
	}{
		{
			name:    "found",
			channel: "C1",
			api:     &channelInfoSlackAPI{channel: &slackapi.Channel{GroupConversation: slackapi.GroupConversation{Name: "alerts"}}},
			want:    "alerts",
		},
		{name: "empty channel ID", channel: "", api: &channelInfoSlackAPI{}, want: ""},
		{name: "channel not found", channel: "C1", api: &channelInfoSlackAPI{err: errors.New(internal.SlackErrChannelNotFound)}, want: ""},
		{name: "rate limited", channel: "C1", api: &channelInfoSlackAPI{err: fmt.Errorf("%w: conversations.info", internal.ErrRateLimitBackoff)}, want: "", wantLevels: []string{"info"}},
		{name: "context deadline exceeded", channel: "C1", api: &channelInfoSlackAPI{err: timeoutErr}, want: "", wantLevels: []string{"info"}},
		{name: "other error", channel: "C1", api: &channelInfoSlackAPI{err: errors.New("invalid_auth")}, want: "", wantLevels: []string{"error"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger := &channelNameLogger{}
			c := &Client{api: tc.api, logger: logger}

			assert.Equal(t, tc.want, c.GetChannelName(context.Background(), tc.channel))
			assert.Equal(t, tc.wantLevels, logger.levels)
		})
	}
}
