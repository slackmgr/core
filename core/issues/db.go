package issues

import (
	"context"

	"github.com/peteraglen/slack-manager/core/models"
)

type DB interface {
	LoadAllActiveIssues(ctx context.Context) (map[string][]*models.Issue, error)
	CreateOrUpdateIssue(ctx context.Context, issue *models.Issue) error
	UpdateIssues(ctx context.Context, issues []*models.Issue) (int, error)
	GetIssueBySlackPostID(ctx context.Context, channelID, slackPostID string) (*models.Issue, error)
	AddAlert(ctx context.Context, alert *models.Alert) (models.Future, error)
}

type Settings interface {
	GetMoveMappings(ctx context.Context, channelID string) (map[string]*models.MoveMapping, error)
	SaveMoveMapping(ctx context.Context, mapping *models.MoveMapping) error
}
