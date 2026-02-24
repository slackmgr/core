package manager

import (
	"context"

	"github.com/slackmgr/core/manager/internal/models"
)

// SlackClient is the internal Slack API interface used by the coordinator and channel
// managers to query channel state and post or update Slack messages. It is implemented
// by the internal slack.Client and is exposed here to allow injection of test doubles.
type SlackClient interface {
	// GetChannelName returns the human-readable name of the given Slack channel, or an
	// empty string if the name cannot be resolved.
	GetChannelName(ctx context.Context, channelID string) string

	// IsAlertChannel reports whether channelID is a valid target for alerts.
	// Returns the channel name and any lookup error alongside the boolean result.
	IsAlertChannel(ctx context.Context, channelID string) (bool, string, error)

	// Update rebuilds and posts the consolidated issue summary message for all open
	// issues in the given channel.
	Update(ctx context.Context, channelID string, allChannelIssues []*models.Issue) error

	// UpdateSingleIssueWithThrottling updates the Slack message for one issue, applying
	// per-channel throttling when many issues are active to avoid flooding the API.
	UpdateSingleIssueWithThrottling(ctx context.Context, issue *models.Issue, reason string, issuesInChannel int) error

	// UpdateSingleIssue updates the Slack message for one issue without throttling.
	UpdateSingleIssue(ctx context.Context, issue *models.Issue, reason string) error

	// Delete removes an issue's Slack message. When updateIfMessageHasReplies is true
	// and the post already has thread replies, the message is replaced with a tombstone
	// instead of being deleted outright.
	Delete(ctx context.Context, issue *models.Issue, reason string, updateIfMessageHasReplies bool) error

	// DeletePost deletes an arbitrary Slack message identified by channelID and timestamp.
	DeletePost(ctx context.Context, channelID, ts string) error
}
