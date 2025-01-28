package manager

import (
	"context"
	"encoding/json"

	common "github.com/peteraglen/slack-manager-common"
)

// DB is an interface for interacting with the database.
type DB interface {
	// SaveAlert saves an alert to the database (for auditing purposes).
	// The same alert may be saved multiple times, in case of errors and retries.
	//
	// A database implementation can choose to skip saving the alerts, since they are never read by the manager.
	//
	// id is the unique identifier for the alert, and body is the json formatted alert.
	SaveAlert(ctx context.Context, channelID string, alert *common.Alert) error

	// CreateOrUpdateIssue creates or updates a single issue in the database.
	//
	// id is the unique identifier for the issue, and body is the json formatted issue.
	CreateOrUpdateIssue(ctx context.Context, channelID string, issue common.Issue) error

	// UpdateIssues updates multiple existing issues in the database.
	//
	// issues is a map of issue IDs to json formatted issue bodies.
	UpdateIssues(ctx context.Context, channelID string, issues ...common.Issue) error

	// FindIssueBySlackPostID finds a single issue in the database, based on the provided channel ID and Slack post ID.
	//
	// The database implementation should return an error if the query matches multiple issues, and ["", nil, nil] if no issue is found.
	FindIssueBySlackPostID(ctx context.Context, channelID, postID string) (string, json.RawMessage, error)

	// LoadOpenIssues loads all open (non-archived) issues from the database, across all channels.
	LoadOpenIssues(ctx context.Context) (map[string]json.RawMessage, error)

	// LoadMoveMappings returns all move mappings from the database, for the specified channel ID.
	LoadMoveMappings(ctx context.Context, channelID string) ([]json.RawMessage, error)

	// SaveMoveMapping saves a move mapping to the database.
	SaveMoveMapping(ctx context.Context, channelID string, moveMapping common.MoveMapping) error
}
