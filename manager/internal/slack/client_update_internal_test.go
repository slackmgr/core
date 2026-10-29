package slack

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	slackapi "github.com/slack-go/slack"
	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/internal"
	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const updateTestChannel = "C0123456789"

// fakeSlackAPI is a [slackAPI] that records post, update and delete calls, and returns errors from the configured functions.
// Posts are identified by the fallback text (the issue name, see newUpdateTestIssue), updates and deletes by the message timestamp.
type fakeSlackAPI struct {
	slackAPI // Unused methods panic

	postErr   func(name string) error
	updateErr func(ts string) error
	deleteErr func(ts string) error

	mu      sync.Mutex
	posts   []string
	updates []string
	deletes []string
}

func (f *fakeSlackAPI) ChatPostMessage(_ context.Context, channelID string, options ...slackapi.MsgOption) (string, error) {
	_, values, err := slackapi.UnsafeApplyMsgOptions("", channelID, "", options...)
	if err != nil {
		return "", err
	}

	name := values.Get("text")

	f.mu.Lock()
	f.posts = append(f.posts, name)
	f.mu.Unlock()

	if f.postErr != nil {
		if err := f.postErr(name); err != nil {
			return "", err
		}
	}

	return "new-" + name, nil
}

func (f *fakeSlackAPI) ChatUpdateMessage(_ context.Context, channelID string, options ...slackapi.MsgOption) (string, error) {
	_, values, err := slackapi.UnsafeApplyMsgOptions("", channelID, "", options...)
	if err != nil {
		return "", err
	}

	ts := values.Get("ts")

	f.mu.Lock()
	f.updates = append(f.updates, ts)
	f.mu.Unlock()

	if f.updateErr != nil {
		if err := f.updateErr(ts); err != nil {
			return "", err
		}
	}

	return ts, nil
}

func (f *fakeSlackAPI) ChatDeleteMessage(ctx context.Context, _ string, ts string) error {
	f.mu.Lock()
	f.deletes = append(f.deletes, ts)
	f.mu.Unlock()

	if f.deleteErr != nil {
		return f.deleteErr(ts)
	}

	return ctx.Err()
}

func (f *fakeSlackAPI) MessageHasReplies(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}

func newUpdateTestClient(t *testing.T, api *fakeSlackAPI) *Client {
	t.Helper()

	cfg := config.NewDefaultManagerConfig()
	cfg.EncryptionKey = "abcdefghijklmnopqrstuvwxyz123456"
	cfg.SlackClient.AppToken = "xapp-test"
	cfg.SlackClient.BotToken = "xoxb-test"
	require.NoError(t, cfg.Validate())

	settings := &config.ManagerSettings{}
	require.NoError(t, settings.InitAndValidate())

	c := New(nil, nil, &types.NoopLogger{}, &types.NoopMetrics{}, cfg, models.NewManagerSettingsWrapper(settings))
	c.api = api

	return c
}

// newUpdateTestIssue returns an open issue that needs a Slack post. The name is used as fallback text, to identify posts.
// A non-empty slackPostID gives the issue an existing post.
func newUpdateTestIssue(name, slackPostID string) *models.Issue {
	alert := &models.Alert{Alert: types.Alert{
		SlackChannelID:       updateTestChannel,
		Header:               "Header " + name,
		FallbackText:         name,
		Severity:             types.AlertError,
		CorrelationID:        "corr-" + name,
		Timestamp:            time.Now(),
		IssueFollowUpEnabled: true,
		AutoResolveSeconds:   3600,
	}}

	issue := models.NewIssue(alert, &types.NoopLogger{})
	issue.SlackPostNeedsUpdate = true

	if slackPostID != "" {
		issue.SlackPostID = slackPostID
		issue.SlackPostHeader = alert.Header
	}

	return issue
}

func TestUpdate_FailedDeleteDoesNotBlockOtherIssues(t *testing.T) {
	t.Parallel()

	api := &fakeSlackAPI{
		deleteErr: func(ts string) error {
			if ts == "ts-a" {
				return errors.New("some_unknown_error")
			}
			return nil
		},
	}
	c := newUpdateTestClient(t, api)

	failedDelete := newUpdateTestIssue("a", "ts-a")
	failedDelete.SlackPostNeedsDelete = true

	okDelete := newUpdateTestIssue("b", "ts-b")
	okDelete.SlackPostNeedsDelete = true

	newIssue := newUpdateTestIssue("c", "")

	err := c.Update(context.Background(), updateTestChannel, []*models.Issue{failedDelete, okDelete, newIssue})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "some_unknown_error")

	assert.ElementsMatch(t, []string{"ts-a", "ts-b"}, api.deletes)

	// The issue with the failed delete keeps its post, which is updated in place rather than duplicated.
	assert.Equal(t, []string{"ts-a"}, api.updates)
	assert.Equal(t, "ts-a", failedDelete.SlackPostID)
	assert.True(t, failedDelete.SlackPostNeedsDelete, "delete is retried in the next cycle")

	// The successfully deleted issue is re-posted, and the new issue is created.
	assert.ElementsMatch(t, []string{"b", "c"}, api.posts)
	assert.Equal(t, "new-b", okDelete.SlackPostID)
	assert.Equal(t, "new-c", newIssue.SlackPostID)
}

func TestUpdate_AbortingDeleteErrorSkipsCreateAndUpdate(t *testing.T) {
	t.Parallel()

	api := &fakeSlackAPI{
		deleteErr: func(_ string) error {
			return slackapi.SlackErrorResponse{Err: internal.SlackErrInvalidAuth}
		},
	}
	c := newUpdateTestClient(t, api)

	toDelete := newUpdateTestIssue("a", "ts-a")
	toDelete.SlackPostNeedsDelete = true

	newIssue := newUpdateTestIssue("b", "")

	err := c.Update(context.Background(), updateTestChannel, []*models.Issue{toDelete, newIssue})

	require.Error(t, err)
	assert.Contains(t, err.Error(), internal.SlackErrInvalidAuth)
	assert.Empty(t, api.posts)
	assert.Empty(t, api.updates)
	assert.Equal(t, "ts-a", toDelete.SlackPostID)
}

func TestUpdate_FailedCreateDoesNotBlockOtherIssues(t *testing.T) {
	t.Parallel()

	api := &fakeSlackAPI{
		postErr: func(header string) error {
			if header == "b" {
				return errors.New("some_unknown_error")
			}
			return nil
		},
		updateErr: func(ts string) error {
			if ts == "ts-d" {
				return errors.New("another_unknown_error")
			}
			return nil
		},
	}
	c := newUpdateTestClient(t, api)

	issueA := newUpdateTestIssue("a", "")
	issueB := newUpdateTestIssue("b", "")
	issueC := newUpdateTestIssue("c", "")
	issueD := newUpdateTestIssue("d", "ts-d")
	issueE := newUpdateTestIssue("e", "ts-e")

	err := c.Update(context.Background(), updateTestChannel, []*models.Issue{issueA, issueB, issueC, issueD, issueE})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "some_unknown_error")
	assert.Contains(t, err.Error(), "another_unknown_error")

	assert.ElementsMatch(t, []string{"a", "b", "c"}, api.posts)
	assert.ElementsMatch(t, []string{"ts-d", "ts-e"}, api.updates)

	assert.Equal(t, "new-a", issueA.SlackPostID)
	assert.Empty(t, issueB.SlackPostID)
	assert.True(t, issueB.SlackPostNeedsUpdate, "create is retried in the next cycle")
	assert.Equal(t, "new-c", issueC.SlackPostID)
	assert.True(t, issueD.SlackPostNeedsUpdate, "update is retried in the next cycle")
	assert.False(t, issueE.SlackPostNeedsUpdate)
}

func TestUpdate_AbortingCreateErrorSkipsRemainingIssues(t *testing.T) {
	t.Parallel()

	tests := map[string]error{
		"rate limit": fmt.Errorf("failed to call Slack API chat.postMessage: rate limit error: %w", &slackapi.RateLimitedError{}),
		"transient":  fmt.Errorf("failed to call Slack API chat.postMessage: transient error: %w", slackapi.SlackErrorResponse{Err: internal.SlackErrServiceUnavailable}),
		"auth":       slackapi.SlackErrorResponse{Err: internal.SlackErrTokenRevoked},
	}

	for name, abortErr := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			api := &fakeSlackAPI{
				postErr: func(_ string) error { return abortErr },
			}
			c := newUpdateTestClient(t, api)

			issues := []*models.Issue{
				newUpdateTestIssue("a", ""),
				newUpdateTestIssue("b", ""),
				newUpdateTestIssue("c", ""),
			}

			err := c.Update(context.Background(), updateTestChannel, issues)

			require.Error(t, err)
			assert.Contains(t, err.Error(), abortErr.Error())
			assert.Len(t, api.posts, 1, "remaining issues are skipped")
		})
	}
}

func TestUpdate_NoErrors(t *testing.T) {
	t.Parallel()

	api := &fakeSlackAPI{}
	c := newUpdateTestClient(t, api)

	toDelete := newUpdateTestIssue("a", "ts-a")
	toDelete.SlackPostNeedsDelete = true

	err := c.Update(context.Background(), updateTestChannel, []*models.Issue{toDelete, newUpdateTestIssue("b", "ts-b")})

	require.NoError(t, err)
	assert.Equal(t, []string{"ts-a"}, api.deletes)
	assert.Equal(t, []string{"a"}, api.posts)
	assert.Equal(t, []string{"ts-b"}, api.updates)
}

func TestUpdateSingleIssue_SlackErrorHandling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		existingPostID  string
		slackErr        string
		wantErr         bool
		wantPostID      string
		wantNeedsUpdate bool
	}{
		{name: "create: channel unavailable", slackErr: internal.SlackErrIsArchived, wantPostID: PostIDInvalidChannel},
		{name: "create: invalid content", slackErr: internal.SlackErrMsgBlocksTooLong, wantPostID: ""},
		{name: "create: not in channel", slackErr: internal.SlackErrNotInChannel, wantPostID: "", wantNeedsUpdate: true},
		{name: "create: other error", slackErr: "some_unknown_error", wantErr: true, wantPostID: "", wantNeedsUpdate: true},
		{name: "update: channel inactive", existingPostID: "ts-a", slackErr: internal.SlackErrIsInactive, wantPostID: PostIDInvalidChannel},
		{name: "update: edit window closed", existingPostID: "ts-a", slackErr: internal.SlackErrEditWindowClosed, wantPostID: "", wantNeedsUpdate: true},
		{name: "update: invalid content", existingPostID: "ts-a", slackErr: internal.SlackErrInvalidBlocksFormat, wantPostID: ""},
		{name: "update: not in channel", existingPostID: "ts-a", slackErr: internal.SlackErrNotInChannel, wantPostID: "", wantNeedsUpdate: true},
		{name: "update: other error", existingPostID: "ts-a", slackErr: "some_unknown_error", wantErr: true, wantPostID: "ts-a", wantNeedsUpdate: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			slackErr := slackapi.SlackErrorResponse{Err: tc.slackErr}
			api := &fakeSlackAPI{
				postErr:   func(_ string) error { return slackErr },
				updateErr: func(_ string) error { return slackErr },
			}
			c := newUpdateTestClient(t, api)

			issue := newUpdateTestIssue("a", tc.existingPostID)

			err := c.UpdateSingleIssue(context.Background(), issue, "test")

			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tc.wantPostID, issue.SlackPostID)
			assert.Equal(t, tc.wantNeedsUpdate, issue.SlackPostNeedsUpdate)
		})
	}
}

func TestDelete_SlackErrorHandling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		slackErr string
		wantErr  bool
	}{
		{name: "channel unavailable", slackErr: internal.SlackErrIsInactive},
		{name: "message unavailable", slackErr: internal.SlackErrCantDeleteMessage},
		{name: "other error", slackErr: "some_unknown_error", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			api := &fakeSlackAPI{
				deleteErr: func(_ string) error { return slackapi.SlackErrorResponse{Err: tc.slackErr} },
			}
			c := newUpdateTestClient(t, api)

			issue := newUpdateTestIssue("a", "ts-a")
			issue.SlackPostNeedsDelete = true

			err := c.Delete(context.Background(), issue, "test", false)

			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, "ts-a", issue.SlackPostID)
				assert.True(t, issue.SlackPostNeedsDelete)
			} else {
				require.NoError(t, err)
				assert.Empty(t, issue.SlackPostID)
				assert.False(t, issue.SlackPostNeedsDelete)
			}
		})
	}
}

func TestAbortsChannelUpdate(t *testing.T) {
	t.Parallel()

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	timeoutURLErr := &url.Error{Op: "Post", URL: "https://slack.com/api/test", Err: context.DeadlineExceeded}

	tests := []struct {
		name string
		ctx  context.Context //nolint:containedctx // Test table
		err  error
		want bool
	}{
		{name: "cancelled context", ctx: cancelledCtx, err: errors.New("some_unknown_error"), want: true},
		{name: "not connected", ctx: context.Background(), err: fmt.Errorf("wrapped: %w", internal.ErrNotConnected), want: true},
		{name: "rate limit", ctx: context.Background(), err: fmt.Errorf("wrapped: %w", &slackapi.RateLimitedError{}), want: true},
		{name: "transient slack error", ctx: context.Background(), err: fmt.Errorf("wrapped: %w", slackapi.SlackErrorResponse{Err: internal.SlackErrInternalError}), want: true},
		{name: "http timeout", ctx: context.Background(), err: fmt.Errorf("wrapped: %w", timeoutURLErr), want: true},
		{name: "auth", ctx: context.Background(), err: slackapi.SlackErrorResponse{Err: internal.SlackErrInvalidAuth}, want: true},
		{name: "channel unavailable", ctx: context.Background(), err: slackapi.SlackErrorResponse{Err: internal.SlackErrChannelNotFound}, want: false},
		{name: "unknown slack error", ctx: context.Background(), err: slackapi.SlackErrorResponse{Err: "some_unknown_error"}, want: false},
		{name: "plain error", ctx: context.Background(), err: errors.New("failed"), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, abortsChannelUpdate(tc.ctx, tc.err))
		})
	}
}
