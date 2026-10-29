package slack

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	slack "github.com/slack-go/slack"
	"github.com/slackmgr/core/internal/models"
)

const (
	// postMetadataEventType is the message metadata event type for issue posts. It is used to find posts
	// with an unknown outcome (see resolvePendingPost).
	postMetadataEventType = "slackmgr_issue"

	// postMetadataDeletedEventType is the message metadata event type for soft-deleted issue posts (posts that are
	// updated to look deleted, since they have replies). It ensures that such posts are never adopted again.
	postMetadataDeletedEventType = "slackmgr_issue_deleted"

	// postMetadataIssueIDKey is the message metadata payload key for the issue ID.
	postMetadataIssueIDKey = "issue_id"

	// pendingPostLookupMargin is subtracted from the pending time when looking up a pending post,
	// to allow for clock differences between this host and Slack.
	pendingPostLookupMargin = time.Minute

	// slackPendingPostsTotalMetric counts Slack posts with an unknown outcome, and how they were resolved.
	slackPendingPostsTotalMetric = "slack_pending_posts_total"
)

// Result label values for slackPendingPostsTotalMetric.
const (
	pendingPostResultPending          = "pending"
	pendingPostResultAdopted          = "adopted"
	pendingPostResultNotFound         = "not_found"
	pendingPostResultExpired          = "expired"
	pendingPostResultLookupFailed     = "lookup_failed"
	pendingPostResultDuplicateDeleted = "duplicate_deleted"
)

// allPendingPostResults lists every result label, used to pre-warm metric label combinations.
var allPendingPostResults = []string{ //nolint:gochecknoglobals // Constant list
	pendingPostResultPending, pendingPostResultAdopted, pendingPostResultNotFound,
	pendingPostResultExpired, pendingPostResultLookupFailed, pendingPostResultDuplicateDeleted,
}

// postMetadata returns the message metadata for an issue post. The metadata is visible to all users and apps
// in the workspace, so it must only contain the (opaque) issue ID.
func postMetadata(issue *models.Issue, method postUpdateMethod) slack.SlackMetadata {
	eventType := postMetadataEventType

	if method == postUpdateMethodUpdateDeleted {
		eventType = postMetadataDeletedEventType
	}

	return slack.SlackMetadata{
		EventType:    eventType,
		EventPayload: map[string]any{postMetadataIssueIDKey: issue.ID},
	}
}

// resolvePendingPost looks up a pending Slack post (see [models.Issue.SlackPostPendingSince]) in the channel history.
// It does nothing if the issue has no pending post.
//
//   - If found, the newest matching post is adopted as the issue post, and older matches (duplicates) are deleted.
//   - If not found, the post is assumed to not exist once the grace period has elapsed, or immediately if force is true.
//     Until then, the issue stays pending, and no new post is created.
//   - If the lookup fails, the error is returned and the issue stays pending, unless the pending post has expired.
func (c *Client) resolvePendingPost(ctx context.Context, issue *models.Issue, force bool) error {
	if !issue.HasPendingSlackPost() {
		return nil
	}

	channelID := issue.LastAlert.SlackChannelID
	logger := c.logger.WithFields(issue.LogFields()).WithField("pending_since", issue.SlackPostPendingSince)

	msgs, err := c.api.ListBotMessages(ctx, channelID, issue.SlackPostPendingSince.Add(-pendingPostLookupMargin), postMetadataEventType)
	if err != nil {
		if issue.SlackPostPendingExpired() {
			logger.Errorf("Failed to look up pending Slack post, giving up since it has expired: %s", err)
			issue.ClearSlackPostPending()
			c.metrics.CounterInc(slackPendingPostsTotalMetric, pendingPostResultExpired)

			return nil
		}

		c.metrics.CounterInc(slackPendingPostsTotalMetric, pendingPostResultLookupFailed)

		return fmt.Errorf("failed to look up pending Slack post for issue %s in channel %s: %w", issue.CorrelationID, channelID, err)
	}

	var matches []slack.Message

	for _, msg := range msgs {
		if id, ok := msg.Metadata.EventPayload[postMetadataIssueIDKey].(string); ok && id == issue.ID {
			matches = append(matches, msg)
		}
	}

	if len(matches) == 0 {
		if !force && !issue.SlackPostPendingGraceElapsed() {
			logger.Debug("Pending Slack post not found yet, waiting for grace period")
			return nil
		}

		logger.Info("Pending Slack post not found, assuming it was never created")
		issue.ClearSlackPostPending()
		c.metrics.CounterInc(slackPendingPostsTotalMetric, pendingPostResultNotFound)

		return nil
	}

	// Newest first. Slack timestamps have a fixed number of decimals, and compare correctly as strings.
	slices.SortFunc(matches, func(a, b slack.Message) int {
		return strings.Compare(b.Timestamp, a.Timestamp)
	})

	issue.RegisterSlackPostAdopted(matches[0].Timestamp)
	logger.WithField("slack_post_id", matches[0].Timestamp).Info("Pending Slack post found, adopting it")
	c.metrics.CounterInc(slackPendingPostsTotalMetric, pendingPostResultAdopted)

	for _, dup := range matches[1:] {
		if err := c.api.ChatDeleteMessage(ctx, channelID, dup.Timestamp); err != nil && !isIgnorableDeleteError(err) {
			logger.WithField("duplicate_slack_post_id", dup.Timestamp).Errorf("Failed to delete duplicate Slack post: %s", err)
			continue
		}

		logger.WithField("duplicate_slack_post_id", dup.Timestamp).Info("Deleted duplicate Slack post")
		c.metrics.CounterInc(slackPendingPostsTotalMetric, pendingPostResultDuplicateDeleted)
	}

	return nil
}
