package issues

import "github.com/peteraglen/slack-manager/core/models"

type moveRequest struct {
	UserRealName  string
	Issue         *models.Issue
	SourceChannel string
	TargetChannel string
}
