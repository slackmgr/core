package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	slackapi "github.com/slack-go/slack"
	"github.com/slackmgr/core/internal"
	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// botMessage returns a history message with issue post metadata for the given issue.
func botMessage(ts string, issue *models.Issue) slackapi.Message {
	msg := slackapi.Message{}
	msg.Timestamp = ts
	msg.Metadata = slackapi.SlackMetadata{
		EventType:    postMetadataEventType,
		EventPayload: map[string]any{postMetadataIssueIDKey: issue.ID},
	}

	return msg
}

// fakeIssueSaver is an [IssueSaver] that records the saved issues as JSON, together with the number of Slack posts at save time.
type fakeIssueSaver struct {
	api *fakeSlackAPI
	err error

	saved       [][]byte
	postsAtSave []int
}

func (f *fakeIssueSaver) SaveIssue(_ context.Context, issue types.Issue) error {
	if f.err != nil {
		return f.err
	}

	body, err := json.Marshal(issue)
	if err != nil {
		return err
	}

	f.saved = append(f.saved, body)
	f.postsAtSave = append(f.postsAtSave, len(f.api.posts))

	return nil
}

// newPendingTestIssue returns an issue with a pending post, pending since the given duration ago.
func newPendingTestIssue(name string, pendingFor time.Duration) *models.Issue {
	issue := newUpdateTestIssue(name, "")
	issue.SlackPostPendingSince = time.Now().Add(-pendingFor)

	return issue
}

func TestCreate_OutcomeUnknownRegistersPending(t *testing.T) {
	t.Parallel()

	api := &fakeSlackAPI{
		postErr: func(_ string) error {
			return fmt.Errorf("%w: %w", internal.ErrPostOutcomeUnknown, slackapi.SlackErrorResponse{Err: internal.SlackErrFatalError})
		},
	}
	c := newUpdateTestClient(t, api)

	issue := newUpdateTestIssue("a", "")

	err := c.Update(context.Background(), updateTestChannel, []*models.Issue{issue, newUpdateTestIssue("b", "")})

	require.NoError(t, err, "unknown outcome is not an error")
	assert.True(t, issue.HasPendingSlackPost())
	assert.Empty(t, issue.SlackPostID)
	assert.True(t, issue.SlackPostNeedsUpdate)
	assert.Len(t, api.posts, 2, "the other issue is still posted")

	// Within the grace period, the post is not found and not created again
	api.postErr = nil
	api.posts = nil

	require.NoError(t, c.UpdateSingleIssue(context.Background(), issue, "test"))
	assert.Empty(t, api.posts)
	assert.True(t, issue.HasPendingSlackPost())
	assert.Equal(t, 1, api.historyCalls)
}

func TestResolvePendingPost_Adopted(t *testing.T) {
	t.Parallel()

	api := &fakeSlackAPI{}
	c := newUpdateTestClient(t, api)

	issue := newPendingTestIssue("a", time.Second)
	other := newUpdateTestIssue("b", "")

	api.history = []slackapi.Message{
		botMessage("1700000003.000000", other),
		botMessage("1700000001.000000", issue),
		botMessage("1700000002.000000", issue),
	}

	err := c.Update(context.Background(), updateTestChannel, []*models.Issue{issue})

	require.NoError(t, err)
	assert.False(t, issue.HasPendingSlackPost())
	assert.Equal(t, "1700000002.000000", issue.SlackPostID, "newest match is adopted")
	assert.Equal(t, []string{"1700000001.000000"}, api.deletes, "older match is deleted as a duplicate")
	assert.Empty(t, api.posts, "no new post is created")
	assert.Equal(t, []string{"1700000002.000000"}, api.updates, "adopted post is updated with current content")
	assert.False(t, issue.SlackPostNeedsUpdate)
}

func TestResolvePendingPost_NotFoundAfterGracePeriod(t *testing.T) {
	t.Parallel()

	api := &fakeSlackAPI{}
	c := newUpdateTestClient(t, api)

	issue := newPendingTestIssue("a", models.SlackPostPendingGracePeriod+time.Second)

	err := c.Update(context.Background(), updateTestChannel, []*models.Issue{issue})

	require.NoError(t, err)
	assert.False(t, issue.HasPendingSlackPost())
	assert.Equal(t, []string{"a"}, api.posts, "a new post is created")
	assert.Equal(t, "new-a", issue.SlackPostID)
}

func TestResolvePendingPost_LookupFailed(t *testing.T) {
	t.Parallel()

	t.Run("error is returned and issue stays pending", func(t *testing.T) {
		t.Parallel()

		api := &fakeSlackAPI{historyErr: errors.New("some_unknown_error")}
		c := newUpdateTestClient(t, api)

		issue := newPendingTestIssue("a", models.SlackPostPendingGracePeriod+time.Second)
		other := newUpdateTestIssue("b", "")

		err := c.Update(context.Background(), updateTestChannel, []*models.Issue{issue, other})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "some_unknown_error")
		assert.True(t, issue.HasPendingSlackPost())
		assert.Equal(t, []string{"b"}, api.posts, "other issues are not blocked")
	})

	t.Run("aborting error skips remaining issues", func(t *testing.T) {
		t.Parallel()

		api := &fakeSlackAPI{historyErr: slackapi.SlackErrorResponse{Err: internal.SlackErrInvalidAuth}}
		c := newUpdateTestClient(t, api)

		issues := []*models.Issue{newPendingTestIssue("a", time.Second), newPendingTestIssue("b", time.Second)}

		err := c.Update(context.Background(), updateTestChannel, issues)

		require.Error(t, err)
		assert.Equal(t, 1, api.historyCalls)
	})

	t.Run("expired pending post is given up", func(t *testing.T) {
		t.Parallel()

		api := &fakeSlackAPI{historyErr: errors.New("some_unknown_error")}
		c := newUpdateTestClient(t, api)

		issue := newPendingTestIssue("a", models.SlackPostPendingMaxAge+time.Second)

		err := c.Update(context.Background(), updateTestChannel, []*models.Issue{issue})

		require.NoError(t, err)
		assert.False(t, issue.HasPendingSlackPost())
		assert.Equal(t, []string{"a"}, api.posts)
	})
}

func TestResolvePendingPost_ResolvedIssueIsStillLookedUp(t *testing.T) {
	t.Parallel()

	api := &fakeSlackAPI{}
	c := newUpdateTestClient(t, api)

	// A resolved issue whose alert post was never confirmed has no Slack action, but must still be looked up,
	// so that a post showing the alert is updated to resolved.
	issue := newPendingTestIssue("a", time.Second)
	issue.SlackAlertSentAtLeastOnce = false
	issue.LastAlert.Severity = "resolved"
	require.Equal(t, models.ActionNone, issue.GetSlackAction())

	api.history = []slackapi.Message{botMessage("1700000001.000000", issue)}

	err := c.Update(context.Background(), updateTestChannel, []*models.Issue{issue})

	require.NoError(t, err)
	assert.Equal(t, "1700000001.000000", issue.SlackPostID)
	assert.Equal(t, []string{"1700000001.000000"}, api.updates)
}

func TestDelete_PendingPost(t *testing.T) {
	t.Parallel()

	t.Run("found post is deleted", func(t *testing.T) {
		t.Parallel()

		api := &fakeSlackAPI{}
		c := newUpdateTestClient(t, api)

		issue := newPendingTestIssue("a", time.Second)
		api.history = []slackapi.Message{botMessage("1700000001.000000", issue)}

		require.NoError(t, c.Delete(context.Background(), issue, "test", false))

		assert.Equal(t, []string{"1700000001.000000"}, api.deletes)
		assert.False(t, issue.HasPendingSlackPost())
		assert.Empty(t, issue.SlackPostID)
	})

	t.Run("post not found within grace period is assumed missing", func(t *testing.T) {
		t.Parallel()

		api := &fakeSlackAPI{}
		c := newUpdateTestClient(t, api)

		issue := newPendingTestIssue("a", time.Second)

		require.NoError(t, c.Delete(context.Background(), issue, "test", false))

		assert.Empty(t, api.deletes)
		assert.False(t, issue.HasPendingSlackPost())
	})

	t.Run("lookup error is returned", func(t *testing.T) {
		t.Parallel()

		api := &fakeSlackAPI{historyErr: errors.New("some_unknown_error")}
		c := newUpdateTestClient(t, api)

		issue := newPendingTestIssue("a", time.Second)

		require.Error(t, c.Delete(context.Background(), issue, "test", false))
		assert.True(t, issue.HasPendingSlackPost())
	})
}

func TestPostMetadata(t *testing.T) {
	t.Parallel()

	api := &fakeSlackAPI{hasReplies: true}
	c := newUpdateTestClient(t, api)

	issue := newUpdateTestIssue("a", "")

	require.NoError(t, c.UpdateSingleIssue(context.Background(), issue, "test"))
	issue.SlackPostNeedsUpdate = true
	require.NoError(t, c.UpdateSingleIssue(context.Background(), issue, "test"))

	// Soft delete, since the post has replies
	require.NoError(t, c.Delete(context.Background(), issue, "test", true))

	wantPayload := `"event_payload":{"issue_id":"` + issue.ID + `"}`

	require.Len(t, api.metadata, 3)
	assert.Contains(t, api.metadata[0], `"event_type":"slackmgr_issue"`, "create")
	assert.Contains(t, api.metadata[0], wantPayload)
	assert.Contains(t, api.metadata[1], `"event_type":"slackmgr_issue"`, "update")
	assert.Contains(t, api.metadata[2], `"event_type":"slackmgr_issue_deleted"`, "soft delete")
	assert.Contains(t, api.metadata[2], wantPayload)
}

func TestCreate_WriteAhead(t *testing.T) {
	t.Parallel()

	t.Run("issue is saved as pending before posting", func(t *testing.T) {
		t.Parallel()

		api := &fakeSlackAPI{}
		c := newUpdateTestClient(t, api)
		saver := &fakeIssueSaver{api: api}
		c.SetIssueSaver(saver)

		issue := newUpdateTestIssue("a", "")

		require.NoError(t, c.UpdateSingleIssue(context.Background(), issue, "test"))

		require.Len(t, saver.saved, 1)
		assert.Equal(t, 0, saver.postsAtSave[0], "saved before posting")

		saved := &models.Issue{}
		require.NoError(t, json.Unmarshal(saver.saved[0], saved))
		assert.True(t, saved.HasPendingSlackPost())
		assert.Empty(t, saved.SlackPostID)

		assert.Equal(t, []string{"a"}, api.posts)
		assert.Equal(t, "new-a", issue.SlackPostID)
		assert.False(t, issue.HasPendingSlackPost(), "pending is cleared after posting")
	})

	t.Run("save error prevents posting", func(t *testing.T) {
		t.Parallel()

		api := &fakeSlackAPI{}
		c := newUpdateTestClient(t, api)
		c.SetIssueSaver(&fakeIssueSaver{api: api, err: errors.New("db down")})

		issue := newUpdateTestIssue("a", "")

		err := c.UpdateSingleIssue(context.Background(), issue, "test")

		require.ErrorContains(t, err, "db down")
		assert.Empty(t, api.posts)
		assert.False(t, issue.HasPendingSlackPost())
		assert.True(t, issue.SlackPostNeedsUpdate)
	})

	t.Run("updates are not saved ahead", func(t *testing.T) {
		t.Parallel()

		api := &fakeSlackAPI{}
		c := newUpdateTestClient(t, api)
		saver := &fakeIssueSaver{api: api}
		c.SetIssueSaver(saver)

		require.NoError(t, c.UpdateSingleIssue(context.Background(), newUpdateTestIssue("a", "1700000001.000000"), "test"))

		assert.Empty(t, saver.saved)
		assert.Equal(t, []string{"1700000001.000000"}, api.updates)
	})

	t.Run("failed post clears pending", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			err     error
			wantErr bool
		}{
			{name: "channel unavailable", err: slackapi.SlackErrorResponse{Err: internal.SlackErrChannelNotFound}},
			{name: "invalid content", err: slackapi.SlackErrorResponse{Err: internal.SlackErrInvalidBlocks}},
			{name: "other error", err: errors.New("some_unknown_error"), wantErr: true},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				api := &fakeSlackAPI{postErr: func(_ string) error { return tt.err }}
				c := newUpdateTestClient(t, api)
				c.SetIssueSaver(&fakeIssueSaver{api: api})

				issue := newUpdateTestIssue("a", "")

				err := c.UpdateSingleIssue(context.Background(), issue, "test")

				if tt.wantErr {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}

				assert.False(t, issue.HasPendingSlackPost())
			})
		}
	})

	t.Run("unknown outcome keeps pending", func(t *testing.T) {
		t.Parallel()

		api := &fakeSlackAPI{
			postErr: func(_ string) error {
				return fmt.Errorf("%w: %w", internal.ErrPostOutcomeUnknown, errors.New("timeout"))
			},
		}
		c := newUpdateTestClient(t, api)
		c.SetIssueSaver(&fakeIssueSaver{api: api})

		issue := newUpdateTestIssue("a", "")

		require.NoError(t, c.UpdateSingleIssue(context.Background(), issue, "test"))
		assert.True(t, issue.HasPendingSlackPost())
	})

	t.Run("post is adopted after crash before final save", func(t *testing.T) {
		t.Parallel()

		api := &fakeSlackAPI{}
		c := newUpdateTestClient(t, api)
		saver := &fakeIssueSaver{api: api}
		c.SetIssueSaver(saver)

		issue := newUpdateTestIssue("a", "")
		require.NoError(t, c.UpdateSingleIssue(context.Background(), issue, "test"))

		// Simulate a crash after posting: the in-memory issue is lost, and only the write-ahead save remains
		recovered := &models.Issue{}
		require.NoError(t, json.Unmarshal(saver.saved[0], recovered))

		api.history = []slackapi.Message{botMessage(issue.SlackPostID, recovered)}
		api.posts = nil

		require.NoError(t, c.Update(context.Background(), updateTestChannel, []*models.Issue{recovered}))

		assert.Empty(t, api.posts, "no duplicate post is created")
		assert.Equal(t, issue.SlackPostID, recovered.SlackPostID)
		assert.False(t, recovered.HasPendingSlackPost())
	})
}
