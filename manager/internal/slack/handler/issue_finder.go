package handler

import (
	"context"

	"github.com/peteraglen/slack-manager/manager/internal/models"
)

type IssueFinder interface {
	FindIssueBySlackPost(ctx context.Context, channelID string, slackPostID string, includeArchived bool) *models.Issue
}
