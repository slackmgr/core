package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gocache_store "github.com/eko/gocache/store/go_cache/v4"
	gocache "github.com/patrickmn/go-cache"
	"github.com/slack-go/slack"
	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/internal"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRateLimitedSlackAPIClient returns a connected Slack API client, wired to gate, against a fake Slack API that
// answers auth.test and returns 429 for every other method. Rate limit errors are not retried.
func newRateLimitedSlackAPIClient(t *testing.T, gate RateLimitGate) *internal.SlackAPIClient {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth.test" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user_id": "UBOT", "user": "bot", "bot_id": "BBOT"})

			return
		}

		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	cfg := config.NewDefaultSlackClientConfig()
	cfg.BotToken = "xoxb-test"
	cfg.AppToken = "xapp-test"
	cfg.MaxAttemptsForRateLimitError = 1

	store := gocache_store.NewGoCache(gocache.New(5*time.Minute, time.Minute))

	client := internal.NewSlackAPIClient(store, "test:", &types.NoopLogger{}, &types.NoopMetrics{}, cfg,
		internal.WithRateLimitGate(gate), internal.WithAPIURL(server.URL+"/"))

	_, err := client.Connect(context.Background())
	require.NoError(t, err)

	return client
}

// TestRateLimitGroups_ChannelProcessing verifies that a 429 on a lookup method does not block channel processing,
// while a 429 on a write method still does.
func TestRateLimitGroups_ChannelProcessing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		call        func(ctx context.Context, client *internal.SlackAPIClient) error
		wantBlocked bool
	}{
		{
			name: "conversations.info",
			call: func(ctx context.Context, client *internal.SlackAPIClient) error {
				_, err := client.GetChannelInfo(ctx, "C1")
				return err
			},
		},
		{
			name: "conversations.members",
			call: func(ctx context.Context, client *internal.SlackAPIClient) error {
				_, err := client.GetUserIDsInChannel(ctx, "C1")
				return err
			},
		},
		{
			name: "users.info",
			call: func(ctx context.Context, client *internal.SlackAPIClient) error {
				_, err := client.GetUserInfo(ctx, "U1")
				return err
			},
		},
		{
			name: "usergroups.users.list",
			call: func(ctx context.Context, client *internal.SlackAPIClient) error {
				_, err := client.ListUserGroupMembers(ctx, "S1")
				return err
			},
		},
		{
			name: "conversations.history",
			call: func(ctx context.Context, client *internal.SlackAPIClient) error {
				_, err := client.ListBotMessages(ctx, "C1", time.Now().Add(-time.Hour), "event")
				return err
			},
		},
		{
			name: "chat.postMessage",
			call: func(ctx context.Context, client *internal.SlackAPIClient) error {
				_, err := client.ChatPostMessage(ctx, "C1", slack.MsgOptionText("x", false))
				return err
			},
			wantBlocked: true,
		},
		{
			name: "chat.update",
			call: func(ctx context.Context, client *internal.SlackAPIClient) error {
				_, err := client.ChatUpdateMessage(ctx, "C1", slack.MsgOptionUpdate("1.1"), slack.MsgOptionText("x", false))
				return err
			},
			wantBlocked: true,
		},
		{
			name: "chat.delete",
			call: func(ctx context.Context, client *internal.SlackAPIClient) error {
				return client.ChatDeleteMessage(ctx, "C1", "1.1")
			},
			wantBlocked: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gate := NewLocalRateLimitGate(&mockLogger{}, 0)
			client := newRateLimitedSlackAPIClient(t, gate)
			cm := newTestChannelManager(t, "C1", nil, &stubSlackClient{}, &NoopChannelLocker{})
			cm.gate = gate

			err := tc.call(context.Background(), client)
			require.Error(t, err)
			assert.True(t, internal.IsRateLimitError(err))

			blocked, err := gate.IsBlocked(context.Background())
			require.NoError(t, err)
			assert.Equal(t, tc.wantBlocked, blocked)

			// Channel processing waits for the gate before each alert, command and issue processing cycle.
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			start := time.Now()
			err = cm.waitForGate(ctx)

			if tc.wantBlocked {
				require.ErrorIs(t, err, context.DeadlineExceeded, "a write 429 blocks channel processing")
			} else {
				require.NoError(t, err, "a lookup 429 does not block channel processing")
				assert.Less(t, time.Since(start), 100*time.Millisecond)
			}
		})
	}
}
