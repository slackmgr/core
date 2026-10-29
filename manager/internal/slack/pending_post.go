package slack

import (
	"context"
	"errors"
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

	// pendingPostLookupMargin is subtracted from the pending time when looking up a pending post (in addition to the
	// HTTP timeout, see pendingPostLookupWindow), to allow for clock differences between this host and Slack.
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

// ErrPendingPostUnresolved is returned by [Client.Delete] when the issue has a pending Slack post that was not found
// in the channel history yet, and the grace period has not elapsed. The post may still exist, so it can neither be
// deleted nor assumed to not exist. The issue is left unchanged, and the delete should be retried later.
var ErrPendingPostUnresolved = errors.New("pending Slack post is not resolved yet")

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
// It does nothing if the issue has no pending post. The channel history is cached (see [postHistoryCache]).
//
//   - If found, the newest matching post is adopted as the issue post, and older matches (duplicates) are deleted.
//   - If not found, the post is assumed to not exist if the grace period had elapsed when the history was read.
//     Until then, the issue stays pending, and no new post is created.
//   - If the lookup fails, the error is returned and the issue stays pending, unless the pending post has expired.
func (c *Client) resolvePendingPost(ctx context.Context, issue *models.Issue) error {
	if !issue.HasPendingSlackPost() {
		return nil
	}

	channelID := issue.LastAlert.SlackChannelID
	logger := c.logger.WithFields(issue.LogFields()).WithField("pending_since", issue.SlackPostPendingSince)

	msgs, fetchedAt, err := c.lookupPendingPosts(ctx, channelID, issue.SlackPostPendingSince)
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
		// A history read before the grace period elapsed may not include the post yet, even if it exists
		if !issue.SlackPostPendingGraceElapsedAt(fetchedAt) {
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
		dupLogger := logger.WithField("duplicate_slack_post_id", dup.Timestamp)

		err := c.api.ChatDeleteMessage(ctx, channelID, dup.Timestamp)

		switch {
		case err == nil:
			dupLogger.Info("Deleted duplicate Slack post")
			c.metrics.CounterInc(slackPendingPostsTotalMetric, pendingPostResultDuplicateDeleted)
			c.historyCache.removeMessage(channelID, dup.Timestamp)
		case isIgnorableDeleteError(err):
			dupLogger.Infof("Duplicate Slack post is no longer available, nothing to delete: %s", err)
			c.historyCache.removeMessage(channelID, dup.Timestamp)
		default:
			dupLogger.Errorf("Failed to delete duplicate Slack post: %s", err)
		}
	}

	return nil
}

// lookupPendingPosts returns the issue posts in the channel that may belong to a post pending since the given time,
// and the time the channel history was read. See [postHistoryCache].
func (c *Client) lookupPendingPosts(ctx context.Context, channelID string, pendingSince time.Time) ([]slack.Message, time.Time, error) {
	oldest := pendingSince.Add(-c.pendingPostLookupWindow())

	return c.historyCache.lookup(ctx, channelID, oldest, func(ctx context.Context) ([]slack.Message, error) {
		return c.api.ListBotMessages(ctx, channelID, oldest, postMetadataEventType)
	})
}

// prefetchPendingPosts reads the channel history once per channel for all issues with a pending post, from the oldest
// pending time, so that the lookups for the individual issues (see [Client.resolvePendingPost]) are served from the cache.
// Errors are cached, and reported by the individual lookups.
func (c *Client) prefetchPendingPosts(ctx context.Context, issues []*models.Issue) {
	oldestPending := make(map[string]time.Time)

	for _, issue := range issues {
		if !issue.HasPendingSlackPost() {
			continue
		}

		channelID := issue.LastAlert.SlackChannelID

		if since, ok := oldestPending[channelID]; !ok || issue.SlackPostPendingSince.Before(since) {
			oldestPending[channelID] = issue.SlackPostPendingSince
		}
	}

	for channelID, since := range oldestPending {
		_, _, _ = c.lookupPendingPosts(ctx, channelID, since)
	}
}

// pendingPostLookupWindow returns how far back from the pending time a pending post is looked up. A post with an unknown
// outcome is registered as pending after the failed call (see [Client.create]), but may have been created at any point
// during the call. The window therefore covers the HTTP timeout, plus [pendingPostLookupMargin] for clock differences.
func (c *Client) pendingPostLookupWindow() time.Duration {
	return time.Duration(c.cfg.SlackClient.HTTPTimeoutSeconds)*time.Second + pendingPostLookupMargin
}
