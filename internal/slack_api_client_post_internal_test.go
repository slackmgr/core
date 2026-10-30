package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slackmgr/core/config"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

const (
	testBotUserID = "UBOT"
	testBotID     = "BBOT"
)

// newTestSlackAPIClient returns a connected client that sends requests to the given handler.
func newTestSlackAPIClient(t *testing.T, handler http.HandlerFunc) *SlackAPIClient {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cfg := config.NewDefaultSlackClientConfig()

	return &SlackAPIClient{
		api:       slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/")),
		logger:    &types.NoopLogger{},
		metrics:   &types.NoopMetrics{},
		cfg:       cfg,
		sem:       semaphore.NewWeighted(int64(cfg.Concurrency)),
		connected: true,
		botUserID: testBotUserID,
		botID:     testBotID,
	}
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func TestChatPostMessage_OutcomeUnknownIsNotRetried(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		respond     func(w http.ResponseWriter)
		wantUnknown bool
	}{
		{
			name: "fatal_error",
			respond: func(w http.ResponseWriter) {
				writeJSON(w, map[string]any{"ok": false, "error": SlackErrFatalError})
			},
			wantUnknown: true,
		},
		{
			name: "internal_error",
			respond: func(w http.ResponseWriter) {
				writeJSON(w, map[string]any{"ok": false, "error": SlackErrInternalError})
			},
			wantUnknown: true,
		},
		{
			name:        "http 500",
			respond:     func(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) },
			wantUnknown: true,
		},
		{
			name: "channel_not_found",
			respond: func(w http.ResponseWriter) {
				writeJSON(w, map[string]any{"ok": false, "error": SlackErrChannelNotFound})
			},
			wantUnknown: false,
		},
		{
			name:        "http 400",
			respond:     func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadRequest) },
			wantUnknown: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32

			c := newTestSlackAPIClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				tc.respond(w)
			})

			_, err := c.ChatPostMessage(context.Background(), "C1", slack.MsgOptionText("hello", false))

			require.Error(t, err)
			assert.Equal(t, tc.wantUnknown, errors.Is(err, ErrPostOutcomeUnknown))
			assert.Equal(t, int32(1), calls.Load(), "not retried")
		})
	}
}

func TestChatPostMessage_Success(t *testing.T) {
	t.Parallel()

	c := newTestSlackAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/chat.postMessage", r.URL.Path)
		writeJSON(w, map[string]any{"ok": true, "channel": "C1", "ts": "1700000000.000100"})
	})

	ts, err := c.ChatPostMessage(context.Background(), "C1", slack.MsgOptionText("hello", false))

	require.NoError(t, err)
	assert.Equal(t, "1700000000.000100", ts)
}

func TestListBotMessages(t *testing.T) {
	t.Parallel()

	oldest := time.Unix(1700000000, 500000000)

	var calls atomic.Int32

	c := newTestSlackAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/conversations.history", r.URL.Path)
		assert.NoError(t, r.ParseForm())
		assert.Equal(t, "C1", r.Form.Get("channel"))
		assert.Equal(t, "1700000000.500000", r.Form.Get("oldest"))
		assert.Equal(t, "1", r.Form.Get("include_all_metadata"))

		meta := map[string]any{"event_type": "slackmgr_issue", "event_payload": map[string]any{"issue_id": "i1"}}

		if calls.Add(1) == 1 {
			assert.Empty(t, r.Form.Get("cursor"))
			writeJSON(w, map[string]any{
				"ok": true,
				"messages": []map[string]any{
					// Real shape of a message posted with as_user=false: bot_message with bot ID, but no user ID
					{"ts": "1700000002.000000", "subtype": "bot_message", "bot_id": testBotID, "username": "Custom", "metadata": meta},
					{"ts": "1700000002.000001", "subtype": "bot_message", "bot_id": "BOTHER", "metadata": meta},
					{"ts": "1700000002.000002", "subtype": "bot_message", "bot_id": testBotID},
					{"ts": "1700000002.000003", "subtype": "bot_message", "bot_id": testBotID, "metadata": map[string]any{"event_type": "other"}},
					{"ts": "1700000002.000004", "user": testBotUserID, "metadata": meta},
				},
				"has_more":          true,
				"response_metadata": map[string]any{"next_cursor": "page2"},
			})

			return
		}

		assert.Equal(t, "page2", r.Form.Get("cursor"))
		writeJSON(w, map[string]any{
			"ok":       true,
			"messages": []map[string]any{{"ts": "1700000001.000000", "bot_id": testBotID, "metadata": meta}},
			"has_more": false,
		})
	})

	msgs, err := c.ListBotMessages(context.Background(), "C1", oldest, "slackmgr_issue")

	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, "1700000002.000000", msgs[0].Timestamp)
	assert.Equal(t, "1700000001.000000", msgs[1].Timestamp)
	assert.Equal(t, "i1", msgs[0].Metadata.EventPayload["issue_id"])
}

func TestListBotMessages_TooManyPages(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	c := newTestSlackAPIClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeJSON(w, map[string]any{
			"ok":                true,
			"messages":          []map[string]any{},
			"has_more":          true,
			"response_metadata": map[string]any{"next_cursor": "more"},
		})
	})

	_, err := c.ListBotMessages(context.Background(), "C1", time.Now(), "slackmgr_issue")

	require.ErrorIs(t, err, ErrTooManyMessages)
	assert.Equal(t, int32(maxBotMessagesPages), calls.Load())
}

func TestListBotMessages_Validation(t *testing.T) {
	t.Parallel()

	_, err := (&SlackAPIClient{}).ListBotMessages(context.Background(), "C1", time.Now(), "x")
	require.ErrorIs(t, err, ErrNotConnected)

	_, err = (&SlackAPIClient{connected: true}).ListBotMessages(context.Background(), "", time.Now(), "x")
	require.Error(t, err)

	_, err = (&SlackAPIClient{connected: true}).ListBotMessages(context.Background(), "C1", time.Now(), "x")
	require.ErrorContains(t, err, "bot ID is unknown")
}

func TestIsPostOutcomeUnknown(t *testing.T) {
	t.Parallel()

	timeoutURLErr := &url.Error{Op: "Post", URL: "https://slack.com/api/chat.postMessage", Err: context.DeadlineExceeded}
	resetURLErr := &url.Error{Op: "Post", URL: "https://slack.com/api/chat.postMessage", Err: errors.New("connection reset by peer")}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "fatal_error response", err: slack.SlackErrorResponse{Err: SlackErrFatalError}, want: true},
		{name: "internal_error response", err: slack.SlackErrorResponse{Err: SlackErrInternalError}, want: true},
		{name: "fatal_error string", err: errors.New(SlackErrFatalError), want: true},
		{name: "service_unavailable response", err: slack.SlackErrorResponse{Err: SlackErrServiceUnavailable}, want: false},
		{name: "channel_not_found response", err: slack.SlackErrorResponse{Err: SlackErrChannelNotFound}, want: false},
		{name: "unknown code response", err: slack.SlackErrorResponse{Err: "some_unknown_error"}, want: false},
		{name: "known code string", err: errors.New(SlackErrIsArchived), want: false},
		{name: "rate limited", err: &slack.RateLimitedError{RetryAfter: time.Second}, want: false},
		{name: "http 503", err: slack.StatusCodeError{Code: 503, Status: "503"}, want: true},
		{name: "http 404", err: slack.StatusCodeError{Code: 404, Status: "404"}, want: false},
		{name: "timeout", err: timeoutURLErr, want: true},
		{name: "connection reset", err: resetURLErr, want: true},
		{name: "context cancelled", err: context.Canceled, want: true},
		{name: "wrapped fatal_error", err: fmt.Errorf("x: %w", slack.SlackErrorResponse{Err: SlackErrFatalError}), want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isPostOutcomeUnknown(tc.err))
		})
	}
}
