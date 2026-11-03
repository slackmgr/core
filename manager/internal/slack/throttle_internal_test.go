package slack

import (
	"context"
	"testing"
	"time"

	slackapi "github.com/slack-go/slack"
	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPostedThrottleTestIssue returns an issue with a Slack post created by the client, i.e. with a fingerprint that
// matches the current issue state. The post times are set to the given ages.
func newPostedThrottleTestIssue(t *testing.T, c *Client, api *fakeSlackAPI, postAge, contentAge, prevContentAge time.Duration) *models.Issue {
	t.Helper()

	issue := newUpdateTestIssue("a", "")
	issue.LastAlert.Text = "some text"

	require.NoError(t, c.UpdateSingleIssue(context.Background(), issue, "test"))
	require.Equal(t, []string{"a"}, api.posts)
	require.False(t, issue.SlackPostFingerprint.IsZero())

	now := time.Now()
	issue.SlackPostUpdated = now.Add(-postAge)
	issue.SlackPostContentUpdated = now.Add(-contentAge)

	if prevContentAge > 0 {
		issue.SlackPostPrevContentUpdated = now.Add(-prevContentAge)
	}

	return issue
}

func TestUpdateSingleIssue_Throttling(t *testing.T) {
	t.Parallel()

	newAlert := func(issue *models.Issue) {
		issue.AlertCount++
		issue.LastAlertReceived = time.Now()
	}

	tests := []struct {
		name           string
		postAge        time.Duration
		contentAge     time.Duration
		prevContentAge time.Duration // Zero means no previous content change
		mutate         func(issue *models.Issue)
		wantUpdate     bool
	}{
		// Footer-only changes: throttled for the max throttle duration (60s), since the last post update
		{name: "footer: recent post update", postAge: 5 * time.Second, contentAge: time.Hour, mutate: newAlert, wantUpdate: false},
		{name: "footer: window passed", postAge: 61 * time.Second, contentAge: time.Hour, mutate: newAlert, wantUpdate: true},

		// Content changes: throttled by the content window, since the last content change
		{
			name: "content: first change after a quiet period", postAge: 2 * time.Second, contentAge: time.Hour,
			mutate: func(issue *models.Issue) { newAlert(issue); issue.LastAlert.Text = "new text" }, wantUpdate: true,
		},
		{
			name: "content: recent change", postAge: 5 * time.Second, contentAge: 5 * time.Second,
			mutate: func(issue *models.Issue) { newAlert(issue); issue.LastAlert.Text = "new text" }, wantUpdate: false,
		},
		{
			name: "content: min window passed", postAge: 11 * time.Second, contentAge: 11 * time.Second,
			mutate: func(issue *models.Issue) { newAlert(issue); issue.LastAlert.Text = "new text" }, wantUpdate: true,
		},
		{
			name: "content: backoff while the content keeps changing", postAge: 30 * time.Second, contentAge: 30 * time.Second, prevContentAge: 50 * time.Second,
			mutate: func(issue *models.Issue) { newAlert(issue); issue.LastAlert.Text = "new text" }, wantUpdate: false,
		},
		{
			name: "content: backoff window passed", postAge: 41 * time.Second, contentAge: 41 * time.Second, prevContentAge: 61 * time.Second,
			mutate: func(issue *models.Issue) { newAlert(issue); issue.LastAlert.Text = "new text" }, wantUpdate: true,
		},
		{
			name: "content: backoff capped to the max window", postAge: 61 * time.Second, contentAge: 61 * time.Second, prevContentAge: 111 * time.Second,
			mutate: func(issue *models.Issue) { newAlert(issue); issue.LastAlert.Text = "new text" }, wantUpdate: true,
		},
		{
			name: "content: backoff reset after stable content", postAge: 11 * time.Second, contentAge: 11 * time.Second, prevContentAge: 5 * time.Minute,
			mutate: func(issue *models.Issue) { newAlert(issue); issue.LastAlert.Text = "new text" }, wantUpdate: true,
		},
		{
			name: "content: field changed", postAge: 5 * time.Second, contentAge: 5 * time.Second,
			mutate: func(issue *models.Issue) {
				issue.LastAlert.Fields = []*types.Field{{Title: "a", Value: "b"}}
			},
			wantUpdate: false,
		},

		// Significant changes: never throttled
		{
			name: "significant: severity changed", postAge: time.Second, contentAge: time.Second,
			mutate: func(issue *models.Issue) { newAlert(issue); issue.LastAlert.Severity = types.AlertWarning }, wantUpdate: true,
		},
		{
			name: "significant: header changed", postAge: time.Second, contentAge: time.Second,
			mutate: func(issue *models.Issue) { newAlert(issue); issue.LastAlert.Header = "new header" }, wantUpdate: true,
		},
		{
			name: "significant: action changed", postAge: time.Second, contentAge: time.Second,
			mutate: func(issue *models.Issue) { newAlert(issue); issue.LastAlert.Severity = types.AlertResolved }, wantUpdate: true,
		},
		{
			name: "significant: webhook button added", postAge: time.Second, contentAge: time.Second,
			mutate: func(issue *models.Issue) {
				issue.LastAlert.Webhooks = []*types.Webhook{{ID: "restart", ButtonText: "Restart"}}
			},
			wantUpdate: true,
		},
		{
			name: "significant: investigated by user", postAge: time.Second, contentAge: time.Second,
			mutate: func(issue *models.Issue) {
				issue.IsEmojiInvestigated = true
				issue.InvestigatedByUser = "user"
			},
			wantUpdate: true,
		},
		{
			name: "significant: username changed", postAge: time.Second, contentAge: time.Second,
			mutate: func(issue *models.Issue) { issue.LastAlert.Username = "other bot" }, wantUpdate: true,
		},
		{
			name: "significant: active mentions", postAge: time.Second, contentAge: time.Second,
			mutate: func(issue *models.Issue) { newAlert(issue); issue.LastAlert.Text = "<!here> some text" }, wantUpdate: true,
		},
		{
			name: "significant: unknown fingerprint", postAge: time.Second, contentAge: time.Second,
			mutate: func(issue *models.Issue) { newAlert(issue); issue.SlackPostFingerprint = models.SlackPostFingerprint{} }, wantUpdate: true,
		},
		{
			name: "significant: cleared post-click state", postAge: time.Second, contentAge: time.Second,
			mutate: func(issue *models.Issue) {
				issue.PendingPostClick = &models.PostClickState{WebhookID: "restart"}
				issue.ClearPostClickState()
				newAlert(issue)
			},
			wantUpdate: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			api := &fakeSlackAPI{}
			c := newUpdateTestClient(t, api)
			issue := newPostedThrottleTestIssue(t, c, api, tc.postAge, tc.contentAge, tc.prevContentAge)
			postUpdated := issue.SlackPostUpdated

			tc.mutate(issue)
			issue.SlackPostNeedsUpdate = true

			require.NoError(t, c.UpdateSingleIssue(context.Background(), issue, "test"))

			if tc.wantUpdate {
				assert.Equal(t, []string{"new-a"}, api.updates)
				assert.False(t, issue.SlackPostNeedsUpdate)
				assert.True(t, issue.SlackPostUpdated.After(postUpdated))
			} else {
				assert.Empty(t, api.updates)
				assert.True(t, issue.SlackPostNeedsUpdate, "a throttled update must be kept for a later attempt")
				assert.Equal(t, postUpdated, issue.SlackPostUpdated)
			}
		})
	}
}

func TestUpdate_ThrottledUpdateIsPostedLater(t *testing.T) {
	t.Parallel()

	api := &fakeSlackAPI{}
	c := newUpdateTestClient(t, api)
	issue := newPostedThrottleTestIssue(t, c, api, 5*time.Second, 5*time.Second, 0)

	issue.LastAlert.Text = "new text"
	issue.SlackPostNeedsUpdate = true

	// The content changed 5 seconds ago, so the channel update throttles it
	require.NoError(t, c.Update(context.Background(), updateTestChannel, []*models.Issue{issue}))
	assert.Empty(t, api.updates)
	assert.True(t, issue.SlackPostNeedsUpdate)

	// Once the min window has passed, the next channel update posts it
	issue.SlackPostUpdated = time.Now().Add(-11 * time.Second)
	issue.SlackPostContentUpdated = issue.SlackPostUpdated

	require.NoError(t, c.Update(context.Background(), updateTestChannel, []*models.Issue{issue}))
	assert.Equal(t, []string{"new-a"}, api.updates)
	assert.False(t, issue.SlackPostNeedsUpdate)
}

func TestUpdateSingleIssue_NewPostIsNeverThrottled(t *testing.T) {
	t.Parallel()

	api := &fakeSlackAPI{}
	c := newUpdateTestClient(t, api)

	issue := newUpdateTestIssue("a", "")
	issue.AlertCount = 1000

	require.NoError(t, c.UpdateSingleIssue(context.Background(), issue, "test"))
	assert.Equal(t, []string{"a"}, api.posts)
}

func TestContentThrottleWindow(t *testing.T) {
	t.Parallel()

	minWindow := 10 * time.Second
	maxWindow := time.Minute
	now := time.Now()

	tests := []struct {
		name      string
		gap       time.Duration // Gap between the last two content changes; zero means no previous change
		minWindow time.Duration
		want      time.Duration
	}{
		{name: "no previous change", want: minWindow},
		{name: "short gap uses the min window", gap: 3 * time.Second, want: minWindow},
		{name: "window is twice the gap", gap: 20 * time.Second, want: 40 * time.Second},
		{name: "window is capped to the max window", gap: 50 * time.Second, want: maxWindow},
		{name: "gap just below the reset limit", gap: 2*maxWindow - time.Second, want: maxWindow},
		{name: "stable content resets the window", gap: 2 * maxWindow, want: minWindow},
		{name: "min window above the max window is capped", minWindow: 2 * time.Minute, want: maxWindow},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			issue := &models.Issue{SlackPostContentUpdated: now}

			if tc.gap > 0 {
				issue.SlackPostPrevContentUpdated = now.Add(-tc.gap)
			}

			window := minWindow
			if tc.minWindow > 0 {
				window = tc.minWindow
			}

			assert.Equal(t, tc.want, contentThrottleWindow(issue, window, maxWindow))
		})
	}
}

func TestPostFingerprint(t *testing.T) {
	t.Parallel()

	issue := newUpdateTestIssue("a", "")
	issue.LastAlert.Text = "some text"

	blocks := func() *postBlocks {
		b := &postBlocks{}
		b.add(postBlockSignificant, slackapi.NewHeaderBlock(newPlainTextTextBlock(issue.LastAlert.Header)))
		b.add(postBlockContent, slackapi.NewSectionBlock(newMrkdwnTextBlock(issue.LastAlert.Text), nil, nil))
		b.add(postBlockStats, slackapi.NewContextBlock("", newMrkdwnTextBlock(time.Now().String())))
		return b
	}

	base := postFingerprint(issue, models.ActionAlert, blocks())
	require.False(t, base.IsZero())

	// The stats block is not part of the fingerprint
	assert.Equal(t, base, postFingerprint(issue, models.ActionAlert, blocks()))

	// The action is significant
	resolved := postFingerprint(issue, models.ActionResolve, blocks())
	assert.NotEqual(t, base.Significant, resolved.Significant)
	assert.Equal(t, base.Content, resolved.Content)

	// The fallback text is content
	issue.LastAlert.FallbackText = "other"
	fallback := postFingerprint(issue, models.ActionAlert, blocks())
	assert.Equal(t, base.Significant, fallback.Significant)
	assert.NotEqual(t, base.Content, fallback.Content)
}

func TestPostBlocks(t *testing.T) {
	t.Parallel()

	header := slackapi.NewHeaderBlock(newPlainTextTextBlock("header"))
	text := slackapi.NewSectionBlock(newMrkdwnTextBlock("text"), nil, nil)
	stats := slackapi.NewContextBlock("", newMrkdwnTextBlock("stats"))

	b := postBlocks{}
	b.add(postBlockSignificant, header)
	b.add(postBlockContent, text)
	b.add(postBlockStats, stats)

	assert.Equal(t, []slackapi.Block{header, text, stats}, b.blocks())
	assert.Equal(t, []slackapi.Block{header}, b.ofKind(postBlockSignificant))
	assert.Equal(t, []slackapi.Block{text}, b.ofKind(postBlockContent))
	assert.Equal(t, []slackapi.Block{stats}, b.ofKind(postBlockStats))
}
