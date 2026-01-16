package manager

import (
	"context"

	"github.com/peteraglen/slack-manager/manager/internal/models"
	"golang.org/x/sync/semaphore"
)

type SlackClient interface {
	GetChannelName(ctx context.Context, channelID string) string
	IsAlertChannel(ctx context.Context, channelID string) (bool, string, error)
	Update(ctx context.Context, channelID string, allChannelIssues []*models.Issue) error
	UpdateSingleIssueWithThrottling(ctx context.Context, issue *models.Issue, reason string, issuesInChannel int) error
	UpdateSingleIssue(ctx context.Context, issue *models.Issue, reason string) error
	Delete(ctx context.Context, issue *models.Issue, reason string, updateIfMessageHasReplies bool, sem *semaphore.Weighted) error
	DeletePost(ctx context.Context, channelID, ts string) error
}
